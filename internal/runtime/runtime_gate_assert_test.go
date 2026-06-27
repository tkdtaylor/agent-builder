package runtime

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestGateExistenceNilFactory validates TC-078-01 (unit level):
// The unit-level gate-existence check rejects a nil GateFactory, and the error
// message contains "gate" and "nil".
func TestGateExistenceNilFactory(t *testing.T) {
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
	// A pass-through gate that always returns OK without running any checks. It
	// implements supervisor.Gate but NOT gate.Blocker, so it must be rejected.
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
	g := newProductionGateFactory
	err := verifyGateExists(g)
	if err != nil {
		t.Fatalf("verifyGateExists(production gate) should not error, got: %v", err)
	}
}

// TestGateExistenceThroughRunPath validates TC-078-04 and the TC-078-01 audit
// sub-assertion via the LIVE assembler path:
//
//   - It registers a recipe with a nil GateFactory and calls the real
//     runtime.Run (not the unit helper). This proves the gate-existence check is
//     wired into Run with no escape hatch — the assertion fires unconditionally
//     for any recipe path, generated or human-authored (TC-078-04).
//   - It sets AuditRecordPath to a path that does not yet exist and asserts the
//     audit file is never created. Run only constructs the audit sink AFTER the
//     gate-existence check; the file's absence is behavioral proof that Run bailed
//     before any dispatch — "the run never starts", no audit events (TC-078-01).
func TestGateExistenceThroughRunPath(t *testing.T) {
	// A recipe with the defect: no gate. This simulates a generated recipe that
	// slipped past compilation. Register it under a unique name.
	recipe.Register("test-nil-gate-run-path", func() (recipe.Recipe, error) {
		return recipe.Recipe{
			GoalSourceFactory: func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) { return nil, nil },
			RoutingSpec:       recipe.RoutingSpec{MinCapability: 1, SensitivityHint: recipe.SensitivityNone},
			GateFactory:       nil, // The defect — this MUST be caught at assembly time.
			ResultSinkFactory: func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) { return nil, nil },
			BlockWiring:       nil,
		}, nil
	})

	// TaskRoot and Worktree must exist (validatePaths runs before recipe select);
	// use real temp dirs so Run reaches the gate-existence check.
	tmp := t.TempDir()
	auditPath := filepath.Join(tmp, "audit.jsonl")

	config := Config{
		TaskRoot:        tmp,
		Worktree:        tmp,
		ClaudeCLI:       "claude",
		RunTimeout:      0,
		MaxAttempts:     1,
		PublishRemote:   "origin",
		GitCLI:          "git",
		GitHubCLI:       "gh",
		RecipeName:      "test-nil-gate-run-path",
		AuditRecordPath: auditPath, // if Run got past the gate check it would build the audit sink here
	}

	err := Run(config, io.Discard)

	// The live Run path must reject the gateless recipe before any dispatch.
	if err == nil {
		t.Fatalf("Run with nil GateFactory should return an error, got nil")
	}
	if !containsSubstring(err.Error(), "gate") {
		t.Fatalf("Run error should cite the gate defect, got: %v", err)
	}

	// No audit events were emitted: Run returned before the audit sink was ever
	// constructed (the run never started), so the audit file does not exist.
	if _, statErr := os.Stat(auditPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected no audit file (run never started), but os.Stat(%q) = %v", auditPath, statErr)
	}
}

// noopGate is a stub gate that always returns OK without running any checks.
// It implements supervisor.Gate but not gate.Blocker, so it must be rejected.
type noopGate struct{}

func (g *noopGate) Verify(repoPath string) gate.Verdict {
	return gate.Verdict{OK: true, Results: []gate.StepResult{}}
}

// containsSubstring reports whether text contains substring.
func containsSubstring(text, substring string) bool {
	for i := 0; i <= len(text)-len(substring); i++ {
		if text[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
