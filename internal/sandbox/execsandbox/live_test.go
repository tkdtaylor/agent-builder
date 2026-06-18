// +build !short

package execsandbox

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
)

// TestExecSandboxLive is a gated live test (L6) that runs the real exec-sandbox binary
// against both bubblewrap and gvisor tiers, exercising the full adapter.
// Gate: AGENT_BUILDER_LIVE_EXEC_SANDBOX=1
// Binary path: AGENT_BUILDER_EXEC_SANDBOX_BIN
// TC-062-08
func TestExecSandboxLive(t *testing.T) {
	// Gate: require explicit env var to run.
	if os.Getenv("AGENT_BUILDER_LIVE_EXEC_SANDBOX") != "1" {
		t.Skip("set AGENT_BUILDER_LIVE_EXEC_SANDBOX=1 for live block run")
	}

	// Read the binary path from env.
	binPath := os.Getenv("AGENT_BUILDER_EXEC_SANDBOX_BIN")
	if binPath == "" {
		t.Skip("AGENT_BUILDER_EXEC_SANDBOX_BIN not set; skipping live test")
	}

	runner := New(binPath)

	t.Run("bubblewrap_timeout_fires", func(t *testing.T) {
		// TC-062-08: bubblewrap with a 1-second timeout on a 5-second sleep.
		// Expect: timeout status, non-zero exit, stdout shows START but not SHOULD_NOT_PRINT.
		req := sandbox.Request{
			Command: []string{"sh", "-c", "echo START; sleep 5; echo SHOULD_NOT_PRINT"},
			Worktree: "/tmp",
			Tier:     "bubblewrap",
			Limits: sandbox.Limits{
				WallClockTimeout: 1 * time.Second,
			},
		}

		result, exitCode, err := runner.Run(req)
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}

		// Timeout should result in a non-zero exit and status="timeout".
		if exitCode == 0 {
			t.Errorf("Expected non-zero exit code for timeout, got 0; result: %+v", result)
		}

		if result.Status != "timeout" {
			t.Errorf("Expected status 'timeout', got %q", result.Status)
		}

		if result.Tier != "bubblewrap" {
			t.Errorf("Expected tier 'bubblewrap', got %q", result.Tier)
		}

		// START should be in stdout (the echo before sleep).
		if !contains(result.Stdout, "START") {
			t.Errorf("Expected stdout to contain 'START', got: %q", result.Stdout)
		}

		// SHOULD_NOT_PRINT should NOT be in stdout (sleep was killed).
		if contains(result.Stdout, "SHOULD_NOT_PRINT") {
			t.Errorf("Expected stdout to NOT contain 'SHOULD_NOT_PRINT', got: %q", result.Stdout)
		}

		// SandboxID should be populated.
		if result.SandboxID == "" {
			t.Errorf("Expected non-empty SandboxID, got empty")
		}

		t.Logf("bubblewrap_timeout_fires: status=%s, tier=%s, exit=%d, stdout=%q, SandboxID=%s",
			result.Status, result.Tier, exitCode, result.Stdout, result.SandboxID)
	})

	t.Run("gvisor_clean_run", func(t *testing.T) {
		// TC-062-08: gvisor with a simple echo command and generous timeout.
		// Expect: clean status, exit 0, stdout contains HELLO_FROM_GVISOR.
		req := sandbox.Request{
			Command: []string{"echo", "HELLO_FROM_GVISOR"},
			Worktree: "/tmp",
			Tier:     "gvisor",
			Limits: sandbox.Limits{
				WallClockTimeout: 10 * time.Second,
				MemoryBytes:      256 * 1024 * 1024, // 256 MB
			},
		}

		result, exitCode, err := runner.Run(req)
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}

		if exitCode != 0 {
			t.Errorf("Expected exit code 0, got %d; stderr: %q", exitCode, result.Stderr)
		}

		if result.Status != "clean" {
			t.Errorf("Expected status 'clean', got %q", result.Status)
		}

		if result.Tier != "gvisor" {
			t.Errorf("Expected tier 'gvisor', got %q", result.Tier)
		}

		// HELLO_FROM_GVISOR should be in stdout.
		if !contains(result.Stdout, "HELLO_FROM_GVISOR") {
			t.Errorf("Expected stdout to contain 'HELLO_FROM_GVISOR', got: %q", result.Stdout)
		}

		// SandboxID should be populated.
		if result.SandboxID == "" {
			t.Errorf("Expected non-empty SandboxID, got empty")
		}

		t.Logf("gvisor_clean_run: status=%s, tier=%s, exit=%d, stdout=%q, SandboxID=%s",
			result.Status, result.Tier, exitCode, result.Stdout, result.SandboxID)
	})

	t.Run("bubblewrap_worktree_mount_read_write", func(t *testing.T) {
		// TC-062-07: bubblewrap with a real temp dir as Worktree.
		// Seed a file, run a payload that reads it and writes a new file, assert the new file persists on the host.
		tempDir := t.TempDir()

		// Seed a file in the worktree.
		seedFilePath := filepath.Join(tempDir, "seed.txt")
		seedContent := "SEEDED_CONTENT"
		if err := os.WriteFile(seedFilePath, []byte(seedContent), 0o644); err != nil {
			t.Fatalf("failed to seed file: %v", err)
		}

		// Command: read seed file, verify content, write new file.
		// The payload runs with cwd=/work (where the worktree is mounted).
		cmd := []string{"sh", "-c", "cat seed.txt && echo 'WROTE_FILE' > output.txt"}

		req := sandbox.Request{
			Command:  cmd,
			Worktree: tempDir,
			Tier:     "bubblewrap",
			Limits: sandbox.Limits{
				WallClockTimeout: 10 * time.Second,
			},
		}

		result, exitCode, err := runner.Run(req)
		if err != nil {
			t.Fatalf("Run() returned error: %v", err)
		}

		if exitCode != 0 {
			t.Errorf("Expected exit code 0, got %d; stderr: %q", exitCode, result.Stderr)
		}

		if result.Status != "clean" {
			t.Errorf("Expected status 'clean', got %q", result.Status)
		}

		// Verify the payload read the seeded file (output should contain SEEDED_CONTENT).
		if !contains(result.Stdout, seedContent) {
			t.Errorf("Expected stdout to contain seeded content %q, got: %q", seedContent, result.Stdout)
		}

		// Verify the new file was written to the host's worktree (persisted after the sandbox exited).
		outputFilePath := filepath.Join(tempDir, "output.txt")
		outputContent, err := os.ReadFile(outputFilePath)
		if err != nil {
			t.Errorf("Expected output.txt to exist and be readable, but got error: %v", err)
		} else if !contains(string(outputContent), "WROTE_FILE") {
			t.Errorf("Expected output.txt to contain 'WROTE_FILE', got: %q", string(outputContent))
		}

		t.Logf("bubblewrap_worktree_mount_read_write: worktree=%s, seed read successfully, output.txt persisted to host", tempDir)
	})
}

