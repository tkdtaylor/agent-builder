package orchestrator_test

// Task 168: rehydrate and idempotently resume in-flight runs from RunStore.
// These are blackbox tests over the exported orchestrator API (WithRunStore,
// RehydrateInFlight, ResumeFromRecord) plus a real runstore.FileStore in a temp
// dir. They reuse fakePolicy/fakeReporter from orchestrator_test.go.

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// planReturningPlanner returns a fixed plan, defaulting GoalID/Goal from the goal.
type planReturningPlanner struct{ plan orchestrator.Plan }

func (p planReturningPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	pl := p.plan
	if pl.GoalID == "" {
		pl.GoalID = goal.ID
	}
	if pl.Goal == "" {
		pl.Goal = goal.Spec
	}
	return pl, nil
}

// recordingDispatch records the Task.IDs it was dispatched with and optionally
// fails for a configured set of IDs.
type recordingDispatch struct {
	mu      sync.Mutex
	calls   []string
	failIDs map[string]bool
}

func (d *recordingDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.calls = append(d.calls, sub.Task.ID)
	if d.failIDs[sub.Task.ID] {
		return fmt.Errorf("simulated dispatch failure for %s", sub.Task.ID)
	}
	return nil
}

func (d *recordingDispatch) dispatched(id string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, c := range d.calls {
		if c == id {
			n++
		}
	}
	return n
}

func codingSubGoals(ids ...string) []orchestrator.SubGoal {
	subs := make([]orchestrator.SubGoal, 0, len(ids))
	for _, id := range ids {
		subs = append(subs, orchestrator.SubGoal{
			RecipeName: orchestrator.DefaultRecipeName,
			Task:       supervisor.Task{ID: id, Spec: "do " + id},
		})
	}
	return subs
}

// TC-168-01: plan admission persists a Record (StatusRunning) before dispatch.
func TestTC168_01_PlanAdmissionPersistsRecord(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	plan := orchestrator.Plan{SubGoals: codingSubGoals("g1-a", "g1-b")}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
		// default requireApproval=true → pause, no dispatch → Record stays Running.
	)
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "build the API"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, ok, err := store.Load("g1")
	if err != nil || !ok {
		t.Fatalf("Load(g1) = ok=%v err=%v, want a persisted record", ok, err)
	}
	if rec.Status != runstore.StatusRunning {
		t.Errorf("Status = %q, want %q", rec.Status, runstore.StatusRunning)
	}
	if rec.Goal != "build the API" {
		t.Errorf("Goal = %q, want %q", rec.Goal, "build the API")
	}
	var gotPlan orchestrator.Plan
	if err := json.Unmarshal(rec.Plan, &gotPlan); err != nil {
		t.Fatalf("unmarshal persisted Plan: %v", err)
	}
	if gotPlan.GoalID != "g1" || len(gotPlan.SubGoals) != 2 || gotPlan.SubGoals[0].Task.ID != "g1-a" {
		t.Errorf("persisted Plan = %+v, want the planner's 2-sub-goal plan for g1", gotPlan)
	}
}

// TC-168-02: sub-goal dispatch records attempt state before/after; success.
func TestTC168_02_AttemptStateRecorded(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	plan := orchestrator.Plan{SubGoals: codingSubGoals("g2-a", "g2-b")}
	disp := &recordingDispatch{}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g2", Spec: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, ok, _ := store.Load("g2")
	if !ok {
		t.Fatal("no record for g2")
	}
	if len(rec.Attempts) != 2 {
		t.Fatalf("Attempts len = %d, want 2 (one per sub-goal, before/after update the SAME entry)", len(rec.Attempts))
	}
	byTask := map[string]runstore.AttemptState{}
	for _, a := range rec.Attempts {
		if _, dup := byTask[a.TaskID]; dup {
			t.Fatalf("duplicate attempt entry for %s (before/after must update the same entry)", a.TaskID)
		}
		byTask[a.TaskID] = a
	}
	for _, id := range []string{"g2-a", "g2-b"} {
		a, ok := byTask[id]
		if !ok {
			t.Fatalf("no attempt entry for %s", id)
		}
		if a.Status != runstore.StatusCompleted {
			t.Errorf("%s Status = %q, want completed", id, a.Status)
		}
		if a.Attempt != 1 {
			t.Errorf("%s Attempt = %d, want 1", id, a.Attempt)
		}
	}
}

