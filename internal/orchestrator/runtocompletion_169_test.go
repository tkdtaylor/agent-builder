package orchestrator_test

// Task 169: bounded goal-level re-plan loop (RunToCompletion) with a
// RunStore-persisted attempt budget and escalation on exhaustion.
// Reuses fakePolicy/fakeReporter from orchestrator_test.go and codingSubGoals from
// runstore_168_test.go.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// countingPlanner records how many times it was invoked and the goal text seen
// each call (so a re-plan's folded goal text is inspectable).
type countingPlanner struct {
	mu    sync.Mutex
	plan  orchestrator.Plan
	calls int
	goals []string
}

func (p *countingPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.goals = append(p.goals, goal.Spec)
	pl := p.plan
	pl.GoalID = goal.ID // stable key across re-plans
	pl.Goal = goal.Spec
	return pl, nil
}

func (p *countingPlanner) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *countingPlanner) goalAt(i int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i < 0 || i >= len(p.goals) {
		return ""
	}
	return p.goals[i]
}

// failCountDispatch fails its first failUntil calls (with a fixed detail), then
// succeeds. failUntil = -1 fails every call.
type failCountDispatch struct {
	mu       sync.Mutex
	failUntil int
	detail   string
	calls    int
}

func (d *failCountDispatch) fn(_ context.Context, _ orchestrator.SubGoal, _ runtime.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls++
	if d.failUntil < 0 || d.calls <= d.failUntil {
		return fmt.Errorf("%s", d.detail)
	}
	return nil
}

// capturingReporter records every reported message.
type capturingReporter struct {
	mu   sync.Mutex
	msgs []string
}

func (r *capturingReporter) Report(_ context.Context, s string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, s)
	return nil
}

func (r *capturingReporter) countContaining(sub string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, m := range r.msgs {
		if strings.Contains(m, sub) {
			n++
		}
	}
	return n
}

func newRTCOrchestrator(planner orchestrator.Planner, reporter supervisor.Reporter, disp orchestrator.DispatchFunc, opts ...orchestrator.Option) *orchestrator.Orchestrator {
	base := []orchestrator.Option{
		orchestrator.WithDispatchFunc(disp),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	}
	base = append(base, opts...)
	return orchestrator.New(planner, &fakePolicy{decision: policy.DecisionAllow}, reporter, runtime.Config{}, base...)
}

// TC-169-02: immediate success needs no re-plan (Planner invoked once, no escalation).
func TestTC169_02_ImmediateSuccessNoReplan(t *testing.T) {
	planner := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
	rep := &capturingReporter{}
	disp := &failCountDispatch{failUntil: 0} // always succeed
	o := newRTCOrchestrator(planner, rep, disp.fn)

	res, err := o.RunToCompletion(context.Background(), supervisor.Task{ID: "g", Spec: "do it"}, 3)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if res.HasTerminalFailure() {
		t.Errorf("result has terminal failure, want full success")
	}
	if planner.callCount() != 1 {
		t.Errorf("Planner invoked %d times, want 1", planner.callCount())
	}
	if rep.countContaining("exhausted") != 0 {
		t.Errorf("got %d exhaustion reports, want 0", rep.countContaining("exhausted"))
	}
}

// TC-169-03: a terminal failure folds the detail into the goal and re-plans.
func TestTC169_03_FailureFoldsAndReplans(t *testing.T) {
	planner := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
	rep := &capturingReporter{}
	disp := &failCountDispatch{failUntil: 1, detail: "gate failure: lint"} // fail attempt 1, succeed attempt 2
	o := newRTCOrchestrator(planner, rep, disp.fn)

	res, err := o.RunToCompletion(context.Background(), supervisor.Task{ID: "g", Spec: "build API"}, 3)
	if err != nil {
		t.Fatalf("RunToCompletion: %v", err)
	}
	if planner.callCount() != 2 {
		t.Fatalf("Planner invoked %d times, want 2", planner.callCount())
	}
	// The SECOND invocation's goal text carries the folded failure detail.
	first, second := planner.goalAt(0), planner.goalAt(1)
	if first == second {
		t.Errorf("re-plan goal text unchanged (%q); want folded failure detail added", second)
	}
	if !strings.Contains(second, "gate failure: lint") {
		t.Errorf("re-plan goal text = %q, want it to contain the folded detail 'gate failure: lint'", second)
	}
	if res.HasTerminalFailure() {
		t.Errorf("final result has terminal failure, want success on attempt 2")
	}
}

