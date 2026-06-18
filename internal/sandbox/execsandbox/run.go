// Package execsandbox adapts the shipped exec-sandbox block binary to the
// repo-owned exec-sandbox run() seam (sandbox.Runner interface).
package execsandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

var (
	ErrMissingBinary   = errors.New("execsandbox: binary not configured or not found")
	ErrInvalidWorktree = errors.New("execsandbox: invalid worktree")
)

// Runner invokes the exec-sandbox block binary behind the sandbox.Runner interface.
type Runner struct {
	binPath string
}

var _ sandbox.Runner = (*Runner)(nil)

// New constructs an exec-sandbox-backed Runner with the given binary path.
// It does not validate the path until Run() is called; that way misconfiguration
// fails at first use, not at construction.
func New(binPath string) *Runner {
	return &Runner{
		binPath: strings.TrimSpace(binPath),
	}
}

// Run executes req.Command through the exec-sandbox block binary.
// It marshals the Request/Limits into the block's JSON RunRequest contract,
// execs the binary, writes the JSON to stdin, reads the result from stdout,
// and parses it back into Result plus exit code and SandboxStatus.
func (r *Runner) Run(req sandbox.Request) (sandbox.Result, int, error) {
	if err := sandbox.ValidateRequest(req); err != nil {
		return sandbox.Result{}, 0, err
	}

	// Fail fast if binary is not configured.
	if r.binPath == "" {
		return sandbox.Result{}, 0, fmt.Errorf("%w: AGENT_BUILDER_EXEC_SANDBOX_BIN is not set", ErrMissingBinary)
	}

	// Validate that the binary exists and is executable.
	absPath, err := filepath.Abs(r.binPath)
	if err != nil {
		return sandbox.Result{}, 0, fmt.Errorf("%w: cannot resolve binary path %q: %v", ErrMissingBinary, r.binPath, err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return sandbox.Result{}, 0, fmt.Errorf("%w: binary %q does not exist or is not readable: %v", ErrMissingBinary, absPath, err)
	}
	if info.IsDir() {
		return sandbox.Result{}, 0, fmt.Errorf("%w: %q is a directory, not an executable", ErrMissingBinary, absPath)
	}
	if (info.Mode() & 0o111) == 0 {
		return sandbox.Result{}, 0, fmt.Errorf("%w: %q is not executable", ErrMissingBinary, absPath)
	}

	// Validate worktree if non-empty (must be an absolute existing directory).
	worktree := ""
	if strings.TrimSpace(req.Worktree) != "" {
		validatedWorktree, err := validateWorktree(req.Worktree)
		if err != nil {
			return sandbox.Result{}, 0, err
		}
		worktree = validatedWorktree
	}

	// Validate egress allowlist early (fail loud before building the request).
	var egressParseErr error
	if len(req.Limits.EgressAllowlist) > 0 {
		_, egressParseErr = parseEgressAllowlist(req.Limits.EgressAllowlist)
	}

	// Build the RunRequest JSON.
	runReq, err := buildRunRequest(req, worktree, egressParseErr)
	if err != nil {
		return sandbox.Result{}, 0, err
	}

	// Marshal it to JSON.
	reqJSON, err := json.Marshal(runReq)
	if err != nil {
		return sandbox.Result{}, 0, fmt.Errorf("execsandbox: marshal request JSON: %w", err)
	}

	// Invoke the block binary with "run" subcommand.
	cmd := exec.Command(absPath, "run")

	// Write the JSON to stdin.
	cmd.Stdin = bytes.NewReader(reqJSON)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	_ = time.Since(start) // Duration is captured from the block's sandbox_status

	// Check for process exit errors.
	var exitCode int
	var exitErr *exec.ExitError
	if err == nil {
		exitCode = 0
	} else if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	} else {
		// Non-exit error (e.g. binary not found, signal killed).
		return sandbox.Result{}, 0, fmt.Errorf("execsandbox: invoke %q: %w", absPath, err)
	}

	// Non-zero exit is a loud error (not a result with that exit code).
	if exitCode != 0 {
		stderrStr := stderr.String()
		return sandbox.Result{}, exitCode, fmt.Errorf("execsandbox: binary exited %d; stderr: %s", exitCode, strings.TrimSpace(stderrStr))
	}

	// Parse the JSON result from stdout.
	resultJSON := stdout.Bytes()
	if len(resultJSON) == 0 {
		return sandbox.Result{}, 0, fmt.Errorf("execsandbox: binary exited 0 but produced no output")
	}

	var blockResult blockResult
	if err := json.Unmarshal(resultJSON, &blockResult); err != nil {
		return sandbox.Result{}, 0, fmt.Errorf("execsandbox: parse result JSON: invalid JSON from binary: %w", err)
	}

	// Check for error in the result JSON.
	if blockResult.Error != "" {
		return sandbox.Result{}, 0, fmt.Errorf("execsandbox: block returned error: %s", blockResult.Error)
	}

	// Surface the sandbox_status and build the Result.
	result := sandbox.Result{
		Stdout:    blockResult.Stdout,
		Stderr:    blockResult.Stderr,
		Duration:  time.Duration(blockResult.SandboxStatus.DurationMs) * time.Millisecond,
		SandboxID: blockResult.SandboxStatus.SandboxID,
		Tier:      blockResult.SandboxStatus.Tier,
		Status:    blockResult.SandboxStatus.Status,
		Degraded:  extractDegraded(blockResult.SandboxStatus.Limits),
	}

	return result, blockResult.ExitCode, nil
}

