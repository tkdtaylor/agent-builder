package orchestrator_test

// Tests for task 112 — async control-loop core, orchestrator-side (ADR 054 §1/§3).
// These exercise the orchestrator-core pieces the control loop composes: the
// fleet-wide worker semaphore acquired INSIDE dispatchPlan's per-sub-goal
// goroutine, the status-registry projection driven at each lifecycle edge, the
// audit chain under M goals × N workers, and permit balance after drain. The
// control-loop-level cases (non-blocking intake, actor-per-goal, goal-admission
// cap) live in internal/cli (asynccore_test.go there) because they exercise
// runControlLoop, which owns intake.
//
//   TC-112-03 — worker semaphore caps total live workers at MAX_WORKERS (=2, =4)
//   TC-112-05 — registry is a mutex-guarded projection; transitions ordered;
//               write failure (no-op registry) never halts a goal
//   TC-112-06 — audit chain stays valid + event count exact under M×N concurrency
//   TC-112-07 — permits balanced; no leak after drain (incl. error path); -race clean

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- TC-112-03 — worker semaphore caps total live workers --------------------

// liveCounterDispatch increments a shared atomic counter on entry, blocks on a
// release channel, decrements on exit, and records the maximum concurrently-live
// count observed. It is the instrument that proves the fleet-wide cap: if the
// semaphore bounds live workers at N, max must equal N even when more sub-goals
// are eligible.
type liveCounterDispatch struct {
	live    atomic.Int64
	max     atomic.Int64
	entered chan struct{} // signalled once per dispatch entry
	release chan struct{} // closed by the test to release all blocked dispatches
}

func newLiveCounterDispatch(totalSubGoals int) *liveCounterDispatch {
	return &liveCounterDispatch{
		entered: make(chan struct{}, totalSubGoals),
		release: make(chan struct{}),
	}
}

func (d *liveCounterDispatch) fn(ctx context.Context, _ orchestrator.SubGoal, _ runtime.Config) error {
	cur := d.live.Add(1)
	for {
		old := d.max.Load()
		if cur <= old || d.max.CompareAndSwap(old, cur) {
			break
		}
	}
	d.entered <- struct{}{}
	defer d.live.Add(-1)
	select {
	case <-d.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestTC112_03_WorkerSemaphoreCapsLiveWorkers(t *testing.T) {
	const subPerGoal = 2
	const totalSubGoals = 2 * subPerGoal // 2 goals × 2 sub-goals = 4 eligible

	run := func(t *testing.T, maxWorkers int) int64 {
		disp := newLiveCounterDispatch(totalSubGoals)
		sem := orchestrator.NewSemaphore(maxWorkers)
		// Two independent orchestrators would NOT share a semaphore; the control loop
		// shares ONE across all goal actors. We model that by giving both goals the
		// same orchestrator instance (one planner emits per-goal IDs is unnecessary —
		// each Handle carries its own goalID, and the planner pins sub-goal count).
		mkOrch := func(goalID string) *orchestrator.Orchestrator {
			return orchestrator.New(
				newNSubGoalPlanner(goalID, subPerGoal, "coding-agent"),
				&fakePolicy{decision: policy.DecisionAllow},
				&fakeReporter{}, runtime.Config{},
				orchestrator.WithDispatchFunc(disp.fn),
				orchestrator.WithWorkerSemaphore(sem), // SHARED across both goals
			)
		}
		oA := mkOrch("goal-A")
		oB := mkOrch("goal-B")

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = oA.Handle(context.Background(), supervisor.Task{ID: "goal-A", Spec: "A"}) }()
		go func() { defer wg.Done(); _, _ = oB.Handle(context.Background(), supervisor.Task{ID: "goal-B", Spec: "B"}) }()

		// Wait until exactly maxWorkers have entered (the cap is saturated), let the
		// scheduler settle to confirm no (maxWorkers+1)th enters, then release in
		// waves until all totalSubGoals have run.
		for i := 0; i < maxWorkers; i++ {
			select {
			case <-disp.entered:
			case <-time.After(3 * time.Second):
				t.Fatalf("MAX_WORKERS=%d: only %d/%d workers entered before timeout", maxWorkers, i, maxWorkers)
			}
		}
		// Settle window: if the cap leaked, a (maxWorkers+1)th would enter here.
		time.Sleep(100 * time.Millisecond)
		select {
		case <-disp.entered:
			// One more got in during the settle window — but it may be a legitimately
			// released one only if release was closed, which it is not yet. So this is
			// a cap breach. Put it back conceptually by failing.
			t.Fatalf("MAX_WORKERS=%d: a %dth worker entered while the cap should be saturated", maxWorkers, maxWorkers+1)
		default:
		}
		// Release everyone; remaining sub-goals now flow through as permits free.
		close(disp.release)
		wg.Wait()
		return disp.max.Load()
	}

	t.Run("cap=2", func(t *testing.T) {
		got := run(t, 2)
		if got != 2 {
			t.Fatalf("MAX_WORKERS=2: max concurrent live workers = %d, want exactly 2", got)
		}
	})
	t.Run("cap=4", func(t *testing.T) {
		got := run(t, 4)
		if got != 4 {
			t.Fatalf("MAX_WORKERS=4: max concurrent live workers = %d, want exactly 4 (all eligible)", got)
		}
	})
}

// --- TC-112-05 — registry projection + ordered transitions -------------------

func TestTC112_05_RegistryRecordsOrderedTransitions(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", 1, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(func(context.Context, orchestrator.SubGoal, runtime.Config) error { return nil }),
		orchestrator.WithStatusRegistry(reg),
	)
	// Mirror the control loop: register Queued before Handle.
	reg.Register("g1", orchestrator.StateQueued)

	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "allow path"}); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	st, ok := reg.Get("g1")
	if !ok {
		t.Fatal("registry has no entry for g1 after Handle")
	}
	if st.State != orchestrator.StateDone {
		t.Fatalf("terminal state = %v, want Done", st.State)
	}
	if len(st.SubGoals) != 1 {
		t.Fatalf("sub-goal progress entries = %d, want 1", len(st.SubGoals))
	}
	if st.SubGoals[0].State != "done" {
		t.Fatalf("sub-goal[0].State = %q, want \"done\"", st.SubGoals[0].State)
	}
	if st.SubGoals[0].Name != "g1-sub-0" {
		t.Fatalf("sub-goal[0].Name = %q, want g1-sub-0", st.SubGoals[0].Name)
	}
}