// TC-169-04: attempt counter survives a crash (persisted via RunStore), L5.
func TestTC169_04_AttemptCounterSurvivesCrash(t *testing.T) {
	dir := t.TempDir()

	// (1) orch1 with a 2-attempt budget, always failing → exhausts, persists Attempt=2.
	store1, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	planner1 := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
	disp1 := &failCountDispatch{failUntil: -1, detail: "always fails"}
	orch1 := newRTCOrchestrator(planner1, &capturingReporter{}, disp1.fn, orchestrator.WithRunStore(store1))

	_, err = orch1.RunToCompletion(context.Background(), supervisor.Task{ID: "gc", Spec: "hard goal"}, 2)
	if !errors.Is(err, orchestrator.ErrGoalAttemptsExhausted) {
		t.Fatalf("orch1 err = %v, want ErrGoalAttemptsExhausted", err)
	}
	if planner1.callCount() != 2 {
		t.Fatalf("orch1 Planner invoked %d times, want 2", planner1.callCount())
	}
	if rec, ok, _ := store1.Load("gc"); !ok || rec.Attempt != 2 {
		t.Fatalf("store1 Record.Attempt = %d (ok=%v), want 2 persisted", rec.Attempt, ok)
	}

	// (2) Fresh store2+orch2 on the SAME dir (a restart), now succeeding. The budget
	// is 3, but 2 attempts are already recorded, so orch2 runs exactly ONE attempt.
	store2, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	planner2 := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
	disp2 := &failCountDispatch{failUntil: 0} // succeed
	orch2 := newRTCOrchestrator(planner2, &capturingReporter{}, disp2.fn, orchestrator.WithRunStore(store2))

	res, err := orch2.RunToCompletion(context.Background(), supervisor.Task{ID: "gc", Spec: "hard goal"}, 3)
	if err != nil {
		t.Fatalf("orch2 RunToCompletion: %v", err)
	}
	if res.HasTerminalFailure() {
		t.Errorf("orch2 result has terminal failure, want success")
	}
	if planner2.callCount() != 1 {
		t.Errorf("orch2 Planner invoked %d times, want 1 (budget resumed from 2, not reset to 3)", planner2.callCount())
	}
	if rec, ok, _ := store2.Load("gc"); !ok || rec.Attempt != 3 {
		t.Errorf("store2 Record.Attempt = %d (ok=%v), want 3 (advanced from 2, not reset)", rec.Attempt, ok)
	}

	// Sub-case: a THIRD process whose single remaining attempt ALSO fails reports
	// exhaustion immediately (0 further attempts left in the budget).
	t.Run("last_remaining_attempt_fails_exhausts", func(t *testing.T) {
		dir3 := t.TempDir()
		storeA, _ := runstore.NewFileStore(dir3)
		plA := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
		orchA := newRTCOrchestrator(plA, &capturingReporter{}, (&failCountDispatch{failUntil: -1, detail: "x"}).fn, orchestrator.WithRunStore(storeA))
		_, _ = orchA.RunToCompletion(context.Background(), supervisor.Task{ID: "g3", Spec: "g"}, 2) // exhaust to 2

		storeB, _ := runstore.NewFileStore(dir3)
		plB := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
		repB := &capturingReporter{}
		orchB := newRTCOrchestrator(plB, repB, (&failCountDispatch{failUntil: -1, detail: "x"}).fn, orchestrator.WithRunStore(storeB))
		_, err := orchB.RunToCompletion(context.Background(), supervisor.Task{ID: "g3", Spec: "g"}, 3)
		if !errors.Is(err, orchestrator.ErrGoalAttemptsExhausted) {
			t.Fatalf("orchB err = %v, want ErrGoalAttemptsExhausted", err)
		}
		if plB.callCount() != 1 {
			t.Errorf("orchB Planner invoked %d times, want 1 (only attempt 3 remained)", plB.callCount())
		}
		if repB.countContaining("exhausted") != 1 {
			t.Errorf("orchB exhaustion reports = %d, want 1", repB.countContaining("exhausted"))
		}
	})
}