// validateWorktree validates that the worktree is an absolute existing directory.
// It returns the absolute path if valid, or an error if the path is invalid/nonexistent/not-a-dir.
func validateWorktree(worktree string) (string, error) {
	if strings.TrimSpace(worktree) == "" {
		return "", ErrInvalidWorktree
	}
	abs, err := filepath.Abs(worktree)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidWorktree, err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidWorktree, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%w: not a directory: %s", ErrInvalidWorktree, abs)
	}
	return abs, nil
}

// discoverGoroot finds the Go toolchain root via `go env GOROOT`, falling back to
// AGENT_BUILDER_EXEC_SANDBOX_GOROOT env var if set.
func discoverGoroot() (string, error) {
	if envPath := os.Getenv("AGENT_BUILDER_EXEC_SANDBOX_GOROOT"); envPath != "" {
		return envPath, nil
	}
	// Query go env GOROOT
	cmd := exec.Command("go", "env", "GOROOT")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("discoverGoroot: %w", err)
	}
	goroot := strings.TrimSpace(string(out))
	if goroot == "" {
		return "", errors.New("discoverGoroot: go env GOROOT returned empty")
	}
	return goroot, nil
}

// discoverGateTools finds the gate-tools directory via AGENT_BUILDER_GATE_TOOLS env var,
// falling back to containment/execution-box/gate-tools relative to the repo root.
func discoverGateTools(repoRoot string) (string, error) {
	if envPath := os.Getenv("AGENT_BUILDER_GATE_TOOLS"); envPath != "" {
		// Verify it exists
		if info, err := os.Stat(envPath); err == nil && info.IsDir() {
			return envPath, nil
		}
		// If env var is set but path doesn't exist, don't silently fall back
		return "", fmt.Errorf("AGENT_BUILDER_GATE_TOOLS set to %q but path does not exist or is not readable", envPath)
	}
	// Default path: containment/execution-box/gate-tools
	defaultPath := filepath.Join(repoRoot, "containment", "execution-box", "gate-tools")
	if info, err := os.Stat(defaultPath); err == nil && info.IsDir() {
		return defaultPath, nil
	}
	// If default doesn't exist, return it anyway (gate-tools is optional for base functionality)
	return defaultPath, nil
}

// parseEgressAllowlist converts ["host:port", ...] to map[host] -> [host, port]
func parseEgressAllowlist(allowlist []string) (map[string][2]string, error) {
	result := make(map[string][2]string)
	for _, entry := range allowlist {
		parts := strings.SplitN(entry, ":", 2)
		if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
			return nil, fmt.Errorf("invalid egress allowlist entry: %q (expected host:port)", entry)
		}
		host := parts[0]
		port := parts[1]
		result[host] = [2]string{host, port}
	}
	return result, nil
}