// TC-168-03: a failed sub-goal dispatch is recorded as failed with detail.
func TestTC168_03_FailedAttemptRecorded(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	plan := orchestrator.Plan{SubGoals: codingSubGoals("g3-a", "g3-b")}
	disp := &recordingDispatch{failIDs: map[string]bool{"g3-b": true}}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g3", Spec: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, _, _ := store.Load("g3")
	byTask := map[string]runstore.AttemptState{}
	for _, a := range rec.Attempts {
		byTask[a.TaskID] = a
	}
	if byTask["g3-b"].Status != runstore.StatusFailed {
		t.Errorf("g3-b Status = %q, want failed", byTask["g3-b"].Status)
	}
	if byTask["g3-b"].Detail == "" {
		t.Error("g3-b Detail is empty, want the failure text")
	}
	if byTask["g3-a"].Status != runstore.StatusCompleted {
		t.Errorf("g3-a Status = %q, want completed (independent per-sub-goal state)", byTask["g3-a"].Status)
	}
}

// TC-168-04: RehydrateInFlight wraps ListInFlight.
func TestTC168_04_RehydrateInFlightWrapsListInFlight(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	_ = store.Save(runstore.Record{GoalID: "r1", Status: runstore.StatusRunning})
	_ = store.Save(runstore.Record{GoalID: "r2", Status: runstore.StatusRunning})
	_ = store.Save(runstore.Record{GoalID: "r3", Status: runstore.StatusCompleted})

	got, err := orchestrator.RehydrateInFlight(store)
	if err != nil {
		t.Fatalf("RehydrateInFlight: %v", err)
	}
	want, _ := store.ListInFlight()
	if len(got) != 2 || len(got) != len(want) {
		t.Fatalf("RehydrateInFlight len = %d, want 2 (== ListInFlight len %d)", len(got), len(want))
	}
	for i := range got {
		if got[i].GoalID != want[i].GoalID {
			t.Errorf("RehydrateInFlight[%d].GoalID = %q, want %q", i, got[i].GoalID, want[i].GoalID)
		}
	}
}

