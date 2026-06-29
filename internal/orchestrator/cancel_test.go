package orchestrator_test

// Unit tests for the task-116 orchestrator-core cancellation primitives (ADR 054 §5):
// the per-goal CancelFunc registry slot (SetCancelFunc/Cancel) and the
// ConsumePlanOnCancel plan-consume (same delete path Resume uses).

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TC-116-03 (registry isolation) — Cancel fires ONLY the addressed goal's CancelFunc;
// a sibling's CancelFunc is untouched (no blast radius). A second Cancel of the same
// goal is a no-op (the handle was consumed).
func TestTC116_03_RegistryCancelIsPerGoalAndOnce(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()

	_, gCancel := context.WithCancel(context.Background())
	hCtx, hCancel := context.WithCancel(context.Background())
	gFired := false
	reg.SetCancelFunc("G", func() { gFired = true; gCancel() })
	reg.SetCancelFunc("H", hCancel)

	if !reg.Cancel("G") {
		t.Fatal("Cancel(G) returned false, want true (a CancelFunc was registered)")
	}
	if !gFired {
		t.Fatal("Cancel(G) did not fire G's CancelFunc")
	}
	// H is untouched — its ctx is still live.
	if hCtx.Err() != nil {
		t.Fatal("Cancel(G) cancelled H's context — there must be no blast radius")
	}
	// A second Cancel(G) is a no-op (handle consumed).
	if reg.Cancel("G") {
		t.Fatal("second Cancel(G) returned true, want false (the handle was consumed)")
	}
	// An unknown goal returns false.
	if reg.Cancel("nope") {
		t.Fatal("Cancel(unknown) returned true, want false")
	}
}

// TC-116-03 (plan consume) — ConsumePlanOnCancel removes the plan under the same
// delete path Resume uses, so after a cancel the store holds no plan and Resume finds
// nothing to dispatch.
func TestTC116_03_ConsumePlanOnCancelRemovesPlan(t *testing.T) {
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	store := orchestrator.NewMemoryPlanStore()
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithPlanStore(store),
	)

	// Pause a plan in the store (require_approval).
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "G", Spec: "coding-agent: build X"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !o.HasPendingPlan("G") {
		t.Fatal("plan for G not held after require_approval")
	}

	had, err := o.ConsumePlanOnCancel("G")
	if err != nil {
		t.Fatalf("ConsumePlanOnCancel: %v", err)
	}
	if !had {
		t.Fatal("ConsumePlanOnCancel(G) reported no plan, want had=true")
	}
	if o.HasPendingPlan("G") {
		t.Fatal("plan for G still present after ConsumePlanOnCancel — a late approval could resurrect it")
	}
	// A Resume after consume finds nothing to dispatch.
	if _, err := o.Resume(context.Background(), orchestrator.Approval{From: "operator", To: "orchestrator", GoalID: "G", Approved: true}); err == nil {
		t.Fatal("Resume(G) after consume succeeded, want a 'no pending plan' error")
	}
	// Consuming a goal with no plan reports had=false (idempotent).
	if had, _ := o.ConsumePlanOnCancel("G"); had {
		t.Fatal("second ConsumePlanOnCancel(G) reported had=true, want false")
	}
}