// findRepoRoot searches for the agent-builder repo root by looking for containment/execution-box.
// It starts from the current working directory and walks up the tree.
func findRepoRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	// Try up to 10 levels up the directory tree
	path := cwd
	for i := 0; i < 10; i++ {
		marker := filepath.Join(path, "containment", "execution-box")
		if info, err := os.Stat(marker); err == nil && info.IsDir() {
			return path, nil
		}
		// Also check for CLAUDE.md as another marker of the repo root
		if info, err := os.Stat(filepath.Join(path, "CLAUDE.md")); err == nil && !info.IsDir() {
			// Verify it's the agent-builder repo by checking for containment subdir nearby
			if info, err := os.Stat(filepath.Join(path, "containment")); err == nil && info.IsDir() {
				return path, nil
			}
		}

		parent := filepath.Dir(path)
		if parent == path {
			break // Reached filesystem root
		}
		path = parent
	}

	return "", errors.New("could not find agent-builder repo root (no containment/execution-box marker)")
}

// buildRunRequest constructs the JSON RunRequest from the typed Request.
// The worktree parameter is the validated absolute path; "" means no mount.
// This function cannot return an error internally, so egress allowlist errors
// must be caught by the caller (Run method).
func buildRunRequest(req sandbox.Request, worktree string, egressParseErr error) (runRequest, error) {
	// Fail loud if egress allowlist parsing failed.
	if egressParseErr != nil {
		return runRequest{}, egressParseErr
	}

	// Render the command as a shell script string.
	payload := renderCommand(req.Command)

	// Determine the tier.
	tier := req.Tier
	if tier == "" {
		tier = "bubblewrap"
	}

	// Build the profile.
	profile := profileData{
		Limits: limitsData{
			CPUCount:   req.Limits.CPUCount,
			MemoryMB:   int(req.Limits.MemoryBytes / (1024 * 1024)),
			Pids:       req.Limits.PidsLimit,
			DiskMB:     0, // Not yet on the typed seam (ADR 035).
			TimeoutSec: int(req.Limits.WallClockTimeout.Seconds()),
		},
	}

	// Discover toolchain and gate-tools. FileRead mounts the whole directory
	// read-only; PATH points at the dir that actually holds the executables —
	// for GOROOT that is GOROOT/bin (where `go`/`gofmt` live), not GOROOT itself.
	var fileReadPaths []string
	var pathDirs []string
	if goroot, err := discoverGoroot(); err == nil {
		fileReadPaths = append(fileReadPaths, goroot)
		pathDirs = append(pathDirs, filepath.Join(goroot, "bin"))
	}
	if repoRoot, err := findRepoRoot(); err == nil {
		if gateTools, err := discoverGateTools(repoRoot); err == nil {
			fileReadPaths = append(fileReadPaths, gateTools)
			pathDirs = append(pathDirs, gateTools) // scanners live directly in the gate-tools dir
		}
	}

	// Build environment (PATH) for the payload.
	env := make(map[string]string)
	if len(pathDirs) > 0 {
		pathDirs = append(pathDirs, "/usr/local/sbin", "/usr/local/bin", "/usr/sbin", "/usr/bin", "/sbin", "/bin")
		env["PATH"] = strings.Join(pathDirs, ":")
	}

	// Build capabilities.
	var capabilities []capabilityData

	// Add FileRead capability if we have paths to mount
	if len(fileReadPaths) > 0 {
		capabilities = append(capabilities, capabilityData{
			Type:  "FileRead",
			Paths: fileReadPaths,
		})
	}

	// Add NetConnect capability for egress allowlist
	if len(req.Limits.EgressAllowlist) > 0 {
		capabilities = append(capabilities, capabilityData{
			Type:      "NetConnect",
			Allowlist: req.Limits.EgressAllowlist,
		})
	}

	profile.Capabilities = capabilities

	// Build the wiring.
	wiring := wiringData{
		VaultSocket:   "",
		AuditSocket:   "",
		OriginMap:     map[string][2]string{},
		RequestID:     generateRequestID(),
		InjectionMode: "",
	}

	// Populate origin_map from EgressAllowlist (if we get here, parse already succeeded)
	if len(req.Limits.EgressAllowlist) > 0 {
		if originMap, err := parseEgressAllowlist(req.Limits.EgressAllowlist); err == nil {
			wiring.OriginMap = originMap
		}
	}

	return runRequest{
		Run: runData{
			Payload:    payload,
			Profile:    profile,
			Tier:       tier,
			SecretRefs: []string{},
			Workdir:    worktree,
			Env:        env,
		},
		Wiring: wiring,
	}, nil
}

