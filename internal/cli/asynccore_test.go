package cli

// Tests for task 112 — async control-loop core, CLI side (ADR 054 §1/§3). These
// exercise runControlLoop, which owns intake: non-blocking intake (a goal in
// Dispatching does not stall the next), actor-per-goal concurrency (M goals reach
// Dispatching at once), and the goal-admission cap (excess goals park Queued). The
// fleet-wide worker semaphore and registry-projection cases live in
// internal/orchestrator (asynccore_test.go there).
//
//   TC-112-01 — control loop is non-blocking: status read returns while a goal runs
//   TC-112-02 — actor-per-goal: two goals reach Dispatching concurrently
//   TC-112-04 — goal-admission cap parks excess goals as Queued

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// perGoalPlanner emits exactly one sub-goal per incoming goal, keyed off the goal's
// own ID (so a single shared orchestrator can plan goal-A and goal-B distinctly).
// It also counts how many times Plan was called per goalID, so a test can assert
// exactly one Handle per goal (TC-112-02).
type perGoalPlanner struct {
	mu    sync.Mutex
	calls map[string]int
}

func newPerGoalPlanner() *perGoalPlanner { return &perGoalPlanner{calls: map[string]int{}} }

func (p *perGoalPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	p.mu.Lock()
	p.calls[goal.ID]++
	p.mu.Unlock()
	return orchestrator.Plan{
		Goal:   goal.Spec,
		GoalID: goal.ID,
		SubGoals: []orchestrator.SubGoal{{
			RecipeName: "coding-agent",
			Task:       supervisor.Task{ID: goal.ID + "-sub-0", Spec: "sub"},
		}},
	}, nil
}

func (p *perGoalPlanner) planCount(goalID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[goalID]
}

// latchDispatch blocks each dispatch on a per-goal latch the test controls, and
// signals (once per goal) when the dispatch has entered. The goalID is recovered
// from the sub-goal task ID suffix ("<goalID>-sub-0"). It is the instrument for
// observing a goal held in Dispatching.
type latchDispatch struct {
	mu      sync.Mutex
	entered map[string]chan struct{} // goalID -> closed when its dispatch enters
	release map[string]chan struct{} // goalID -> closed by the test to release it
}

func newLatchDispatch(goalIDs ...string) *latchDispatch {
	d := &latchDispatch{
		entered: map[string]chan struct{}{},
		release: map[string]chan struct{}{},
	}
	for _, g := range goalIDs {
		d.entered[g] = make(chan struct{})
		d.release[g] = make(chan struct{})
	}
	return d
}

func goalIDFromSub(taskID string) string {
	// "<goalID>-sub-0" → "<goalID>"
	const suffix = "-sub-0"
	if len(taskID) > len(suffix) && taskID[len(taskID)-len(suffix):] == suffix {
		return taskID[:len(taskID)-len(suffix)]
	}
	return taskID
}

func (d *latchDispatch) fn(ctx context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	gid := goalIDFromSub(sub.Task.ID)
	d.mu.Lock()
	enter := d.entered[gid]
	rel := d.release[gid]
	d.mu.Unlock()
	if enter != nil {
		close(enter)
	}
	if rel == nil {
		return nil
	}
	select {
	case <-rel:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *latchDispatch) releaseGoal(gid string) {
	d.mu.Lock()
	rel := d.release[gid]
	d.mu.Unlock()
	if rel != nil {
		close(rel)
	}
}

// waitState polls the registry until the goal reaches the wanted state (or any
// later non-Queued state when atLeast is true) or the deadline elapses.
func waitState(t *testing.T, reg *orchestrator.StatusRegistry, goalID string, want orchestrator.GoalState, timeout time.Duration) orchestrator.GoalState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last orchestrator.GoalState
	for time.Now().Before(deadline) {
		st, ok := reg.Get(goalID)
		if ok {
			last = st.State
			if st.State == want {
				return st.State
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return last
}

func waitPastQueued(t *testing.T, reg *orchestrator.StatusRegistry, goalID string, timeout time.Duration) orchestrator.GoalState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last orchestrator.GoalState
	for time.Now().Before(deadline) {
		st, ok := reg.Get(goalID)
		if ok {
			last = st.State
			if st.State != orchestrator.StateQueued {
				return st.State
			}
		}
		time.Sleep(2 * time.Millisecond)
	}
	return last
}

// assembleAsync builds an orchestrateConfig wired for an async-core test: shared
// registry, pinned bounds, a per-goal planner, the latch/spy dispatch, and a stub
// source yielding the given goals.
func assembleAsync(t *testing.T, planner orchestrator.Planner, dispatch orchestrator.DispatchFunc, reg *orchestrator.StatusRegistry, maxWorkers, maxGoals int, goals ...supervisor.Task) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient: &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:     dispatch,
		auditSink:    audit.NewFakeSink(),
		planner:      planner,
		source:       &stubGoalSource{goals: goals},
		signingKey:   testSigningKey(t),
		registry:     reg,
		maxWorkers:   maxWorkers,
		maxGoals:     maxGoals,
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)
	return oc
}

// --- TC-112-01 — control loop is non-blocking --------------------------------

