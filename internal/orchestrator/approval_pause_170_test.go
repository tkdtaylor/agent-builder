package orchestrator_test

// Task 170: sub-goal require_approval pause recorded in RunStore. The fake
// DispatchFunc simulates what runtime.Run does by calling the runtime.Config's
// OnRequireApproval hook it is handed. Reuses planReturningPlanner/codingSubGoals
// (runstore_168_test.go) and fakePolicy/fakeReporter (orchestrator_test.go).

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// approvalHookDispatch fires the supplied OnRequireApproval hook for the sub-goals
// whose IDs are in requireApproval; other sub-goals succeed. It records every
// dispatched sub-goal ID.
type approvalHookDispatch struct {
	mu              sync.Mutex
	dispatched      []string
	requireApproval map[string]bool
	reason          string
}

func (d *approvalHookDispatch) fn(_ context.Context, sub orchestrator.SubGoal, cfg runtime.Config) error {
	d.mu.Lock()
	d.dispatched = append(d.dispatched, sub.Task.ID)
	d.mu.Unlock()
	if d.requireApproval[sub.Task.ID] && cfg.OnRequireApproval != nil {
		cfg.OnRequireApproval(sub.Task, d.reason)
	}
	return nil
}

// TC-170-03: dispatchOne persists a PendingApproval and marks the goal awaiting.
func TestTC170_03_DispatchPersistsPendingApproval(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	plan := orchestrator.Plan{SubGoals: codingSubGoals("sg-1")}
	disp := &approvalHookDispatch{requireApproval: map[string]bool{"sg-1": true}, reason: "policy: requires human approval"}
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
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	rec, ok, _ := store.Load("g")
	if !ok {
		t.Fatal("no record for g")
	}
	if len(rec.Pending) != 1 {
		t.Fatalf("Pending len = %d, want 1; %+v", len(rec.Pending), rec.Pending)
	}
	if rec.Pending[0].TaskID != "sg-1" {
		t.Errorf("Pending[0].TaskID = %q, want %q", rec.Pending[0].TaskID, "sg-1")
	}
	if rec.Pending[0].Reason != "policy: requires human approval" {
		t.Errorf("Pending[0].Reason = %q, want the halt reason", rec.Pending[0].Reason)
	}
	if rec.Status != runstore.StatusAwaitingApproval {
		t.Errorf("Status = %q, want %q", rec.Status, runstore.StatusAwaitingApproval)
	}
}

// TC-170-04: RequestedAt is set within the Handle window.
func TestTC170_04_RequestedAtSet(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	plan := orchestrator.Plan{SubGoals: codingSubGoals("sg-1")}
	disp := &approvalHookDispatch{requireApproval: map[string]bool{"sg-1": true}, reason: "policy: requires human approval"}
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
	before := time.Now().UTC()
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	after := time.Now().UTC()

	rec, _, _ := store.Load("g")
	if len(rec.Pending) != 1 {
		t.Fatalf("Pending len = %d, want 1", len(rec.Pending))
	}
	got := rec.Pending[0].RequestedAt
	if got.Before(before) || got.After(after) {
		t.Errorf("RequestedAt = %v, want within [%v, %v]", got, before, after)
	}
}

// pauseHaltDispatch fires the approval hook on the FIRST dispatched sub-goal (any
// of them) and records any sub-goal dispatched AFTER the pause was recorded (which
// must be none, since the pause halts further not-yet-started dispatch).
type pauseHaltDispatch struct {
	mu             sync.Mutex
	calls          int
	afterPause     []string
	reason         string
}

func (d *pauseHaltDispatch) fn(_ context.Context, sub orchestrator.SubGoal, cfg runtime.Config) error {
	d.mu.Lock()
	d.calls++
	first := d.calls == 1
	if !first {
		d.afterPause = append(d.afterPause, sub.Task.ID)
	}
	d.mu.Unlock()
	if first && cfg.OnRequireApproval != nil {
		cfg.OnRequireApproval(sub.Task, d.reason)
	}
	return nil
}

// TC-170-05: a recorded pause halts further NOT-YET-STARTED dispatch. Determinism
// via a single-worker semaphore (the approval-recording dispatch completes and
// releases before the next sub-goal acquires and checks the pause).
func TestTC170_05_PauseHaltsFurtherDispatch(t *testing.T) {
	dir := t.TempDir()
	store, _ := runstore.NewFileStore(dir)
	plan := orchestrator.Plan{SubGoals: codingSubGoals("sg-1", "sg-2", "sg-3")}
	disp := &pauseHaltDispatch{reason: "policy: requires human approval"}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithWorkerSemaphore(orchestrator.NewSemaphore(1)),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	res, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}

	if disp.calls != 1 {
		t.Fatalf("dispatch called %d times, want exactly 1 (pause halts sub-goals 2 and 3)", disp.calls)
	}
	if len(disp.afterPause) != 0 {
		t.Fatalf("sub-goals dispatched after pause = %v, want none", disp.afterPause)
	}
	// The goal record reflects awaiting-approval, not a silent success/failure.
	rec, _, _ := store.Load("g")
	if rec.Status != runstore.StatusAwaitingApproval {
		t.Errorf("Status = %q, want %q", rec.Status, runstore.StatusAwaitingApproval)
	}
	// The PlanResult reflects the pause: no sub-goal reports success, at least one
	// names approval.
	sawApproval := false
	for _, oc := range res.Outcomes {
		if oc.Success {
			t.Errorf("outcome %+v reports success, want none on a paused plan", oc)
		}
		if strings.Contains(oc.Detail, "approval") {
			sawApproval = true
		}
	}
	if !sawApproval {
		t.Errorf("no PlanResult outcome mentions approval; outcomes=%+v", res.Outcomes)
	}
}

// TC-170-06 (orchestrator side): RunStore unset, the hook is never supplied.
func TestTC170_06_RunStoreUnsetNoHook(t *testing.T) {
	plan := orchestrator.Plan{SubGoals: codingSubGoals("sg-1")}
	hookSeen := false
	disp := func(_ context.Context, _ orchestrator.SubGoal, cfg runtime.Config) error {
		if cfg.OnRequireApproval != nil {
			hookSeen = true
		}
		return nil
	}
	o := orchestrator.New(
		planReturningPlanner{plan: plan},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{},
		runtime.Config{},
		// No WithRunStore.
		orchestrator.WithDispatchFunc(disp),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if hookSeen {
		t.Error("dispatch received a non-nil OnRequireApproval hook, but RunStore is unset (must stay nil)")
	}
}