// resumeRecord builds a Record with the given plan sub-goal IDs and attempts.
func resumeRecord(t *testing.T, goalID string, subIDs []string, attempts []runstore.AttemptState) runstore.Record {
	t.Helper()
	planJSON, err := json.Marshal(orchestrator.Plan{
		GoalID: goalID, Goal: goalID, SubGoals: codingSubGoals(subIDs...),
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	return runstore.Record{GoalID: goalID, Goal: goalID, Plan: planJSON, Attempts: attempts, Status: runstore.StatusRunning}
}

// TC-168-05: ResumeFromRecord skips completed sub-goals.
func TestTC168_05_ResumeSkipsCompleted(t *testing.T) {
	rec := resumeRecord(t, "gr", []string{"sub-a", "sub-b", "sub-c"},
		[]runstore.AttemptState{{TaskID: "sub-a", Attempt: 1, Status: runstore.StatusCompleted}})
	disp := &recordingDispatch{}
	o := orchestrator.New(
		planReturningPlanner{},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := o.ResumeFromRecord(context.Background(), rec); err != nil {
		t.Fatalf("ResumeFromRecord: %v", err)
	}
	if disp.dispatched("sub-a") != 0 {
		t.Errorf("sub-a dispatched %d times, want 0 (already completed)", disp.dispatched("sub-a"))
	}
	if disp.dispatched("sub-b") != 1 || disp.dispatched("sub-c") != 1 {
		t.Errorf("sub-b/sub-c dispatch counts = %d/%d, want 1/1", disp.dispatched("sub-b"), disp.dispatched("sub-c"))
	}
	disp.mu.Lock()
	total := len(disp.calls)
	disp.mu.Unlock()
	if total != 2 {
		t.Errorf("total dispatches = %d, want 2 (not 3)", total)
	}
}

// TC-168-06: ResumeFromRecord re-dispatches an interrupted (running) attempt.
func TestTC168_06_ResumeRedispatchesInterrupted(t *testing.T) {
	rec := resumeRecord(t, "gr2", []string{"sub-a", "sub-b", "sub-c"},
		[]runstore.AttemptState{
			{TaskID: "sub-a", Attempt: 1, Status: runstore.StatusCompleted},
			{TaskID: "sub-b", Attempt: 1, Status: runstore.StatusRunning},
		})
	disp := &recordingDispatch{}
	o := orchestrator.New(
		planReturningPlanner{},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := o.ResumeFromRecord(context.Background(), rec); err != nil {
		t.Fatalf("ResumeFromRecord: %v", err)
	}
	if disp.dispatched("sub-a") != 0 {
		t.Errorf("sub-a dispatched %d times, want 0 (completed must never re-run)", disp.dispatched("sub-a"))
	}
	if disp.dispatched("sub-b") != 1 {
		t.Errorf("sub-b dispatched %d times, want 1 (running-and-crashed IS re-dispatched)", disp.dispatched("sub-b"))
	}
	if disp.dispatched("sub-c") != 1 {
		t.Errorf("sub-c dispatched %d times, want 1", disp.dispatched("sub-c"))
	}
}

// TC-168-07: end-to-end simulated-crash-and-resume across two independently
// constructed Orchestrator+FileStore pairs sharing one on-disk directory (L5).
func TestTC168_07_CrashAndResume(t *testing.T) {
	dir := t.TempDir()

	// (1) orch1 completes sub-a, then FAILS sub-b/sub-c (the simulated crash), and
	// durably records sub-a completed before the failures.
	store1, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("store1: %v", err)
	}
	plan := orchestrator.Plan{SubGoals: codingSubGoals("sub-a", "sub-b", "sub-c")}
	disp1 := &recordingDispatch{failIDs: map[string]bool{"sub-b": true, "sub-c": true}}
	orch1 := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store1),
		orchestrator.WithDispatchFunc(disp1.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := orch1.Handle(context.Background(), supervisor.Task{ID: "goalX", Spec: "big goal"}); err != nil {
		t.Fatalf("orch1.Handle: %v", err)
	}

	// (2) A fresh store2 + orch2 on the SAME dir (a process restart). Rehydrate and
	// resume; orch2's dispatch succeeds for everything.
	store2, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	inflight, err := orchestrator.RehydrateInFlight(store2)
	if err != nil {
		t.Fatalf("RehydrateInFlight: %v", err)
	}
	if len(inflight) != 1 || inflight[0].GoalID != "goalX" {
		t.Fatalf("RehydrateInFlight = %+v, want the 1 in-flight goalX record", inflight)
	}
	disp2 := &recordingDispatch{}
	orch2 := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store2),
		orchestrator.WithDispatchFunc(disp2.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := orch2.ResumeFromRecord(context.Background(), inflight[0]); err != nil {
		t.Fatalf("orch2.ResumeFromRecord: %v", err)
	}

	// orch2 re-dispatches only the interrupted remainder, never sub-a.
	if disp2.dispatched("sub-a") != 0 {
		t.Errorf("orch2 re-dispatched sub-a %d times, want 0 (orch1 already completed it)", disp2.dispatched("sub-a"))
	}
	if disp2.dispatched("sub-b") != 1 || disp2.dispatched("sub-c") != 1 {
		t.Errorf("orch2 dispatch counts sub-b/sub-c = %d/%d, want 1/1", disp2.dispatched("sub-b"), disp2.dispatched("sub-c"))
	}

	// After resume the goal is terminal and all 3 sub-goals completed exactly once.
	rec, ok, _ := store2.Load("goalX")
	if !ok {
		t.Fatal("goalX missing from store2 after resume")
	}
	if rec.Status != runstore.StatusCompleted {
		t.Errorf("post-resume Status = %q, want completed", rec.Status)
	}
	completed := map[string]int{}
	for _, a := range rec.Attempts {
		if a.Status == runstore.StatusCompleted {
			completed[a.TaskID]++
		}
	}
	for _, id := range []string{"sub-a", "sub-b", "sub-c"} {
		if completed[id] != 1 {
			t.Errorf("%s completed %d times in the record, want exactly 1 across both instances", id, completed[id])
		}
	}
	// Total dispatches across BOTH instances: orch1 tried all 3, orch2 only the 2
	// remaining. sub-a ran exactly once (orch1); it was never re-dispatched.
	if disp1.dispatched("sub-a") != 1 {
		t.Errorf("orch1 dispatched sub-a %d times, want 1", disp1.dispatched("sub-a"))
	}
}

// TC-168-08: RunStore unset is byte-for-byte unchanged (nil-guard path).
func TestTC168_08_RunStoreUnsetUnchanged(t *testing.T) {
	plan := orchestrator.Plan{SubGoals: codingSubGoals("u-a", "u-b")}
	disp := &recordingDispatch{}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		// No WithRunStore: runStore stays nil, every runstore path is guarded.
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	res, err := o.Handle(context.Background(), supervisor.Task{ID: "u", Spec: "x"})
	if err != nil {
		t.Fatalf("Handle without RunStore: %v", err)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("Outcomes len = %d, want 2 (dispatch unchanged when RunStore unset)", len(res.Outcomes))
	}
	if disp.dispatched("u-a") != 1 || disp.dispatched("u-b") != 1 {
		t.Errorf("dispatch counts = %d/%d, want 1/1 (byte-for-byte unchanged dispatch)", disp.dispatched("u-a"), disp.dispatched("u-b"))
	}
}
