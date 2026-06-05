// Package sandboxruntime adapts the @anthropic-ai/sandbox-runtime CLI to the
// repo-owned exec-sandbox run() seam.
package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

const defaultCLIPath = "srt"

var (
	ErrInvalidWorktree       = errors.New("sandbox-runtime: invalid worktree")
	ErrInvalidAllowlistEntry = errors.New("sandbox-runtime: invalid egress allowlist entry")
)

// Config controls the sandbox-runtime adapter.
type Config struct {
	CLIPath string
}

// Runner invokes the sandbox-runtime CLI behind the sandbox.Runner interface.
type Runner struct {
	cliPath string
}

var _ sandbox.Runner = (*Runner)(nil)

// New constructs a sandbox-runtime-backed Runner.
func New(config Config) *Runner {
	cliPath := strings.TrimSpace(config.CLIPath)
	if cliPath == "" {
		cliPath = defaultCLIPath
	}
	return &Runner{cliPath: cliPath}
}

// Run executes req.Command through `srt --settings <generated-settings>`.
func (r *Runner) Run(req sandbox.Request) (sandbox.Result, int, error) {
	if err := sandbox.ValidateRequest(req); err != nil {
		return sandbox.Result{}, 0, err
	}
	worktree, err := validateWorktree(req.Worktree)
	if err != nil {
		return sandbox.Result{}, 0, err
	}
	settings, err := settingsFromRequest(req, worktree)
	if err != nil {
		return sandbox.Result{}, 0, err
	}

	settingsDir, err := os.MkdirTemp("", "agent-builder-srt-")
	if err != nil {
		return sandbox.Result{}, 0, fmt.Errorf("sandbox-runtime: create settings dir: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(settingsDir)
	}()

	settingsPath := filepath.Join(settingsDir, "settings.json")
	if err := writeSettings(settingsPath, settings); err != nil {
		return sandbox.Result{}, 0, err
	}

	ctx := context.Background()
	cancel := func() {}
	if req.Limits.WallClockTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, req.Limits.WallClockTimeout)
	}
	defer cancel()

	args := append([]string{"--settings", settingsPath}, req.Command...)
	cmd := exec.CommandContext(ctx, r.cliPath, args...)
	cmd.Dir = worktree

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err = cmd.Run()
	duration := time.Since(start)

	result := sandbox.Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: duration,
	}

	if ctxErr := ctx.Err(); ctxErr != nil {
		return result, -1, ctxErr
	}
	if err == nil {
		return result, 0, nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return result, exitErr.ExitCode(), nil
	}
	return result, 0, fmt.Errorf("sandbox-runtime: invoke %s: %w", r.cliPath, err)
}

type sandboxRuntimeSettings struct {
	Network                      networkSettings     `json:"network"`
	Filesystem                   filesystemSettings  `json:"filesystem"`
	IgnoreViolations             map[string][]string `json:"ignoreViolations"`
	EnableWeakerNestedSandbox    bool                `json:"enableWeakerNestedSandbox"`
	EnableWeakerNetworkIsolation bool                `json:"enableWeakerNetworkIsolation"`
	AllowAppleEvents             bool                `json:"allowAppleEvents"`
}

type networkSettings struct {
	AllowedDomains    []string `json:"allowedDomains"`
	DeniedDomains     []string `json:"deniedDomains"`
	AllowLocalBinding bool     `json:"allowLocalBinding"`
}

type filesystemSettings struct {
	DenyRead   []string `json:"denyRead"`
	AllowRead  []string `json:"allowRead"`
	AllowWrite []string `json:"allowWrite"`
	DenyWrite  []string `json:"denyWrite"`
}

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

func settingsFromRequest(req sandbox.Request, worktree string) (sandboxRuntimeSettings, error) {
	allowedDomains, err := allowedDomains(req.Limits.EgressAllowlist)
	if err != nil {
		return sandboxRuntimeSettings{}, err
	}

	return sandboxRuntimeSettings{
		Network: networkSettings{
			AllowedDomains:    allowedDomains,
			DeniedDomains:     []string{},
			AllowLocalBinding: false,
		},
		Filesystem: filesystemSettings{
			DenyRead: []string{
				"~/.ssh",
				"~/.aws",
				"~/.config",
				"~/.gnupg",
			},
			AllowRead:  []string{worktree},
			AllowWrite: []string{worktree, os.TempDir()},
			DenyWrite:  []string{".env"},
		},
		IgnoreViolations:             map[string][]string{},
		EnableWeakerNestedSandbox:    false,
		EnableWeakerNetworkIsolation: false,
		AllowAppleEvents:             false,
	}, nil
}

func allowedDomains(entries []string) ([]string, error) {
	seen := make(map[string]struct{}, len(entries))
	domains := make([]string, 0, len(entries))
	for _, raw := range entries {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		host, err := hostFromAllowlistEntry(value)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[host]; ok {
			continue
		}
		seen[host] = struct{}{}
		domains = append(domains, host)
	}
	sort.Strings(domains)
	return domains, nil
}

func hostFromAllowlistEntry(value string) (string, error) {
	if strings.Contains(value, "://") || strings.ContainsAny(value, "/\\*% \t") {
		return "", fmt.Errorf("%w: %s", ErrInvalidAllowlistEntry, value)
	}

	host := value
	if strings.Contains(value, ":") {
		parsedHost, port, err := net.SplitHostPort(value)
		if err != nil {
			parts := strings.Split(value, ":")
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return "", fmt.Errorf("%w: %s", ErrInvalidAllowlistEntry, value)
			}
			if _, portErr := strconv.Atoi(parts[1]); portErr != nil {
				return "", fmt.Errorf("%w: %s", ErrInvalidAllowlistEntry, value)
			}
			host = parts[0]
		} else {
			if _, portErr := strconv.Atoi(port); portErr != nil {
				return "", fmt.Errorf("%w: %s", ErrInvalidAllowlistEntry, value)
			}
			host = parsedHost
		}
	}

	host = strings.ToLower(strings.Trim(host, "[]"))
	if host == "" || net.ParseIP(host) != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidAllowlistEntry, value)
	}
	return host, nil
}

func writeSettings(path string, settings sandboxRuntimeSettings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("sandbox-runtime: marshal settings: %w", err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("sandbox-runtime: write settings: %w", err)
	}
	return nil
}