func TestTC112_01_ControlLoopNonBlocking(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	disp := newLatchDispatch("goal-A", "goal-B")
	oc := assembleAsync(t, newPerGoalPlanner(), disp.fn, reg, 4, 8,
		supervisor.Task{ID: "goal-A", Spec: "A"},
		supervisor.Task{ID: "goal-B", Spec: "B"},
	)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// goal-A's dispatch enters and blocks (held in Dispatching).
	select {
	case <-disp.entered["goal-A"]:
	case <-time.After(3 * time.Second):
		t.Fatal("goal-A dispatch never entered")
	}

	// A registry read for goal-A returns WITHOUT blocking while its dispatch is held.
	stA, ok := reg.Get("goal-A")
	if !ok {
		t.Fatal("registry has no entry for goal-A")
	}
	if stA.State != orchestrator.StateDispatching {
		t.Fatalf("goal-A state = %v, want Dispatching (read must not block on the held dispatch)", stA.State)
	}

	// goal-B advances PAST Queued while goal-A is still blocked — intake is not
	// serialized behind goal-A's processing (REQ-112-01).
	bState := waitPastQueued(t, reg, "goal-B", 3*time.Second)
	if bState == orchestrator.StateQueued {
		t.Fatalf("goal-B stayed Queued while goal-A blocked — intake is serialized behind processing")
	}

	// Release both and drain; both reach a terminal state.
	disp.releaseGoal("goal-A")
	disp.releaseGoal("goal-B")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
	for _, gid := range []string{"goal-A", "goal-B"} {
		st, _ := reg.Get(gid)
		if !st.State.IsTerminal() {
			t.Errorf("%s did not reach a terminal state: %v", gid, st.State)
		}
	}
}

// --- TC-112-02 — actor-per-goal: two goals Dispatching concurrently ----------

func TestTC112_02_TwoGoalsDispatchingConcurrently(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	planner := newPerGoalPlanner()
	disp := newLatchDispatch("goal-A", "goal-B")
	oc := assembleAsync(t, planner, disp.fn, reg, 4, 8,
		supervisor.Task{ID: "goal-A", Spec: "A"},
		supervisor.Task{ID: "goal-B", Spec: "B"},
	)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Bounded-wait for BOTH dispatches to enter before releasing either — genuine
	// overlap, not sequential (REQ-112-02).
	for _, gid := range []string{"goal-A", "goal-B"} {
		select {
		case <-disp.entered[gid]:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s dispatch never entered — goals are not running concurrently", gid)
		}
	}
	// Both must be observed Dispatching at the same time.
	for _, gid := range []string{"goal-A", "goal-B"} {
		st := waitState(t, reg, gid, orchestrator.StateDispatching, time.Second)
		if st != orchestrator.StateDispatching {
			t.Fatalf("%s state = %v, want Dispatching (both held simultaneously)", gid, st)
		}
	}

	disp.releaseGoal("goal-A")
	disp.releaseGoal("goal-B")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}

	// Exactly one Handle (→ one Plan) per goalID.
	for _, gid := range []string{"goal-A", "goal-B"} {
		if n := planner.planCount(gid); n != 1 {
			t.Errorf("planner called %d times for %s, want exactly 1 (one Handle per goal)", n, gid)
		}
		st, _ := reg.Get(gid)
		if st.State != orchestrator.StateDone {
			t.Errorf("%s terminal state = %v, want Done", gid, st.State)
		}
	}
}

// --- TC-112-04 — goal-admission cap parks excess goals as Queued -------------

func TestTC112_04_GoalAdmissionCapParksExcess(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	disp := newLatchDispatch("goal-A", "goal-B")
	// MAX_GOALS=1: only one actor may be non-Queued/non-terminal at a time. The
	// admission order between goal-A and goal-B is NOT deterministic (two actor
	// goroutines race for the one slot); this test is written agnostic to which goal
	// wins — it identifies the admitted vs parked goal at runtime.
	oc := assembleAsync(t, newPerGoalPlanner(), disp.fn, reg, 4, 1,
		supervisor.Task{ID: "goal-A", Spec: "A"},
		supervisor.Task{ID: "goal-B", Spec: "B"},
	)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Exactly one goal occupies the single admission slot and is held in Dispatching.
	var admitted, parked string
	select {
	case <-disp.entered["goal-A"]:
		admitted, parked = "goal-A", "goal-B"
	case <-disp.entered["goal-B"]:
		admitted, parked = "goal-B", "goal-A"
	case <-time.After(3 * time.Second):
		t.Fatal("neither goal entered dispatch — no actor was admitted")
	}

	// While the admitted goal holds the slot, the parked goal must be Queued
	// (registered but not advanced to Planning).
	parkedRegistered := false
	for i := 0; i < 500; i++ {
		if st, ok := reg.Get(parked); ok {
			parkedRegistered = true
			if st.State != orchestrator.StateQueued {
				t.Fatalf("%s state = %v while %s holds the only slot, want Queued", parked, st.State, admitted)
			}
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !parkedRegistered {
		t.Fatalf("%s was never registered in the registry", parked)
	}

	// No more than MAX_GOALS=1 actors in a non-Queued, non-terminal state.
	if n := reg.LiveNonQueuedCount(); n > 1 {
		t.Fatalf("live non-queued actors = %d, want <= 1 (MAX_GOALS=1)", n)
	}

	// Release the admitted goal → its slot frees → the parked goal advances out of
	// Queued and ultimately to Done.
	disp.releaseGoal(admitted)
	pState := waitPastQueued(t, reg, parked, 3*time.Second)
	if pState == orchestrator.StateQueued {
		t.Fatalf("%s stayed Queued after %s freed the slot, last state = %v", parked, admitted, pState)
	}
	select {
	case <-disp.entered[parked]:
	case <-time.After(3 * time.Second):
		t.Fatalf("%s dispatch never entered after the slot freed", parked)
	}
	disp.releaseGoal(parked)
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
	for _, gid := range []string{"goal-A", "goal-B"} {
		st, _ := reg.Get(gid)
		if st.State != orchestrator.StateDone {
			t.Errorf("%s terminal state = %v, want Done", gid, st.State)
		}
	}
}
