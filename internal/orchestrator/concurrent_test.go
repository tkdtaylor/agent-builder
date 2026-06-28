package orchestrator_test

// Tests for task 086 — multi-worker concurrent dispatch (ADR 042 / ADR 046 §5 /
// ADR 050). dispatchPlan fans out one goroutine per approved sub-goal; this file
// proves true concurrency (all start before any completes), partial-failure
// isolation (best-effort), ordered success+failure aggregation, race-freedom under
// -race, and single fleet-audit-chain coverage of all N concurrent workers.
//
//   TC-086-01 — all N workers start before any completes (barrier spy)
//   TC-086-02 — one worker failure does not halt the others (best-effort)
//   TC-086-03 — aggregated success+failure mix, delivered via Reporter
//   TC-086-04 — no data races under -race (5 concurrent workers, shared sink)
//   TC-086-05 — single fleet chain covers all N concurrent workers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- test doubles for concurrent dispatch ------------------------------------

// nSubGoalPlanner emits exactly the sub-goals it is constructed with, ignoring the
// goal text. It lets a test pin N sub-goals (all on a real, registered recipe) so
// dispatch concurrency can be exercised without depending on the StructuredPlanner's
// parse rules. The recipe must resolve via recipe.SelectRecipe (coding-agent does,
// registered transitively through internal/runtime).
type nSubGoalPlanner struct {
	subs []orchestrator.SubGoal
}

func newNSubGoalPlanner(goalID string, n int, recipeName string) *nSubGoalPlanner {
	subs := make([]orchestrator.SubGoal, n)
	for i := range subs {
		subs[i] = orchestrator.SubGoal{
			RecipeName: recipeName,
			Task: supervisor.Task{
				ID:   fmt.Sprintf("%s-sub-%d", goalID, i),
				Spec: fmt.Sprintf("sub-goal %d", i),
			},
		}
	}
	return &nSubGoalPlanner{subs: subs}
}

func (p *nSubGoalPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	return orchestrator.Plan{Goal: goal.Spec, GoalID: goal.ID, SubGoals: p.subs}, nil
}

// barrierDispatch is a DispatchFunc that blocks every call on a shared barrier that
// releases only once all N calls have entered. If dispatch were sequential, the
// first call would block forever (siblings never enter to trip the barrier), so a
// passing test (within the timeout) is positive proof of concurrency.
type barrierDispatch struct {
	n       int
	mu      sync.Mutex
	started int
	release chan struct{}
	once    sync.Once
}

func newBarrierDispatch(n int) *barrierDispatch {
	return &barrierDispatch{n: n, release: make(chan struct{})}
}

func (b *barrierDispatch) fn(ctx context.Context, _ orchestrator.SubGoal, _ runtime.Config) error {
	b.mu.Lock()
	b.started++
	if b.started == b.n {
		// All N have entered the barrier → release everyone.
		b.once.Do(func() { close(b.release) })
	}
	b.mu.Unlock()
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *barrierDispatch) startedCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.started
}

// --- TC-086-01 — all N workers start before any completes --------------------