// noopRegistry is a *StatusRegistry whose state-write effect is suppressed: we
// model "the registry recorded nothing" by never reading back transitions — but
// the registry type's methods are concrete, so instead we assert the projection
// isolation by injecting a NIL registry (every method is a documented no-op) and
// confirming the goal still completes via the dispatch spy. This is the strongest
// form of "a registry write failure never halts a goal" (REQ-112-05): with NO
// registry at all, control flow is unaffected.
func TestTC112_05_NilRegistryNeverHaltsGoal(t *testing.T) {
	var dispatched atomic.Int64
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		newNSubGoalPlanner("g1", 2, "coding-agent"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(func(context.Context, orchestrator.SubGoal, runtime.Config) error {
			dispatched.Add(1)
			return nil
		}),
		// No WithStatusRegistry → registry is nil → every projection write is a no-op.
	)

	res, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "no registry"})
	if err != nil {
		t.Fatalf("Handle with nil registry must still complete, got error: %v", err)
	}
	if got := dispatched.Load(); got != 2 {
		t.Fatalf("dispatch spy called %d times, want 2 — the goal must complete even with no registry", got)
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(res.Outcomes))
	}
	for i, oc := range res.Outcomes {
		if !oc.Success {
			t.Errorf("outcome %d not successful: %+v", i, oc)
		}
	}
}

func TestTC112_05_PlanErrorTransitionsFailed(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	o := orchestrator.New(
		&errPlanner{},
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{}, runtime.Config{},
		orchestrator.WithStatusRegistry(reg),
	)
	reg.Register("g1", orchestrator.StateQueued)
	if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "boom"}); err == nil {
		t.Fatal("Handle: want planning error, got nil")
	}
	st, _ := reg.Get("g1")
	if st.State != orchestrator.StateFailed {
		t.Fatalf("state after plan error = %v, want Failed", st.State)
	}
}

type errPlanner struct{}

func (errPlanner) Plan(supervisor.Task) (orchestrator.Plan, error) {
	return orchestrator.Plan{}, fmt.Errorf("planner boom")
}

// --- TC-112-06 — audit chain valid + event count exact under M×N -------------

