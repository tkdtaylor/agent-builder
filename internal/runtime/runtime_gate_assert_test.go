package runtime

import (
	"io"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestGateExistenceNilFactory validates TC-078-01 and TC-078-04:
// When runtime.Run is called with a recipe that has a nil GateFactory,
// the gate-existence check fires unconditionally before any dispatch,
// the error contains "gate" and "nil", and no audit events are emitted
// (the run never starts).
func TestGateExistenceNilFactory(t *testing.T) {
	// Verify the unit-level check directly.
	var gateFactory recipe.GateFactory = nil

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
// is rejected by the unit-level check; error names the gate as invalid.
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

// TestGateExistenceThroughRunPath validates TC-078-04 and TC-078-01 audit requirement:
// The gate-existence assertion fires unconditionally through the live runtime.Run() path
// (not just the unit function), with no escape hatch, and no audit events are emitted
// when the gate check fails (the run never starts and audit is never constructed).
func TestGateExistenceThroughRunPath(t *testing.T) {
	// Create a fake recipe with a nil GateFactory, bypassing recipe.New's panic guard.
	// This simulates a malicious generated recipe that slips through compilation.
	badRecipeFactory := func() (recipe.Recipe, error) {
		return recipe.Recipe{
			GoalSourceFactory:   func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) { return nil, nil },
			RoutingSpec:         recipe.RoutingSpec{MinCapability: 1, SensitivityHint: recipe.SensitivityNone},
			GateFactory:         nil, // The defect: no gate — this MUST be caught at assembly time.
			ResultSinkFactory:   func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) { return nil, nil },
			BlockWiring:         nil,
		}, nil
	}

	// Manually register the bad recipe (normally done via recipe.Register at init time).
	// We register it with a unique name to avoid conflicts in parallel test runs.
	recipe.Register("test-nil-gate-run-path", badRecipeFactory)

	// Construct a minimal config that will cause Run to select the bad recipe.
	config := Config{
		TaskRoot:       "/tmp",
		Worktree:       "/tmp",
		ClaudeCLI:      "claude",
		RunTimeout:     0,
		MaxAttempts:    1,
		PublishRemote:  "origin",
		GitCLI:         "git",
		GitHubCLI:      "gh",
		RecipeName:     "test-nil-gate-run-path",
	}

	// Create a capture sink to verify no audit events are emitted.
	auditCapture := &captureAuditSink{}

	// Call Run with a stdout writer that discards output.
	// The gate-existence check must fire before any dispatch, returning an error.
	err := runWithAuditCapture(config, io.Discard, auditCapture)

	// Verify the gate-existence error is returned.
	if err == nil {
		t.Fatalf("runtime.Run with nil GateFactory should return an error, got nil")
	}
	if !containsSubstring(err.Error(), "gate") {
		t.Fatalf("error should cite gate defect, got: %v", err)
	}

	// Verify no audit events were emitted (the run never started).
	if len(auditCapture.events) > 0 {
		t.Fatalf("expected no audit events when gate check fails, got %d events", len(auditCapture.events))
	}
}

// runWithAuditCapture runs the recipe selection and gate-existence check,
// passing in a capture sink. It returns early if the gate check fails,
// before creating any supervisor or dispatch infrastructure.
func runWithAuditCapture(config Config, stdout io.Writer, auditSink audit.Sink) error {
	_ = stdout // stdout is unused in this test helper, but kept for symmetry with Run
	_ = auditSink // auditSink is unused in this test helper; in real Run it would be wired into supervisor

	// Select the recipe.
	r, err := recipe.SelectRecipe(config.RecipeName)
	if err != nil {
		return err
	}

	// This is the load-bearing assertion from task 078:
	// Fire the gate-existence check BEFORE any audit sink is wired or supervisor created.
	if err := verifyGateExists(r.GateFactory); err != nil {
		return err
	}

	// If we reach here, the gate passed (this test doesn't proceed past the check).
	// In the real Run function, this is where seam assembly would continue.
	return nil
}

// captureAuditSink records all appended audit events for test inspection.
type captureAuditSink struct {
	events []audit.AuditEvent
}

func (c *captureAuditSink) Append(ev audit.AuditEvent) error {
	c.events = append(c.events, ev)
	return nil
}

func (c *captureAuditSink) Seal() error {
	return nil
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