func TestTC086_01_AllWorkersStartBeforeAnyCompletes(t *testing.T) {
	const n = 3
	bar := newBarrierDispatch(n)
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", n, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(bar.fn),
	)

	done := make(chan orchestrator.PlanResult, 1)
	errc := make(chan error, 1)
	go func() {
		res, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "concurrent"})
		if err != nil {
			errc <- err
			return
		}
		done <- res
	}()

	select {
	case err := <-errc:
		t.Fatalf("Handle: unexpected error: %v", err)
	case res := <-done:
		// The barrier only releases when all n entered → all started before any
		// returned. A sequential dispatch would have deadlocked (caught by timeout).
		if bar.startedCount() != n {
			t.Fatalf("want all %d workers started, got %d", n, bar.startedCount())
		}
		if len(res.Outcomes) != n {
			t.Fatalf("want %d outcomes, got %d", n, len(res.Outcomes))
		}
		for i, oc := range res.Outcomes {
			if !oc.Success {
				t.Errorf("outcome %d: want success, got %+v", i, oc)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("Handle did not complete in 5s — dispatch is NOT concurrent (only %d of %d workers entered the barrier)", bar.startedCount(), n)
	}
}

// --- TC-086-02 — one worker failure does not halt the others -----------------

// indexedDispatch records, per sub-goal task ID, whether dispatch ran, and fails the
// sub-goals whose index is in failIdx. It is safe for concurrent use.
type indexedDispatch struct {
	mu      sync.Mutex
	ran     map[string]bool // task ID -> dispatched
	failIdx map[int]string  // sub-goal index (derived from task ID suffix) -> error msg
}

func newIndexedDispatch() *indexedDispatch {
	return &indexedDispatch{ran: map[string]bool{}, failIdx: map[int]string{}}
}

func (d *indexedDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ran[sub.Task.ID] = true
	// Task IDs are "<goal>-sub-<i>"; recover i.
	var idx int
	if _, err := fmt.Sscanf(sub.Task.ID[strings.LastIndex(sub.Task.ID, "-")+1:], "%d", &idx); err == nil {
		if msg, fail := d.failIdx[idx]; fail {
			return errString(msg)
		}
	}
	return nil
}

func (d *indexedDispatch) didRun(taskID string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ran[taskID]
}

func TestTC086_02_OneWorkerFailureDoesNotHaltOthers(t *testing.T) {
	const n = 3
	disp := newIndexedDispatch()
	disp.failIdx[0] = "worker 0 failed immediately" // sub-goal 0 fails
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", n, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
	)

	res, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "partial"})
	if err != nil {
		t.Fatalf("Handle: a single worker failure must not error the plan, got: %v", err)
	}
	if len(res.Outcomes) != n {
		t.Fatalf("want %d outcomes, got %d", n, len(res.Outcomes))
	}
	// Outcome 0 failed, carrying the reason.
	if res.Outcomes[0].Success {
		t.Errorf("outcome 0: want failure, got %+v", res.Outcomes[0])
	}
	if !strings.Contains(res.Outcomes[0].Detail, "worker 0 failed") {
		t.Errorf("outcome 0 detail = %q, want the failure reason", res.Outcomes[0].Detail)
	}
	// Survivors 1 and 2 completed — they were NOT halted by worker 0's failure.
	for i := 1; i < n; i++ {
		if !res.Outcomes[i].Success {
			t.Errorf("survivor outcome %d: want success, got %+v", i, res.Outcomes[i])
		}
		if !disp.didRun(fmt.Sprintf("g1-sub-%d", i)) {
			t.Errorf("survivor worker %d was never dispatched — worker 0's failure halted it", i)
		}
	}
}

// --- TC-086-03 — aggregated success+failure mix via Reporter -----------------

func TestTC086_03_AggregatedMixDeliveredViaReporter(t *testing.T) {
	const n = 3
	disp := newIndexedDispatch()
	disp.failIdx[1] = "middle worker failed" // sub-goal 1 (middle) fails
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", n, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
	)

	res, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "mix"})
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	// Deterministic, sub-goal-ordered aggregation: 0 success, 1 failure, 2 success.
	if len(res.Outcomes) != n {
		t.Fatalf("want %d outcomes, got %d", n, len(res.Outcomes))
	}
	wantSuccess := []bool{true, false, true}
	successes, failures := 0, 0
	for i, oc := range res.Outcomes {
		if oc.Success != wantSuccess[i] {
			t.Errorf("outcome %d success = %v, want %v (%+v)", i, oc.Success, wantSuccess[i], oc)
		}
		if oc.Success {
			successes++
		} else {
			failures++
		}
	}
	if successes != 2 || failures != 1 {
		t.Errorf("aggregate: got %d success / %d failure, want 2 / 1", successes, failures)
	}

	// Exactly one summary delivered via the Reporter, containing both OK and FAIL.
	reported := rep.Reported()
	if len(reported) != 1 {
		t.Fatalf("want exactly 1 summary report, got %d: %v", len(reported), reported)
	}
	summary := reported[0]
	for _, want := range []string{"OK", "FAIL"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q marker: %q", want, summary)
		}
	}
}

// --- TC-086-04 — no data races under -race (5 concurrent workers) -------------

// raceDispatch both records the dispatch AND appends two worker-tier events to the
// shared audit.Sink per worker, so -race exercises concurrent writes to (a) the
// aggregate outcome slice (inside the orchestrator), (b) the shared audit sink, and
// (c) the PlanStore. Run via: go test -race -count=1 ./internal/orchestrator/...
type raceDispatch struct {
	sink audit.Sink
}

func (d *raceDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	_ = d.sink.Append(audit.AuditEvent{Action: audit.ActionContainment, TaskID: sub.Task.ID, RunID: sub.Task.ID, Detail: audit.EventDetail{Launcher: "exec-sandbox"}})
	_ = d.sink.Append(audit.AuditEvent{Action: audit.ActionFinish, TaskID: sub.Task.ID, RunID: sub.Task.ID, Outcome: audit.OutcomeCompleted})
	return nil
}