// TestExecSandboxLiveToolchain tests that the toolchain (go) resolves inside the sandbox.
// TC-063-05: FileRead + PATH forwarding makes `go` callable in-box.
// Gate: AGENT_BUILDER_LIVE_EXEC_SANDBOX=1
// Binary path: AGENT_BUILDER_EXEC_SANDBOX_BIN
// Note: Requires exec-sandbox task 004 (FileRead capability) to be merged before this test passes.
func TestExecSandboxLiveToolchain(t *testing.T) {
	// Gate: require explicit env var to run.
	if os.Getenv("AGENT_BUILDER_LIVE_EXEC_SANDBOX") != "1" {
		t.Skip("set AGENT_BUILDER_LIVE_EXEC_SANDBOX=1 for live block run")
	}

	// Read the binary path from env.
	binPath := os.Getenv("AGENT_BUILDER_EXEC_SANDBOX_BIN")
	if binPath == "" {
		t.Skip("AGENT_BUILDER_EXEC_SANDBOX_BIN not set; skipping live test")
	}

	runner := New(binPath)

	// Use a temp directory for the worktree
	tempDir := t.TempDir()

	req := sandbox.Request{
		Command:  []string{"-c", "command -v go && go version"},
		Worktree: tempDir,
		Tier:     "bubblewrap",
		Limits: sandbox.Limits{
			WallClockTimeout: 30 * time.Second,
		},
	}

	result, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Verify exit code is 0 — go must resolve in-box via the adapter's FileRead+PATH forwarding.
	if exitCode != 0 {
		t.Fatalf("Expected exit code 0 (go resolved in-box), got %d; stdout=%q stderr=%q", exitCode, result.Stdout, result.Stderr)
	}

	// Verify sandbox status is clean
	if result.Status != "clean" {
		t.Errorf("Expected status 'clean', got %q", result.Status)
	}

	// Verify stdout contains the go version output (proof that go resolved and ran in-box)
	if !contains(result.Stdout, "go version go") {
		t.Errorf("Expected stdout to contain 'go version go', got: %q", result.Stdout)
	}

	t.Logf("TestExecSandboxLiveToolchain: PASS - go resolved in-box, output=%q", result.Stdout)
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
