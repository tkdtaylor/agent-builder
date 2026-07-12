package orchestrator_test

// Task 173: MemoryGuardPlanStore backed by the durable, read-gated DurableStore.
// Reuses mgStubRunner/newMGStubRunner from memoryguard_test.go.

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func mg173Allow(stub *mgStubRunner) {
	stub.setResponse("validate_write", map[string]any{"allow": true, "stored_id": "sid"})
	stub.setResponse("validate_read", map[string]any{"allow": true, "content_redacted": ""})
	stub.setResponse("verify_delete", map[string]any{"confirmed": true, "residue_detected": false})
}

// TC-173-01: constructors updated (dir + error); interface conformance intact.
func TestTC173_01_ConstructorSignatures(t *testing.T) {
	stub := newMGStubRunner()
	mg173Allow(stub)
	s1, err := orchestrator.NewMemoryGuardPlanStore("/stub/mg", "id", t.TempDir())
	if err != nil || s1 == nil {
		t.Fatalf("NewMemoryGuardPlanStore = (%v, %v), want non-nil, nil", s1, err)
	}
	s2, err := orchestrator.NewMemoryGuardPlanStoreWithRunner("/stub/mg", "id", t.TempDir(), stub)
	if err != nil || s2 == nil {
		t.Fatalf("NewMemoryGuardPlanStoreWithRunner = (%v, %v), want non-nil, nil", s2, err)
	}
	// Interface conformance (compile-time).
	var _ orchestrator.PlanStore = s2
	var _ orchestrator.TamperAwarePlanStore = s2
}

// TC-173-02: Get fails closed on a read-gate denial.
func TestTC173_02_GetFailsClosedOnDenial(t *testing.T) {
	stub := newMGStubRunner()
	stub.setResponse("validate_write", map[string]any{"allow": true, "stored_id": "sid"})
	stub.setResponse("validate_read", map[string]any{"allow": false})
	store, _ := orchestrator.NewMemoryGuardPlanStoreWithRunner("/stub/mg", "id", t.TempDir(), stub)

	if err := store.TryPut(orchestrator.Plan{GoalID: "g1", Goal: "secret"}); err != nil {
		t.Fatalf("TryPut: %v", err)
	}
	plan, ok := store.Get("g1")
	if ok {
		t.Fatalf("Get on denied read = (%+v, true), want (Plan{}, false) — a denied read must never leak the plan", plan)
	}
	if plan.GoalID != "" || plan.Goal != "" {
		t.Errorf("Get returned a non-zero plan %+v on denial", plan)
	}
}

// TC-173-03: Get returns the plan on allow.
func TestTC173_03_GetReturnsPlanOnAllow(t *testing.T) {
	stub := newMGStubRunner()
	mg173Allow(stub)
	store, _ := orchestrator.NewMemoryGuardPlanStoreWithRunner("/stub/mg", "id", t.TempDir(), stub)
	if err := store.TryPut(orchestrator.Plan{GoalID: "g1", Goal: "do X"}); err != nil {
		t.Fatalf("TryPut: %v", err)
	}
	plan, ok := store.Get("g1")
	if !ok || plan.GoalID != "g1" || plan.Goal != "do X" {
		t.Fatalf("Get(g1) = (%+v, %v), want ({g1, do X}, true)", plan, ok)
	}
}

// TC-173-05: cross-restart durability for a stored plan (L5).
func TestTC173_05_CrossRestartDurability(t *testing.T) {
	dir := t.TempDir()
	stub := newMGStubRunner()
	mg173Allow(stub)

	store1, err := orchestrator.NewMemoryGuardPlanStoreWithRunner("/stub/mg", "id", dir, stub)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	want := orchestrator.Plan{GoalID: "g1", Goal: "big goal", SubGoals: []orchestrator.SubGoal{
		{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g1-0", Spec: "do work"}},
	}}
	if err := store1.TryPut(want); err != nil {
		t.Fatalf("store1.TryPut: %v", err)
	}

	store2, err := orchestrator.NewMemoryGuardPlanStoreWithRunner("/stub/mg", "id", dir, stub)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	got, ok := store2.Get("g1")
	if !ok {
		t.Fatal("store2.Get(g1) not found — plan must survive reconstruction")
	}
	if got.GoalID != want.GoalID || got.Goal != want.Goal || len(got.SubGoals) != 1 || got.SubGoals[0].Task.ID != "g1-0" {
		t.Fatalf("store2.Get(g1) = %+v, want %+v (field-for-field durable)", got, want)
	}
}

// TC-173-06: NewPlanStoreFromEnv unaffected when memory-guard is unset.
func TestTC173_06_FromEnvUnsetUnchanged(t *testing.T) {
	t.Setenv(orchestrator.EnvVarMemoryGuardBin, "")
	warned := false
	store, err := orchestrator.NewPlanStoreFromEnv(func(string, ...any) { warned = true })
	if err != nil {
		t.Fatalf("NewPlanStoreFromEnv (unset) err = %v, want nil", err)
	}
	if _, ok := store.(*orchestrator.MemoryPlanStore); !ok {
		t.Fatalf("store type = %T, want *MemoryPlanStore when unset", store)
	}
	if !warned {
		t.Error("expected a degraded-mode warning when memory-guard bin is unset")
	}
}