// TC-169-05: exhaustion escalates over the Reporter exactly once.
func TestTC169_05_ExhaustionEscalatesOnce(t *testing.T) {
	planner := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
	rep := &capturingReporter{}
	disp := &failCountDispatch{failUntil: -1, detail: "boom"}
	o := newRTCOrchestrator(planner, rep, disp.fn)

	res, err := o.RunToCompletion(context.Background(), supervisor.Task{ID: "gx", Spec: "goal"}, 2)
	if !errors.Is(err, orchestrator.ErrGoalAttemptsExhausted) {
		t.Fatalf("err = %v, want ErrGoalAttemptsExhausted", err)
	}
	_ = res
	if planner.callCount() != 2 {
		t.Errorf("Planner invoked %d times, want exactly 2 (maxAttempts)", planner.callCount())
	}
	if got := rep.countContaining("exhausted"); got != 1 {
		t.Fatalf(`Reporter got %d messages containing "exhausted", want exactly 1`, got)
	}
	// The single escalation names the goal ID and the attempt count.
	rep.mu.Lock()
	defer rep.mu.Unlock()
	found := false
	for _, m := range rep.msgs {
		if strings.Contains(m, "exhausted") {
			if !strings.Contains(m, "gx") || !strings.Contains(m, "2") {
				t.Errorf("escalation message %q must name goal ID 'gx' and attempt count '2'", m)
			}
			found = true
		}
	}
	if !found {
		t.Error("no escalation message found")
	}
}

// TC-169-06: RunStore unset still functions (loop mechanics unaffected).
func TestTC169_06_RunStoreUnsetStillFunctions(t *testing.T) {
	// Re-run the fold-and-replan (TC-169-03) scenario with NO RunStore.
	t.Run("fold_replan_no_runstore", func(t *testing.T) {
		planner := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
		disp := &failCountDispatch{failUntil: 1, detail: "gate failure: lint"}
		o := newRTCOrchestrator(planner, &capturingReporter{}, disp.fn) // no WithRunStore
		if _, err := o.RunToCompletion(context.Background(), supervisor.Task{ID: "g", Spec: "s"}, 3); err != nil {
			t.Fatalf("RunToCompletion: %v", err)
		}
		if planner.callCount() != 2 {
			t.Errorf("Planner invoked %d times, want 2 (identical to the RunStore-configured run)", planner.callCount())
		}
	})
	// Re-run the exhaustion (TC-169-05) scenario with NO RunStore.
	t.Run("exhaustion_no_runstore", func(t *testing.T) {
		planner := &countingPlanner{plan: orchestrator.Plan{SubGoals: codingSubGoals("s1")}}
		rep := &capturingReporter{}
		o := newRTCOrchestrator(planner, rep, (&failCountDispatch{failUntil: -1, detail: "boom"}).fn)
		_, err := o.RunToCompletion(context.Background(), supervisor.Task{ID: "gx", Spec: "goal"}, 2)
		if !errors.Is(err, orchestrator.ErrGoalAttemptsExhausted) {
			t.Fatalf("err = %v, want ErrGoalAttemptsExhausted", err)
		}
		if planner.callCount() != 2 || rep.countContaining("exhausted") != 1 {
			t.Errorf("no-RunStore mechanics differ: calls=%d exhausted-reports=%d, want 2 and 1", planner.callCount(), rep.countContaining("exhausted"))
		}
	})
}
