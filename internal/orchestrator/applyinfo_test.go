package orchestrator_test

// Tests for task 115 — apply-info-at-checkpoint, orchestrator-side (ADR 054 §4).
// These exercise the checkpoint-augment primitives the goal actor composes:
//
//   TC-115-02 — queued info is surfaced WITH the approval solicitation
//   TC-115-03 — on approve, ResumeWithFold re-plans the AUGMENTED goal, replaces
//               the stored plan with P1, dispatches P1's sub-goals, drains the queue
//
// The registry pending-info queue semantics (EnqueueInfo/PendingInfo/DrainInfo) and
// the queue-don't-interrupt + amendment-spawn control-loop behaviour are asserted
// CLI-side (applyinfo_test.go in internal/cli) where the live goal actor runs.

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// capturingPlanner records every goal Task it is asked to plan (so a test can assert
// the EXACT text the re-plan was driven with) and returns a distinct plan per call:
// the first call returns plan P0 (one sub-goal "sub-P0"), the second returns P1 (one
// sub-goal "sub-P1"). This lets TC-115-03 prove the dispatch used the RE-PLANNED P1,
// not the originally-stored P0.
type capturingPlanner struct {
	mu       sync.Mutex
	seen     []supervisor.Task
	recipe   string
	subSpecs []string // per-call sub-goal spec: index 0 = first Plan() call, etc.
}

func newCapturingPlanner(recipe string, subSpecs ...string) *capturingPlanner {
	return &capturingPlanner{recipe: recipe, subSpecs: subSpecs}
}

func (p *capturingPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	idx := len(p.seen)
	p.seen = append(p.seen, goal)
	subSpec := "sub"
	if idx < len(p.subSpecs) {
		subSpec = p.subSpecs[idx]
	}
	return orchestrator.Plan{
		Goal:   goal.Spec,
		GoalID: goal.ID,
		SubGoals: []orchestrator.SubGoal{{
			RecipeName: p.recipe,
			Task:       supervisor.Task{ID: goal.ID + "-" + subSpec, Spec: subSpec, Repo: goal.Repo},
		}},
	}, nil
}

func (p *capturingPlanner) calls() []supervisor.Task {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]supervisor.Task, len(p.seen))
	copy(out, p.seen)
	return out
}

// --- TC-115-02 — queued info surfaced with the approval solicitation ---------

func TestTC115_02_QueuedInfoSurfacedWithApprovalSolicitation(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &fakeReporter{}
	// require_approval pauses the goal at AwaitingApproval (plan in store, no dispatch).
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	o := orchestrator.New(
		newNSubGoalPlanner("G", 1, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithStatusRegistry(reg),
	)
	reg.Register("G", orchestrator.StateQueued)

	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "G", Spec: "build the API"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	// The goal is now AwaitingApproval with a plan in the store.
	if st, _ := reg.Get("G"); st.State != orchestrator.StateAwaitingApproval {
		t.Fatalf("state = %v, want AwaitingApproval", st.State)
	}

	// Info arrives while AwaitingApproval → enqueue, then the actor re-solicits.
	reg.EnqueueInfo("G", "must support IPv6")
	if err := o.SolicitApproval(context.Background(), "G"); err != nil {
		t.Fatalf("SolicitApproval: %v", err)
	}

	// The MOST RECENT solicitation reply must include the queued info text as a
	// substring alongside the plan summary (the operator sees the amended context).
	reports := rep.Reported()
	if len(reports) == 0 {
		t.Fatal("no reports emitted")
	}
	last := reports[len(reports)-1]
	if !strings.Contains(last, "must support IPv6") {
		t.Fatalf("re-solicited approval reply does not include the queued info text:\n%s", last)
	}
	if !strings.Contains(last, "Approve?") {
		t.Fatalf("re-solicited reply is not an approval solicitation:\n%s", last)
	}
}

// --- TC-115-03 — on approve, re-plan the augmented goal + replace stored plan --

func TestTC115_03_OnApproveRePlanAugmentedGoalReplacesStoredPlan(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}

	// First Plan() call (during Handle) → P0 with sub-goal "sub-P0".
	// Second Plan() call (during ResumeWithFold) → P1 with sub-goal "sub-P1".
	planner := newCapturingPlanner("coding-agent", "sub-P0", "sub-P1")

	// Capture which sub-goals were actually dispatched.
	var dispMu sync.Mutex
	var dispatched []string
	dispFn := func(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
		dispMu.Lock()
		dispatched = append(dispatched, sub.Task.Spec)
		dispMu.Unlock()
		return nil
	}

	o := orchestrator.New(
		planner, pol, rep, runtime.Config{},
		orchestrator.WithStatusRegistry(reg),
		orchestrator.WithDispatchFunc(dispFn),
	)
	reg.Register("G", orchestrator.StateQueued)

	goal := supervisor.Task{ID: "G", Spec: "build the import pipeline"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !o.HasPendingPlan("G") {
		t.Fatal("expected a pending plan (P0) after require_approval pause")
	}

	// Queue info, then approve via ResumeWithFold (the actor supplies the original goal).
	reg.EnqueueInfo("G", "also validate CSV headers")
	approval := orchestrator.Approval{From: "operator", To: "orchestrator", GoalID: "G", Approved: true}
	if _, err := o.ResumeWithFold(context.Background(), approval, goal); err != nil {
		t.Fatalf("ResumeWithFold: %v", err)
	}

	// 1. planner.Plan was called a SECOND time, with the AUGMENTED goal text containing
	//    BOTH the original spec and the info.
	calls := planner.calls()
	if len(calls) != 2 {
		t.Fatalf("planner.Plan called %d times, want exactly 2 (initial plan + re-plan)", len(calls))
	}
	rePlanText := calls[1].Spec
	if !strings.Contains(rePlanText, "build the import pipeline") {
		t.Fatalf("re-plan goal text missing the original spec:\n%s", rePlanText)
	}
	if !strings.Contains(rePlanText, "also validate CSV headers") {
		t.Fatalf("re-plan goal text missing the folded info:\n%s", rePlanText)
	}

	// 2. The PlanStore was replaced with the re-planned P1 (sub-goal "sub-P1"), and the
	//    dispatch used P1's sub-goals, NOT P0's ("sub-P0").
	dispMu.Lock()
	got := append([]string(nil), dispatched...)
	dispMu.Unlock()
	if len(got) != 1 || got[0] != "sub-P1" {
		t.Fatalf("dispatched sub-goals = %v, want exactly [sub-P1] (the re-planned P1, not P0's sub-P0)", got)
	}

	// 3. The pending-info queue for G is drained (empty) after folding — no double-apply.
	if pi := reg.PendingInfo("G"); len(pi) != 0 {
		t.Fatalf("pending-info queue after fold = %v, want empty (drained)", pi)
	}

	// The store-replacement is proven transitively above: the originally-stored P0 was
	// consumed and the dispatch ran P1's "sub-P1" sub-goal. The terminal registry state
	// is Done (the re-planned plan dispatched successfully).
	if st, _ := reg.Get("G"); st.State != orchestrator.StateDone {
		t.Fatalf("terminal state after fold-dispatch = %v, want Done", st.State)
	}
}
