package runtime

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestGateExistenceNilFactory validates TC-078-01:
// A recipe with GateFactory = nil is rejected before supervisor construction;
// error contains "gate" and describes the defect.
func TestGateExistenceNilFactory(t *testing.T) {
	// Directly call verifyGateExists with a nil GateFactory.
	// We expect an error mentioning "gate" and "nil".
	var gateFactory recipe.GateFactory = nil // Explicitly nil — the defect we're testing.

	err := verifyGateExists(gateFactory)
	if err == nil {
		t.Fatalf("verifyGateExists(nil) should return an error, got nil")
	}
	if !containsSubstring(err.Error(), "gate") {
		t.Fatalf("error message should contain 'gate', got: %v", err)
	}
	if !containsSubstring(err.Error(), "nil") {
		t.Fatalf("error message should contain 'nil', got: %v", err)
	}
}

// TestGateExistencePassThroughGate validates TC-078-02:
// A recipe whose GateFactory returns a pass-through gate (always OK, no checks)
// is rejected; error names the gate as invalid.
func TestGateExistencePassThroughGate(t *testing.T) {
	// Create a pass-through gate that always returns OK without running any checks.
	// It does not implement the Blocker interface, so it should be rejected.
	passthroughGate := &noopGate{}

	gateFactory := func() supervisor.Gate { return passthroughGate }

	err := verifyGateExists(gateFactory)
	if err == nil {
		t.Fatalf("verifyGateExists(passthrough gate) should return an error, got nil")
	}
	if !containsSubstring(err.Error(), "gate") {
		t.Fatalf("error message should contain 'gate', got: %v", err)
	}
}

// TestGateExistenceRealGate validates TC-078-03:
// A recipe with a real, blocking gate (the production gate) passes the assertion.
func TestGateExistenceRealGate(t *testing.T) {
	// Use the production gate factory from the coding-agent recipe.
	g := newProductionGateFactory
	err := verifyGateExists(g)
	if err != nil {
		t.Fatalf("verifyGateExists(production gate) should not error, got: %v", err)
	}
}

// TestGateExistenceUnconditional validates TC-078-04:
// The gate-existence assertion fires for every recipe path, not conditional on
// whether the recipe is "generated" or "human-authored".
func TestGateExistenceUnconditional(t *testing.T) {
	// Simulate a malicious generated recipe that has no gate.
	// The runtime assembly-time check must catch this unconditionally.
	var gateFactory recipe.GateFactory = nil // No gate — the assembly-time check must catch this.

	// Verify that the gate-existence check fires regardless of recipe source.
	err := verifyGateExists(gateFactory)
	if err == nil {
		t.Fatalf("verifyGateExists should reject a nil GateFactory unconditionally, got nil")
	}
	if !containsSubstring(err.Error(), "gate") {
		t.Fatalf("error message should cite the gate defect, got: %v", err)
	}
}

// noopGate is a stub gate that always returns OK without running any checks.
// It implements supervisor.Gate but not gate.Blocker, so it should be rejected.
type noopGate struct{}

func (g *noopGate) Verify(repoPath string) gate.Verdict {
	return gate.Verdict{OK: true, Results: []gate.StepResult{}}
}

// containsSubstring checks if text contains substring.
func containsSubstring(text, substring string) bool {
	for i := 0; i <= len(text)-len(substring); i++ {
		if text[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