func TestTC086_04_NoDataRacesUnderConcurrentDispatch(t *testing.T) {
	const n = 5
	sink := audit.NewFakeSink()
	disp := &raceDispatch{sink: sink}
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", n, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithAuditSink(sink),
	)

	res, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "race"})
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if len(res.Outcomes) != n {
		t.Fatalf("want %d outcomes, got %d", n, len(res.Outcomes))
	}
	for i, oc := range res.Outcomes {
		if !oc.Success {
			t.Errorf("outcome %d: want success, got %+v", i, oc)
		}
	}
	// The shared sink recorded every event with none lost/corrupted: 1 goal-intake +
	// 1 plan-decided + n spawn-decided + n containment + n finish + 1 completion.
	events := sink.Events()
	counts := map[audit.AuditAction]int{}
	for _, ev := range events {
		counts[ev.Action]++
	}
	want := map[audit.AuditAction]int{
		audit.ActionGoalIntake:   1,
		audit.ActionPlanDecided:  1,
		audit.ActionSpawnDecided: n,
		audit.ActionContainment:  n,
		audit.ActionFinish:       n,
		audit.ActionCompletion:   1,
	}
	for action, w := range want {
		if counts[action] != w {
			t.Errorf("action %q count = %d, want %d", action, counts[action], w)
		}
	}
}

// --- TC-086-05 — single fleet chain covers all N concurrent workers -----------

func TestTC086_05_FleetChainCoversAllNConcurrentWorkers(t *testing.T) {
	const n = 3
	sink := audit.NewFakeSink()
	disp := &raceDispatch{sink: sink}
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", n, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithAuditSink(sink),
	)

	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "fleet"}); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	events := sink.Events()
	// Per-worker coverage: EACH of the n distinct sub-goal task IDs must appear in the
	// one chain's spawn-decided, containment, AND finish events.
	for i := 0; i < n; i++ {
		taskID := fmt.Sprintf("g1-sub-%d", i)
		sawSpawn, sawContain, sawFinish := false, false, false
		for _, ev := range events {
			if ev.TaskID != taskID {
				continue
			}
			switch ev.Action {
			case audit.ActionSpawnDecided:
				sawSpawn = true
			case audit.ActionContainment:
				sawContain = true
			case audit.ActionFinish:
				sawFinish = true
			}
		}
		if !sawSpawn || !sawContain || !sawFinish {
			t.Errorf("worker %s missing from fleet chain: spawn=%v containment=%v finish=%v",
				taskID, sawSpawn, sawContain, sawFinish)
		}
	}

	// Bookends: goal-intake first, completion last (preserved despite interleaving).
	if len(events) == 0 || events[0].Action != audit.ActionGoalIntake {
		t.Fatalf("first event must be goal-intake, got %v", actionList(events))
	}
	if events[len(events)-1].Action != audit.ActionCompletion {
		t.Errorf("last event must be completion, got %q", events[len(events)-1].Action)
	}
	// goal-intake and completion appear exactly once.
	var intakeCount, completionCount int
	for _, ev := range events {
		switch ev.Action {
		case audit.ActionGoalIntake:
			intakeCount++
		case audit.ActionCompletion:
			completionCount++
		}
	}
	if intakeCount != 1 || completionCount != 1 {
		t.Errorf("bookend counts: goal-intake=%d completion=%d, want 1/1", intakeCount, completionCount)
	}
	t.Logf("TC-086-05 L2: one fleet chain, %d events covering %d concurrent workers", len(events), n)

	// --- L5: replay the recorded chain through the real audit-trail binary ---
	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		t.Log("TC-086-05 L5 binary-deferred: AGENT_BUILDER_AUDIT_BIN unset; FakeSink single-chain coverage asserted at L2")
		return
	}
	logfile := filepath.Join(t.TempDir(), "fleet-086-l5.log")
	block := audit.NewBlockSink(binPath, logfile)
	for _, ev := range events {
		if err := block.Append(ev); err != nil {
			t.Fatalf("BlockSink.Append(%s): %v", ev.Action, err)
		}
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("BlockSink.Seal: %v", err)
	}
	res, err := audit.VerifyChain(binPath, logfile)
	if err != nil {
		t.Fatalf("VerifyChain (fleet chain): %v", err)
	}
	if !res.Valid {
		t.Fatalf("VerifyChain: Valid=false, want true; message=%q", res.Message)
	}
	t.Logf("TC-086-05 L5: audit-trail verify → valid=%v on the concurrent fleet chain", res.Valid)
}