// extractDegraded extracts the degraded resource list from the limits object.
// The limits object is unmarshaled as interface{} and may contain a "degraded" field.
func extractDegraded(limitsObj any) []string {
	if limitsObj == nil {
		return nil
	}
	limitsMap, ok := limitsObj.(map[string]interface{})
	if !ok {
		return nil
	}
	degradedRaw, ok := limitsMap["degraded"]
	if !ok {
		return nil
	}
	degradedSlice, ok := degradedRaw.([]interface{})
	if !ok {
		return nil
	}
	result := make([]string, 0, len(degradedSlice))
	for _, item := range degradedSlice {
		if str, ok := item.(string); ok {
			result = append(result, str)
		}
	}
	return result
}

// renderCommand converts a command slice into a shell script string for the
// block's payload (which runs as `/usr/bin/sh /payload.sh`).
//
// agent-builder commands target a /bin/sh entrypoint (ADR 032): they are
// arguments to sh, most commonly `-c <script>`. Because the block already wraps
// the payload in sh, the `sh -c <script>` form must become just <script> — not
// a quoted `'-c' '<script>'` line, which sh would try to run as a command named
// "-c" (exit 127).
func renderCommand(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	rest := cmd
	// Skip an optional leading shell token (sh, /bin/sh, /usr/bin/sh, bash).
	if base := filepath.Base(rest[0]); base == "sh" || base == "bash" {
		rest = rest[1:]
	}
	// `sh -c <script> [name [args...]]` → the script body is the single arg after -c.
	if len(rest) >= 2 && rest[0] == "-c" {
		return rest[1]
	}
	// Fallback: treat as a direct command line, shell-quote each arg.
	parts := make([]string, len(cmd))
	for i, arg := range cmd {
		parts[i] = shellQuote(arg)
	}
	return strings.Join(parts, " ")
}

// shellQuote wraps a string in single quotes and escapes internal single quotes.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// If the string contains no special characters, return as-is (for readability).
	if !strings.ContainsAny(s, " \t\n'\"\\$`!") {
		return s
	}
	// Wrap in single quotes and escape internal single quotes.
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// generateRequestID generates a unique request ID (UUID-like).
func generateRequestID() string {
	b := make([]byte, 8)
	n, err := os.Open("/dev/urandom")
	if err == nil {
		defer func() { _ = n.Close() }()
		_, _ = n.Read(b)
		return fmt.Sprintf("req-%x", b[:4])
	}
	// Fallback: use a simple counter-based ID (for tests).
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

// --- JSON structures for the block contract ---

type runRequest struct {
	Run    runData    `json:"run"`
	Wiring wiringData `json:"wiring"`
}

type runData struct {
	Payload    string              `json:"payload"`
	Profile    profileData         `json:"profile"`
	Tier       string              `json:"tier"`
	SecretRefs []string            `json:"secret_refs"`
	Workdir    string              `json:"workdir"`
	Env        map[string]string   `json:"env,omitempty"`
}

type profileData struct {
	Capabilities []capabilityData `json:"capabilities,omitempty"`
	Limits       limitsData       `json:"limits"`
}

type capabilityData struct {
	Type      string        `json:"type"`
	Allowlist []string      `json:"allowlist,omitempty"`
	Paths     []string      `json:"paths,omitempty"`
}

type limitsData struct {
	CPUCount   int `json:"cpu_count"`
	MemoryMB   int `json:"memory_mb"`
	Pids       int `json:"pids"`
	DiskMB     int `json:"disk_mb"`
	TimeoutSec int `json:"timeout_sec"`
}

type wiringData struct {
	VaultSocket   string               `json:"vault_socket"`
	AuditSocket   string               `json:"audit_socket"`
	OriginMap     map[string][2]string `json:"origin_map"`
	RequestID     string               `json:"request_id"`
	InjectionMode string               `json:"injection_mode"`
}

type blockResult struct {
	Stdout        string `json:"stdout"`
	Stderr        string `json:"stderr"`
	ExitCode      int    `json:"exit_code"`
	Error         string `json:"error"`
	SandboxStatus struct {
		SandboxID       string `json:"sandbox_id"`
		Tier            string `json:"tier"`
		DurationMs      int64  `json:"duration_ms"`
		SecretsInjected []any  `json:"secrets_injected"`
		Status          string `json:"status"`
		Limits          any    `json:"limits"`
	} `json:"sandbox_status"`
}
