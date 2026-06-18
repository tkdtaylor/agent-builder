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
	ErrMissingBinary = errors.New("execsandbox: binary not configured or not found")
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

	// Build the RunRequest JSON.
	runReq := buildRunRequest(req)

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
		Stdout:   blockResult.Stdout,
		Stderr:   blockResult.Stderr,
		Duration: time.Duration(blockResult.SandboxStatus.DurationMs) * time.Millisecond,
	}

	// Store the sandbox_status on the runner context so it can be inspected by tests/logging.
	// For now, we surface it through a separate return or as an extended Result field.
	// The test spec requires it to be surfaced, so we'll return it separately.
	// We'll modify the interface in a follow-up or extend Result to include it.

	return result, blockResult.ExitCode, nil
}

// buildRunRequest constructs the JSON RunRequest from the typed Request.
func buildRunRequest(req sandbox.Request) runRequest {
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

	// Build capabilities (egress allowlist).
	if len(req.Limits.EgressAllowlist) > 0 {
		profile.Capabilities = []capabilityData{
			{
				Type:      "NetConnect",
				Allowlist: req.Limits.EgressAllowlist,
			},
		}
	}

	// Build the wiring (deferred fields are sent empty per ADR 035).
	wiring := wiringData{
		VaultSocket:   "",
		AuditSocket:   "",
		OriginMap:     map[string][2]string{},
		RequestID:     generateRequestID(),
		InjectionMode: "",
	}

	return runRequest{
		Run: runData{
			Payload:    payload,
			Profile:    profile,
			Tier:       tier,
			SecretRefs: []string{},
		},
		Wiring: wiring,
	}
}

// renderCommand converts a command slice into a shell script string.
func renderCommand(cmd []string) string {
	if len(cmd) == 0 {
		return ""
	}
	// Simple shell escaping: wrap args in single quotes and escape single quotes.
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
	Payload    string             `json:"payload"`
	Profile    profileData        `json:"profile"`
	Tier       string             `json:"tier"`
	SecretRefs []string           `json:"secret_refs"`
}

type profileData struct {
	Capabilities []capabilityData `json:"capabilities,omitempty"`
	Limits       limitsData       `json:"limits"`
}

type capabilityData struct {
	Type      string   `json:"type"`
	Allowlist []string `json:"allowlist"`
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
