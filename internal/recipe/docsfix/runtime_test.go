// Package docsfix_test provides runtime-integration tests that check the recipe
// against the actual runtime assembler (task 078 gate-existence assertion).
package docsfix_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/runtime"
)

// TestTC079_04_RuntimeGateAssertionPassesForDocsFix tests TC-079-04:
// The runtime gate-existence assertion (task 078) passes when recipe="docs-fix".
//
// This test calls the REAL runtime.Run with recipe="docs-fix" and verifies that
// the gate-existence assertion does NOT reject the docs-fix gate. The gate-existence
// assertion checks that the gate implements gate.Blocker and returns true from Blocks().
// If those checks pass, runtime.Run proceeds PAST the assertion (though it will fail
// later for unrelated reasons like missing sandbox/Claude in the test environment).
func TestTC079_04_RuntimeGateAssertionPassesForDocsFix(t *testing.T) {
	tmpDir := t.TempDir()

	// Construct a minimal runtime.Config for recipe="docs-fix".
	config := runtime.Config{
		TaskRoot:        tmpDir,
		Worktree:        tmpDir,
		ClaudeCLI:       "claude",
		ExecBoxLauncher: "containment/execution-box/run.sh",
		RunTimeout:      1 * time.Second,
		MaxAttempts:     1,
		PublishRemote:   "origin",
		GitCLI:          "git",
		GitHubCLI:       "gh",
		RecipeName:      "docs-fix",
	}

	// Call the real runtime.Run with the docs-fix recipe.
	// The gate-existence assertion will fire before any sandbox creation.
	// If the docs-fix gate does NOT implement gate.Blocker or returns false
	// from Blocks(), the error will contain one of these substrings:
	//   - "GateFactory"
	//   - "Blocker"
	//   - "Blocks()"
	// If the gate-existence assertion PASSES, runtime proceeds to later failures
	// (missing sandbox, missing task, etc.) — which is fine for this test.
	err := runtime.Run(context.Background(), config, io.Discard)

	// The error SHOULD occur (missing sandbox/Claude), but it must NOT be a gate-existence error.
	if err != nil {
		errMsg := err.Error()
		// These substrings would indicate a gate-existence assertion failure.
		forbiddenSubstrings := []string{"GateFactory", "Blocker", "Blocks()"}
		for _, substring := range forbiddenSubstrings {
			if containsSubstring(errMsg, substring) {
				t.Fatalf("runtime.Run returned a gate-existence error: %v", err)
			}
		}
		// Other errors are expected and fine (missing sandbox, missing task, etc.).
		// The important thing is we got PAST the gate-existence assertion.
		return
	}

	// If we get here with no error, that's also acceptable (though unlikely in test env).
}

// containsSubstring checks if a string contains a substring (case-sensitive).
func containsSubstring(s, substr string) bool {
	// Simple substring check
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
