package podman

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// TestPodmanRunnerInterface verifies the Runner satisfies sandbox.Runner.
// TC-035-01: adapter satisfies sandbox.Runner interface at compile time.
func TestPodmanRunnerInterface(t *testing.T) {
	// This is a compile-time check. If it compiles, the interface is satisfied.
	var _ sandbox.Runner = (*Runner)(nil)
}

// TestPodmanRunnerValidRequest verifies that a valid request translates to correct launcher flags.
// TC-035-02: valid request translates to correct launcher flags.
func TestPodmanRunnerValidRequest(t *testing.T) {
	tempDir := t.TempDir()
	worktreeDir := filepath.Join(tempDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	fakeStdout := filepath.Join(tempDir, "fake-stdout.txt")
	fakeStderr := filepath.Join(tempDir, "fake-stderr.txt")

	// Create a fake launcher that echoes the invocation arguments and environment.
	fakeLauncherPath := filepath.Join(tempDir, "fake-launcher.sh")
	fakeLauncherContent := `#!/bin/bash
set -e

# Write invocation details to the output file.
output_file="` + fakeStdout + `"
{
	echo "LAUNCHER_INVOKED"
	echo "args=$@"
	echo "env_cpus=${EXEC_BOX_CPUS:-unset}"
	echo "env_memory=${EXEC_BOX_MEMORY:-unset}"
	echo "env_pids_limit=${EXEC_BOX_PIDS_LIMIT:-unset}"
	while [ "$#" -gt 0 ]; do
		case "$1" in
			--worktree)
				echo "worktree=$2"
				shift 2
				;;
			--egress-allowlist)
				echo "allowlist=$2"
				if [ -f "$2" ]; then
					echo "allowlist_content:"
					cat "$2"
				fi
				shift 2
				;;
			--)
				shift
				break
				;;
			*)
				shift
				;;
		esac
	done
	echo "command=$@"
} > "$output_file"
`
	if err := os.WriteFile(fakeLauncherPath, []byte(fakeLauncherContent), 0o755); err != nil {
		t.Fatalf("failed to write fake launcher: %v", err)
	}

	runner := NewWithLauncher(fakeLauncherPath)

	req := sandbox.Request{
		Command:  []string{"echo", "hello"},
		Worktree: worktreeDir,
		Limits: sandbox.Limits{
			WallClockTimeout: 5 * time.Second,
			MemoryBytes:      2 * 1024 * 1024 * 1024, // 2g
			CPUCount:         2,
			PidsLimit:        100,
			EgressAllowlist: []string{
				"api.github.com:443",
				"registry.npmjs.org:443",
			},
		},
	}

	_, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	// Verify the fake launcher was invoked.
	output, err := os.ReadFile(fakeStdout)
	if err != nil {
		t.Fatalf("failed to read launcher output: %v", err)
	}
	outputStr := string(output)
	if !strings.Contains(outputStr, "LAUNCHER_INVOKED") {
		t.Errorf("launcher was not invoked, output:\n%s", outputStr)
	}
	if !strings.Contains(outputStr, "--worktree") {
		t.Errorf("--worktree flag not found in launcher invocation: %s", outputStr)
	}
	if !strings.Contains(outputStr, "--egress-allowlist") {
		t.Errorf("--egress-allowlist flag not found in launcher invocation: %s", outputStr)
	}
	if !strings.Contains(outputStr, "env_cpus=2") {
		t.Errorf("EXEC_BOX_CPUS environment variable not set correctly: %s", outputStr)
	}
	if !strings.Contains(outputStr, "env_memory=2g") {
		t.Errorf("EXEC_BOX_MEMORY environment variable not set correctly: %s", outputStr)
	}
	if !strings.Contains(outputStr, "env_pids_limit=100") {
		t.Errorf("EXEC_BOX_PIDS_LIMIT environment variable not set correctly: %s", outputStr)
	}

	// Verify the allowlist content.
	if !strings.Contains(outputStr, "api.github.com:443") {
		t.Errorf("allowlist entry 'api.github.com:443' not found: %s", outputStr)
	}
	if !strings.Contains(outputStr, "registry.npmjs.org:443") {
		t.Errorf("allowlist entry 'registry.npmjs.org:443' not found: %s", outputStr)
	}

	// Verify justification comments are present in the allowlist file.
	if !strings.Contains(outputStr, "#") {
		t.Errorf("justification comments not found in allowlist file: %s", outputStr)
	}
	if !strings.Contains(outputStr, "api.github.com:443") || !strings.Contains(outputStr, "#") {
		t.Errorf("allowlist entry 'api.github.com:443' missing justification comment: %s", outputStr)
	}
	if !strings.Contains(outputStr, "registry.npmjs.org:443") || !strings.Contains(outputStr, "#") {
		t.Errorf("allowlist entry 'registry.npmjs.org:443' missing justification comment: %s", outputStr)
	}

	_ = fakeStderr // unused, but prevent linter error
}

// TestPodmanRunnerNonZeroExit verifies that non-zero launcher exit returns exit code with nil adapter error.
// TC-035-03: non-zero launcher exit returns exit code with nil adapter error.
func TestPodmanRunnerNonZeroExit(t *testing.T) {
	tempDir := t.TempDir()
	worktreeDir := filepath.Join(tempDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	// Create a fake launcher that exits with code 2.
	fakeLauncherPath := filepath.Join(tempDir, "fake-launcher.sh")
	fakeLauncherContent := `#!/bin/bash
echo "launcher stdout"
echo "launcher stderr" >&2
exit 2
`
	if err := os.WriteFile(fakeLauncherPath, []byte(fakeLauncherContent), 0o755); err != nil {
		t.Fatalf("failed to write fake launcher: %v", err)
	}

	runner := NewWithLauncher(fakeLauncherPath)

	req := sandbox.Request{
		Command:  []string{"test", "command"},
		Worktree: worktreeDir,
		Limits:   sandbox.Limits{},
	}

	result, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if exitCode != 2 {
		t.Fatalf("expected exit code 2, got %d", exitCode)
	}
	if strings.TrimSpace(result.Stdout) != "launcher stdout" {
		t.Fatalf("expected stdout 'launcher stdout', got '%s'", result.Stdout)
	}
	if strings.TrimSpace(result.Stderr) != "launcher stderr" {
		t.Fatalf("expected stderr 'launcher stderr', got '%s'", result.Stderr)
	}
}

// TestPodmanRunnerInvalidCommandError verifies that invalid requests return adapter error without invoking launcher.
// TC-035-04: invalid request returns adapter error without invoking launcher.
func TestPodmanRunnerInvalidCommandError(t *testing.T) {
	tempDir := t.TempDir()
	worktreeDir := filepath.Join(tempDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	launcherCallCount := 0
	fakeLauncherPath := filepath.Join(tempDir, "fake-launcher.sh")
	fakeLauncherContent := `#!/bin/bash
# Increment call count by writing to a file.
callfile="` + filepath.Join(tempDir, "call-count") + `"
count=$(cat "$callfile" 2>/dev/null || echo 0)
echo $((count + 1)) > "$callfile"
`
	if err := os.WriteFile(fakeLauncherPath, []byte(fakeLauncherContent), 0o755); err != nil {
		t.Fatalf("failed to write fake launcher: %v", err)
	}

	runner := NewWithLauncher(fakeLauncherPath)

	tests := []struct {
		name string
		req  sandbox.Request
	}{
		{
			name: "empty command",
			req: sandbox.Request{
				Command:  []string{},
				Worktree: worktreeDir,
				Limits:   sandbox.Limits{},
			},
		},
		{
			name: "blank command",
			req: sandbox.Request{
				Command:  []string{"   "},
				Worktree: worktreeDir,
				Limits:   sandbox.Limits{},
			},
		},
		{
			name: "blank worktree",
			req: sandbox.Request{
				Command:  []string{"echo", "hello"},
				Worktree: "   ",
				Limits:   sandbox.Limits{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := runner.Run(tt.req)
			if err == nil {
				t.Fatalf("expected error, got nil")
			}

			// Verify the error wraps the expected sentinel error.
			if tt.name == "blank worktree" {
				if !errors.Is(err, ErrInvalidWorktree) {
					t.Errorf("expected error to wrap ErrInvalidWorktree, got: %v", err)
				}
			} else {
				if !errors.Is(err, sandbox.ErrInvalidCommand) {
					t.Errorf("expected error to wrap sandbox.ErrInvalidCommand, got: %v", err)
				}
			}

			// Verify the launcher was not invoked.
			callCountPath := filepath.Join(tempDir, "call-count")
			data, _ := os.ReadFile(callCountPath)
			if len(data) > 0 {
				launcherCallCount++
			}
		})
	}

	if launcherCallCount != 0 {
		t.Errorf("launcher was invoked %d times for invalid requests; expected 0", launcherCallCount)
	}
}

// TestPodmanRunnerWallClockTimeout verifies that wall-clock timeout surfaces as adapter error.
// TC-035-05: wall-clock timeout surfaces as adapter error.
func TestPodmanRunnerWallClockTimeout(t *testing.T) {
	tempDir := t.TempDir()
	worktreeDir := filepath.Join(tempDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	// Create a fake launcher that sleeps past the deadline.
	fakeLauncherPath := filepath.Join(tempDir, "fake-launcher.sh")
	fakeLauncherContent := `#!/bin/bash
sleep 5
echo "should not reach here"
`
	if err := os.WriteFile(fakeLauncherPath, []byte(fakeLauncherContent), 0o755); err != nil {
		t.Fatalf("failed to write fake launcher: %v", err)
	}

	runner := NewWithLauncher(fakeLauncherPath)

	req := sandbox.Request{
		Command:  []string{"sleep", "5"},
		Worktree: worktreeDir,
		Limits: sandbox.Limits{
			WallClockTimeout: 100 * time.Millisecond, // Very short timeout.
		},
	}

	start := time.Now()
	_, exitCode, err := runner.Run(req)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
	// Exit code should be -1 for timeout.
	if exitCode != -1 {
		t.Fatalf("expected exit code -1 for timeout, got %d", exitCode)
	}

	// Verify the subprocess was actually killed and didn't run to completion.
	// With proper process group killing, the call should return well under 5 seconds.
	// Allow some margin (1 second) for system overhead.
	if elapsed > 1500*time.Millisecond {
		t.Errorf("timeout test took too long (%v), suggests subprocess was not killed", elapsed)
	}
}

// TestPodmanRunnerZeroResourceLimits verifies that zero resource limits don't set env vars.
// TC-035-02 edge case: zero memory and zero CPU leave those env vars unset.
func TestPodmanRunnerZeroResourceLimits(t *testing.T) {
	tempDir := t.TempDir()
	worktreeDir := filepath.Join(tempDir, "worktree")
	if err := os.MkdirAll(worktreeDir, 0o755); err != nil {
		t.Fatalf("failed to create worktree: %v", err)
	}

	outputFile := filepath.Join(tempDir, "output.txt")

	// Create a fake launcher that captures environment.
	fakeLauncherPath := filepath.Join(tempDir, "fake-launcher.sh")
	fakeLauncherContent := `#!/bin/bash
{
	echo "cpus=${EXEC_BOX_CPUS:-unset}"
	echo "memory=${EXEC_BOX_MEMORY:-unset}"
} > "` + outputFile + `"
`
	if err := os.WriteFile(fakeLauncherPath, []byte(fakeLauncherContent), 0o755); err != nil {
		t.Fatalf("failed to write fake launcher: %v", err)
	}

	runner := NewWithLauncher(fakeLauncherPath)

	req := sandbox.Request{
		Command:  []string{"echo", "test"},
		Worktree: worktreeDir,
		Limits: sandbox.Limits{
			CPUCount:    0,
			MemoryBytes: 0,
		},
	}

	_, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}

	// Verify that zero values don't set env vars.
	output, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatalf("failed to read output: %v", err)
	}
	outputStr := string(output)
	if !strings.Contains(outputStr, "cpus=unset") {
		t.Errorf("EXEC_BOX_CPUS should be unset but got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "memory=unset") {
		t.Errorf("EXEC_BOX_MEMORY should be unset but got: %s", outputStr)
	}
}

// TestBytesToMemoryString tests the memory conversion helper.
func TestBytesToMemoryString(t *testing.T) {
	tests := []struct {
		bytes    int64
		expected string
	}{
		{0, "0"},
		{1024, "1k"},
		{1024 * 1024, "1m"},
		{1024 * 1024 * 1024, "1g"},
		{2 * 1024 * 1024 * 1024, "2g"},
		{512 * 1024 * 1024, "512m"},
		{1536 * 1024 * 1024, "1536m"}, // Not a clean GiB
	}

	for _, tt := range tests {
		result := bytesToMemoryString(tt.bytes)
		if result != tt.expected {
			t.Errorf("bytesToMemoryString(%d) = %s, expected %s", tt.bytes, result, tt.expected)
		}
	}
}

// TestPodmanRunnerLive is an optional live test that exercises the real launcher.
// TC-035-06: live adapter probe runs inside the execution-box.
// This test is skipped unless AGENT_BUILDER_LIVE_PODMAN=1 is set and Podman is available.
func TestPodmanRunnerLive(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_LIVE_PODMAN") != "1" {
		t.Skip("live Podman test skipped; set AGENT_BUILDER_LIVE_PODMAN=1 to run")
	}

	// Check if Podman is available.
	launcherPath := "containment/execution-box/run.sh"
	if _, err := os.Stat(launcherPath); err != nil {
		t.Skipf("launcher not found at %s", launcherPath)
	}

	runner := New()

	// Get the current working directory as the worktree.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	// Find the project root by looking for the containment directory.
	for {
		if _, err := os.Stat(filepath.Join(cwd, "containment")); err == nil {
			break
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatalf("could not find project root")
		}
		cwd = parent
	}

	// The execution-box image is ENTRYPOINT ["/bin/sh"] (ADR 032): the command is
	// handed to /bin/sh as its args, so commands must be sh-compatible (`-c <script>`),
	// not exec-style argv. ["echo","hello"] would become `sh echo hello` (open file
	// "echo" as a script → fails); use `sh -c "echo hello"` instead.
	req := sandbox.Request{
		Command:  []string{"-c", "echo hello"},
		Worktree: cwd,
		Limits: sandbox.Limits{
			WallClockTimeout: 30 * time.Second,
		},
	}

	result, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Fatalf("expected stdout to contain 'hello', got: %s", result.Stdout)
	}
}
