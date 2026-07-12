package orchestrator_test

// Task 171: ResumeApproval (resume/abort a paused sub-goal) and
// SweepApprovalTimeouts (once-only timeout escalation). Reuses codingSubGoals
// (runstore_168_test.go), recordingDispatch (runstore_168_test.go), fakePolicy
// (orchestrator_test.go), and capturingReporter (runtocompletion_169_test.go).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// seedApprovalRecord stores a Record for goalID with the given plan sub-goals, a
// single pending approval for pendingTaskID, and the other sub-goals pre-completed.
func seedApprovalRecord(t *testing.T, store *runstore.FileStore, goalID, pendingTaskID string, subIDs ...string) {
	t.Helper()
	planJSON, err := json.Marshal(orchestrator.Plan{
		GoalID: goalID, Goal: goalID, SubGoals: codingSubGoals(subIDs...),
	})
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	var attempts []runstore.AttemptState
	for _, id := range subIDs {
		if id == pendingTaskID {
			continue
		}
		attempts = append(attempts, runstore.AttemptState{TaskID: id, Attempt: 1, Status: runstore.StatusCompleted})
	}
	rec := runstore.Record{
		GoalID:   goalID,
		Goal:     goalID,
		Plan:     planJSON,
		Attempts: attempts,
		Pending:  []runstore.PendingApproval{{TaskID: pendingTaskID, Reason: "policy: requires human approval", RequestedAt: time.Now().UTC()}},
		Status:   runstore.StatusAwaitingApproval,
	}
	if err := store.Save(rec); err != nil {
		t.Fatalf("seed Save: %v", err)
	}
}

func newApprovalOrchestrator(store *runstore.FileStore, disp orchestrator.DispatchFunc, rep supervisor.Reporter) *orchestrator.Orchestrator {
	return orchestrator.New(
		planReturningPlanner{},
		&fakePolicy{decision: policy.DecisionAllow},
		rep,
		runtime.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc(disp),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
}

// TC-171-05: ResumeApproval(approved=true) re-dispatches the paused sub-goal only.
func TestTC171_05_ResumeApprovalApproveRedispatches(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	seedApprovalRecord(t, store, "goal-7", "task-3", "task-3", "task-other")

	disp := &recordingDispatch{}
	o := newApprovalOrchestrator(store, disp.fn, &capturingReporter{})
	if err := o.ResumeApproval(context.Background(), "goal-7", "task-3", true); err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}

	if disp.dispatched("task-3") != 1 {
		t.Errorf("task-3 dispatched %d times, want 1", disp.dispatched("task-3"))
	}
	if disp.dispatched("task-other") != 0 {
		t.Errorf("task-other dispatched %d times, want 0 (already completed)", disp.dispatched("task-other"))
	}
	rec, _, _ := store.Load("goal-7")
	for _, p := range rec.Pending {
		if p.TaskID == "task-3" {
			t.Errorf("task-3 still pending after approve")
		}
	}
	if rec.Status != runstore.StatusCompleted {
		t.Errorf("Status = %q, want completed (all sub-goals now complete)", rec.Status)
	}
}

// TC-171-06: ResumeApproval(approved=false) marks the sub-goal needs-human and
// finalizes the plan (this was the last pending item).
func TestTC171_06_ResumeApprovalDenyAbortsAndFinalizes(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	seedApprovalRecord(t, store, "goal-7", "task-3", "task-3", "task-other")

	disp := &recordingDispatch{}
	rep := &capturingReporter{}
	o := newApprovalOrchestrator(store, disp.fn, rep)
	if err := o.ResumeApproval(context.Background(), "goal-7", "task-3", false); err != nil {
		t.Fatalf("ResumeApproval: %v", err)
	}

	if disp.dispatched("task-3") != 0 {
		t.Errorf("task-3 dispatched %d times, want 0 (denied, not re-dispatched)", disp.dispatched("task-3"))
	}
	rec, _, _ := store.Load("goal-7")
	var task3 runstore.AttemptState
	for _, a := range rec.Attempts {
		if a.TaskID == "task-3" {
			task3 = a
		}
	}
	if task3.Status != runstore.StatusNeedsHuman {
		t.Errorf("task-3 attempt Status = %q, want needs-human", task3.Status)
	}
	if rec.Status != runstore.StatusFailed {
		t.Errorf("Status = %q, want failed (terminal, plan finalized)", rec.Status)
	}
	if len(rec.Pending) != 0 {
		t.Errorf("Pending = %+v, want empty", rec.Pending)
	}
	if rep.countContaining("resolved") == 0 {
		t.Errorf("no finalization report sent; msgs=%v", rep.msgs)
	}
}

// TC-171-07: timeout auto-escalates exactly once (idempotent across sweeps).
func TestTC171_07_TimeoutEscalatesOnce(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	// A pending approval requested 2h ago.
	planJSON, _ := json.Marshal(orchestrator.Plan{GoalID: "goal-7", SubGoals: codingSubGoals("task-3")})
	now := time.Now().UTC()
	_ = store.Save(runstore.Record{
		GoalID:  "goal-7",
		Plan:    planJSON,
		Pending: []runstore.PendingApproval{{TaskID: "task-3", Reason: "x", RequestedAt: now.Add(-2 * time.Hour)}},
		Status:  runstore.StatusAwaitingApproval,
	})

	rep := &capturingReporter{}
	o := newApprovalOrchestrator(store, (&recordingDispatch{}).fn, rep)

	o.SweepApprovalTimeouts(context.Background(), now, time.Hour)
	if got := rep.countContaining("timeout"); got != 1 {
		t.Fatalf("first sweep escalations = %d, want 1", got)
	}
	// The single escalation names the goal, task, and elapsed wait.
	rep.mu.Lock()
	msg := rep.msgs[0]
	rep.mu.Unlock()
	if !strings.Contains(msg, "goal-7") || !strings.Contains(msg, "task-3") {
		t.Errorf("escalation %q must name goal-7 and task-3", msg)
	}

	// A second sweep does NOT re-escalate (Escalated flag persisted).
	o.SweepApprovalTimeouts(context.Background(), now.Add(time.Minute), time.Hour)
	if got := rep.countContaining("timeout"); got != 1 {
		t.Fatalf("after second sweep escalations = %d, want still 1 (idempotent)", got)
	}
	// A not-yet-timed-out pending is untouched.
	rec, _, _ := store.Load("goal-7")
	if len(rec.Pending) != 1 || !rec.Pending[0].Escalated {
		t.Errorf("pending Escalated flag not persisted: %+v", rec.Pending)
	}
}
