// +build !short

package execsandbox

import (
	"os"
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