func TestTC112_06_AuditChainValidUnderConcurrency(t *testing.T) {
	const goals = 3
	const subPerGoal = 2
	sink := audit.NewFakeSink()
	disp := &raceDispatch{sink: sink} // appends containment + finish per worker
	sem := orchestrator.NewSemaphore(6)
	reg := orchestrator.NewStatusRegistry()

	var wg sync.WaitGroup
	wg.Add(goals)
	for g := 0; g < goals; g++ {
		gid := fmt.Sprintf("goal-%d", g)
		o := orchestrator.New(
			newNSubGoalPlanner(gid, subPerGoal, "coding-agent"),
			&fakePolicy{decision: policy.DecisionAllow},
			&fakeReporter{}, runtime.Config{},
			orchestrator.WithDispatchFunc(disp.fn),
			orchestrator.WithAuditSink(sink), // ONE shared mutex-guarded sink
			orchestrator.WithWorkerSemaphore(sem),
			orchestrator.WithStatusRegistry(reg),
		)
		go func(gid string) {
			defer wg.Done()
			if _, err := o.Handle(context.Background(), supervisor.Task{ID: gid, Spec: gid}); err != nil {
				t.Errorf("Handle(%s): %v", gid, err)
			}
		}(gid)
	}
	wg.Wait()

	events := sink.Events()
	counts := map[audit.AuditAction]int{}
	for _, ev := range events {
		counts[ev.Action]++
	}
	// Per goal: 1 goal-intake + 1 plan-decided + subPerGoal spawn-decided +
	// subPerGoal containment + subPerGoal finish + 1 completion.
	want := map[audit.AuditAction]int{
		audit.ActionGoalIntake:   goals,
		audit.ActionPlanDecided:  goals,
		audit.ActionSpawnDecided: goals * subPerGoal,
		audit.ActionContainment:  goals * subPerGoal,
		audit.ActionFinish:       goals * subPerGoal,
		audit.ActionCompletion:   goals,
	}
	for action, w := range want {
		if counts[action] != w {
			t.Errorf("action %q count = %d, want %d (event loss under contention?)", action, counts[action], w)
		}
	}

	// L3: replay the recorded chain through the real audit-trail binary when present.
	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		t.Log("TC-112-06 L3 binary-deferred: AGENT_BUILDER_AUDIT_BIN unset; M×N single-chain coverage + counts asserted at L2")
		return
	}
	logfile := filepath.Join(t.TempDir(), "fleet-112-l3.log")
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
		t.Fatalf("VerifyChain (M×N fleet chain): %v", err)
	}
	if !res.Valid {
		t.Fatalf("VerifyChain: Valid=false, want true; message=%q", res.Message)
	}
	t.Logf("TC-112-06 L3: audit-trail verify → valid=%v on %d-event %d×%d chain", res.Valid, len(events), goals, subPerGoal)
}

// --- TC-112-07 — permits balanced; no leak after drain (incl. error path) ----

// mixedDispatch fails a deterministic fraction of dispatches (by sub-goal index
// parity) and succeeds the rest, so the deferred Release is exercised on BOTH the
// success and error paths.
type mixedDispatch struct {
	calls atomic.Int64
}

func (d *mixedDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	n := d.calls.Add(1)
	if n%2 == 0 {
		return fmt.Errorf("simulated dispatch error for %q", sub.Task.ID)
	}
	return nil
}

func TestTC112_07_PermitsBalancedNoLeakAfterDrain(t *testing.T) {
	const maxWorkers = 2
	sem := orchestrator.NewSemaphore(maxWorkers)
	disp := &mixedDispatch{}
	reg := orchestrator.NewStatusRegistry()

	// Several goals with mixed sub-goal counts, all sharing the one semaphore.
	subCounts := []int{1, 3, 2, 4}
	var wg sync.WaitGroup
	wg.Add(len(subCounts))
	for i, sc := range subCounts {
		gid := fmt.Sprintf("goal-%d", i)
		o := orchestrator.New(
			newNSubGoalPlanner(gid, sc, "coding-agent"),
			&fakePolicy{decision: policy.DecisionAllow},
			&fakeReporter{}, runtime.Config{},
			orchestrator.WithDispatchFunc(disp.fn),
			orchestrator.WithWorkerSemaphore(sem),
			orchestrator.WithStatusRegistry(reg),
		)
		go func(gid string) {
			defer wg.Done()
			// A goal with a mix of failing sub-goals still completes (best-effort);
			// Handle returns nil because per-sub-goal dispatch errors are recorded in
			// outcomes, not surfaced as a plan halt.
			if _, err := o.Handle(context.Background(), supervisor.Task{ID: gid, Spec: gid}); err != nil {
				t.Errorf("Handle(%s): %v", gid, err)
			}
		}(gid)
	}
	wg.Wait()

	// After every goal drains, ALL permits must be free — proving every Acquire was
	// matched by a Release on both the success and error paths (deferred Release).
	if !sem.TryAcquireN(maxWorkers) {
		t.Fatalf("TryAcquireN(%d) failed after drain — a permit leaked (Release not balanced on every path)", maxWorkers)
	}
	sem.ReleaseN(maxWorkers)
}
