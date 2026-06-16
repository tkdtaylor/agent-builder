// Package podman adapts the containment/execution-box/run.sh launcher to the
// repo-owned exec-sandbox run() seam.
package podman

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

const (
	launcherPath = "containment/execution-box/run.sh"
)

var (
	ErrInvalidWorktree = errors.New("podman: invalid worktree")
)

// Runner invokes the execution-box launcher behind the sandbox.Runner interface.
type Runner struct {
	launcherPath string
}

var _ sandbox.Runner = (*Runner)(nil)

// New constructs a podman-backed Runner with the standard launcher path.
func New() *Runner {
	return &Runner{
		launcherPath: launcherPath,
	}
}

// NewWithLauncher constructs a podman-backed Runner with a custom launcher path.
// Used primarily for testing with a fake launcher.
func NewWithLauncher(path string) *Runner {
	return &Runner{
		launcherPath: path,
	}
}

// Run executes req.Command through the execution-box launcher.
func (r *Runner) Run(req sandbox.Request) (sandbox.Result, int, error) {
	if err := sandbox.ValidateRequest(req); err != nil {
		return sandbox.Result{}, 0, err
	}

	worktree, err := validateWorktree(req.Worktree)
	if err != nil {
		return sandbox.Result{}, 0, err
	}

	// Create a temporary egress allowlist file if needed.
	var allowlistPath string
	if len(req.Limits.EgressAllowlist) > 0 {
		tempFile, err := createEgressAllowlist(req.Limits.EgressAllowlist)
		if err != nil {
			return sandbox.Result{}, 0, err
		}
		allowlistPath = tempFile
		defer func() {
			_ = os.Remove(allowlistPath)
		}()
	}

	// Build the launcher invocation.
	args := []string{}

	// Add --worktree flag.
	args = append(args, "--worktree", worktree)

	// Add --egress-allowlist flag if provided.
	if allowlistPath != "" {
		args = append(args, "--egress-allowlist", allowlistPath)
	}

	// Add resource limit environment variables.
	env := os.Environ()
	if req.Limits.CPUCount > 0 {
		env = append(env, fmt.Sprintf("EXEC_BOX_CPUS=%d", req.Limits.CPUCount))
	}
	if req.Limits.MemoryBytes > 0 {
		// Convert bytes to human-readable format for Podman.
		env = append(env, fmt.Sprintf("EXEC_BOX_MEMORY=%s", bytesToMemoryString(req.Limits.MemoryBytes)))
	}
	if req.Limits.PidsLimit > 0 {
		env = append(env, fmt.Sprintf("EXEC_BOX_PIDS_LIMIT=%d", req.Limits.PidsLimit))
	}

	// Add command separator and the command itself.
	args = append(args, "--")
	args = append(args, req.Command...)

	ctx := context.Background()
	cancel := func() {}
	if req.Limits.WallClockTimeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, req.Limits.WallClockTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(ctx, r.launcherPath, args...)
	cmd.Dir = worktree
	cmd.Env = env

	// Set up process group so we can kill the entire group on context deadline.
	// This ensures child processes (e.g., sleep) are killed when the context is cancelled.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// Configure the process to be killed as a group when the context is cancelled.
	cmd.Cancel = func() error {
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil
		}
		if cmd.Process != nil {
			// Kill the entire process group (negative PID).
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}

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
	return result, 0, fmt.Errorf("podman: invoke %s: %w", r.launcherPath, err)
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

// createEgressAllowlist creates a temporary file containing the egress allowlist entries.
// Each entry is formatted as "host:port # justification comment".
func createEgressAllowlist(entries []string) (string, error) {
	tempFile, err := os.CreateTemp("", "agent-builder-egress-*.allowlist")
	if err != nil {
		return "", fmt.Errorf("podman: create allowlist temp file: %w", err)
	}
	defer func() {
		_ = tempFile.Close()
	}()

	for _, entry := range entries {
		// Each allowlist entry must be in "host:port # comment" format.
		// If the entry is just "host:port", add a default comment.
		line := entry
		if !strings.Contains(entry, "#") {
			line = entry + " # agent-builder egress allowlist entry"
		}
		if _, err := tempFile.WriteString(line + "\n"); err != nil {
			_ = os.Remove(tempFile.Name())
			return "", fmt.Errorf("podman: write allowlist entry: %w", err)
		}
	}

	return tempFile.Name(), nil
}

// bytesToMemoryString converts bytes to a human-readable memory string for Podman.
func bytesToMemoryString(bytes int64) string {
	if bytes == 0 {
		return "0"
	}

	units := []struct {
		name  string
		bytes int64
	}{
		{"g", 1024 * 1024 * 1024},
		{"m", 1024 * 1024},
		{"k", 1024},
	}

	for _, unit := range units {
		if bytes%unit.bytes == 0 {
			return strconv.FormatInt(bytes/unit.bytes, 10) + unit.name
		}
	}

	return strconv.FormatInt(bytes, 10)
}
