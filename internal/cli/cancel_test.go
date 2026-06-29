package cli

// Tests for task 116 — cancellation + worker teardown, CLI control-plane side (ADR
// 054 §5). A `cancel <goalID>` fires the goal's per-goal CancelFunc (registered by
// routeNewGoal), which propagates through the goal actor → Handle → dispatchPlan →
// runtime.Run → Supervisor.Run to the run-loop's case <-ctx.Done(): arm — tearing
// down ONLY that goal's in-flight workers (siblings' contexts are independent). The
// cancel handler then sets Cancelled, consumes the plan from the PlanStore under the
// same delete path Resume uses, and the fleet-wide worker permit is released on the
// dispatch return path (no leak).
//
//   TC-116-03 — cancel G (G,H concurrent) → G's worker torn down (ctx cancelled), H
//               untouched; Get("G").State == Cancelled; PlanStore.Get("G") empty so a
//               later Resume(G) does not dispatch.
//   TC-116-04 — MAX_WORKERS=1, cancel G holding the permit → H acquires + dispatches;
//               Acquire(MAX_WORKERS) succeeds after the cancel drain (no permit leak).
//   TC-116-06 — concurrent cancel + Resume-approve → dispatch spy for G called at most
//               once total (no double-dispatch); -race clean on consume + transition.

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

// cancelSpyDispatch is a latch dispatch that records, per goalID, whether its worker
// was torn down by ctx cancellation (the dispatch returned ctx.Err()) versus released
// normally. It is the instrument that proves a running worker was actually killed by
// cancel (observed, not inferred) and that a sibling worker was untouched.
type cancelSpyDispatch struct {
	mu          sync.Mutex
	entered     map[string]chan struct{} // goalID -> closed when its dispatch enters
	release     map[string]chan struct{} // goalID -> closed by the test to release normally
	dispatched  map[string]int           // goalID -> number of dispatch calls
	cancelledBy map[string]bool          // goalID -> dispatch returned because ctx was cancelled
}

func newCancelSpyDispatch(latchedGoalIDs ...string) *cancelSpyDispatch {
	d := &cancelSpyDispatch{
		entered:     map[string]chan struct{}{},
		release:     map[string]chan struct{}{},
		dispatched:  map[string]int{},
		cancelledBy: map[string]bool{},
	}
	for _, g := range latchedGoalIDs {
		d.entered[g] = make(chan struct{})
		d.release[g] = make(chan struct{})
	}
	return d
}

func (d *cancelSpyDispatch) fn(ctx context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	gid := goalIDFromSub(sub.Task.ID)
	d.mu.Lock()
	d.dispatched[gid]++
	enter := d.entered[gid]
	rel := d.release[gid]
	d.mu.Unlock()
	if enter != nil {
		// Close once (a latched goal dispatches one sub-goal in these tests).
		select {
		case <-enter:
		default:
			close(enter)
		}
	}
	if rel == nil {
		return nil
	}
	select {
	case <-rel:
		return nil
	case <-ctx.Done():
		d.mu.Lock()
		d.cancelledBy[gid] = true
		d.mu.Unlock()
		return ctx.Err()
	}
}

func (d *cancelSpyDispatch) dispatchCount(gid string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.dispatched[gid]
}

func (d *cancelSpyDispatch) wasCancelled(gid string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.cancelledBy[gid]
}

func (d *cancelSpyDispatch) releaseGoal(gid string) {
	d.mu.Lock()
	rel := d.release[gid]
	d.mu.Unlock()
	if rel != nil {
		close(rel)
	}
}

// assembleCancel builds an orchestrateConfig wired for a cancel test: a scripted/gated
// message source, a shared registry, an injected mailbox map, the cancel-spy dispatch,
// allow policy, and (optionally) a shared worker semaphore for the permit-leak case.
func assembleCancel(t *testing.T, src supervisor.MessageSource, planner orchestrator.Planner, dispatch orchestrator.DispatchFunc, reg *orchestrator.StatusRegistry, mboxes *commandMailboxes, sem *orchestrator.Semaphore, maxWorkers int) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:      dispatch,
		auditSink:     audit.NewFakeSink(),
		planner:       planner,
		messageSource: src,
		signingKey:    testSigningKey(t),
		registry:      reg,
		workerSem:     sem,
		maxWorkers:    maxWorkers,
		maxGoals:      8,
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	oc.mailboxes = mboxes
	t.Cleanup(cleanup)
	return oc
}

// --- TC-116-03 — cancel tears down only G's worker; H untouched; plan consumed -----

func TestTC116_03_CancelTearsDownOnlyGsWorkerAndConsumesPlan(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	// Both G and H have a worker held at a latch (running). A gated source lets us
	// observe both running BEFORE delivering cancel G.
	disp := newCancelSpyDispatch("G", "H")

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "build G"}},
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "H", Goal: supervisor.Task{ID: "H", Spec: "build H"}},
		supervisor.Message{Kind: supervisor.MsgCancel, GoalID: "G"},
	)
	oc := assembleCancel(t, src, newPerGoalPlanner(), disp.fn, reg, mboxes, nil, 4)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Both workers reach dispatch and are held.
	disp.releaseAllowEntry(t, "G")
	src.release(1) // deliver new-goal H
	disp.releaseAllowEntry(t, "H")

	// Deliver cancel G.
	src.release(2)

	// G's worker must be torn down by ctx cancellation (observed: its dispatch returned
	// ctx.Err()), and G must reach Cancelled.
	if st := waitState(t, reg, "G", orchestrator.StateCancelled, 3*time.Second); st != orchestrator.StateCancelled {
		t.Fatalf("TC-116-03: G state = %v, want Cancelled", st)
	}
	if !waitCancelled(t, disp, "G", 3*time.Second) {
		t.Fatal("TC-116-03: G's worker was NOT torn down by ctx cancellation (dispatch never observed ctx.Done())")
	}

	// PlanStore for G is empty — a later Resume(G) finds nothing to dispatch.
	if oc.orch.HasPendingPlan("G") {
		t.Fatal("TC-116-03: G's plan still in the PlanStore after cancel — a late approval could resurrect it")
	}
	gDispatchBefore := disp.dispatchCount("G")
	if _, err := oc.orch.Resume(context.Background(), orchestrator.Approval{From: "operator", To: "orchestrator", GoalID: "G", Approved: true}); err == nil {
		t.Fatal("TC-116-03: Resume(G) succeeded after cancel — want a 'no pending plan' error (plan was consumed)")
	}
	if got := disp.dispatchCount("G"); got != gDispatchBefore {
		t.Fatalf("TC-116-03: Resume(G) after cancel dispatched G again (count %d→%d) — a consumed plan must not re-dispatch", gDispatchBefore, got)
	}

	// H is UNTOUCHED — no blast radius: its worker is still held (not cancelled), and
	// H's state is NOT Cancelled.
	if disp.wasCancelled("H") {
		t.Fatal("TC-116-03: H's worker was cancelled — cancelling G must not touch H (blast radius)")
	}
	if st, _ := reg.Get("H"); st.State == orchestrator.StateCancelled {
		t.Fatal("TC-116-03: H reached Cancelled — cancelling G must not cancel H")
	}

	// Release H so the loop can drain.
	disp.releaseGoal("H")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("TC-116-03: control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TC-116-03: control loop did not drain")
	}
}

// --- TC-116-04 — permit released on the cancel/teardown path (no leak) ----------

func TestTC116_04_PermitReleasedOnCancelNoLeak(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	// MAX_WORKERS=1: a SHARED semaphore the test holds a reference to so it can assert
	// the permit count after the cancel drains.
	sem := orchestrator.NewSemaphore(1)
	// G holds the single permit (latched worker). H needs the permit; it must acquire it
	// only AFTER G's cancel releases it.
	disp := newCancelSpyDispatch("G", "H")

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "build G"}},
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "H", Goal: supervisor.Task{ID: "H", Spec: "build H"}},
		supervisor.Message{Kind: supervisor.MsgCancel, GoalID: "G"},
	)
	oc := assembleCancel(t, src, newPerGoalPlanner(), disp.fn, reg, mboxes, sem, 1)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// G acquires the single permit and is held at the latch.
	disp.releaseAllowEntry(t, "G")
	// Deliver new-goal H; with MAX_WORKERS=1, H's worker PARKS on Acquire (no permit
	// free), so H does NOT enter dispatch while G holds the permit.
	src.release(1)
	// Confirm H is parked: its dispatch must NOT have entered yet.
	time.Sleep(50 * time.Millisecond)
	if disp.dispatchCount("H") != 0 {
		t.Fatalf("TC-116-04: H dispatched while G held the only permit (count=%d) — the semaphore did not bound the fleet", disp.dispatchCount("H"))
	}

	// Cancel G → G's worker is torn down (ctx cancelled), its deferred permit Release
	// fires. H's parked Acquire now succeeds and H dispatches.
	src.release(2)

	if !disp.releaseEntryWithin(t, "H", 3*time.Second) {
		t.Fatal("TC-116-04: H's worker never acquired the permit + dispatched after cancel G — the permit LEAKED on the cancel path")
	}
	if !waitCancelled(t, disp, "G", 3*time.Second) {
		t.Fatal("TC-116-04: G's worker was not torn down by ctx cancellation")
	}

	// Release H so the loop drains and every permit returns.
	disp.releaseGoal("H")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("TC-116-04: control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("TC-116-04: control loop did not drain")
	}

	// Direct accounting: after the cancel+drain, ALL MAX_WORKERS permits are free —
	// Acquire(MAX_WORKERS) succeeds. A leaked permit would make this fail.
	if !sem.TryAcquireN(1) {
		t.Fatal("TC-116-04: Acquire(MAX_WORKERS) failed after cancel drain — a permit was LEAKED on the cancel/teardown path")
	}
	sem.ReleaseN(1)
}

// --- TC-116-06 — cancel racing Resume-approve → no double-dispatch --------------

func TestTC116_06_CancelRacingResumeApproveNoDoubleDispatch(t *testing.T) {
	// G is held at AwaitingApproval (require_approval), so its plan sits in the
	// PlanStore. We then concurrently deliver a cancel (via the actor's mailbox) and a
	// Resume-approve (via the orchestrator), forcing the race on the plan-store consume.
	// Whichever wins the consume acts on the plan exactly once; the loser finds no plan.
	// The dispatch spy for G must be called AT MOST once across the pair.
	for _, order := range []string{"cancel-first", "approve-first"} {
		order := order
		t.Run(order, func(t *testing.T) {
			reg := orchestrator.NewStatusRegistry()
			mboxes := newCommandMailboxes()
			disp := newCancelSpyDispatch() // not latched: a dispatched G returns immediately
			pol := &perActionPolicy{spawnPlan: policy.DecisionRequireApproval, spawnWorker: map[string]policy.Decision{}}

			src := newGatedMessageSource(
				supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "build G"}},
			)
			setBaseConfigEnv(t)
			oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
				policyClient:  pol,
				dispatch:      disp.fn,
				auditSink:     audit.NewFakeSink(),
				planner:       newPerGoalPlanner(),
				messageSource: src,
				signingKey:    testSigningKey(t),
				registry:      reg,
				maxWorkers:    4,
				maxGoals:      8,
			})
			if err != nil {
				t.Fatalf("assembleOrchestrate: %v", err)
			}
			oc.mailboxes = mboxes
			t.Cleanup(cleanup)

			loopDone := make(chan error, 1)
			go func() { loopDone <- runControlLoop(context.Background(), oc) }()

			// Wait until G is AwaitingApproval (its plan is in the PlanStore).
			if st := waitState(t, reg, "G", orchestrator.StateAwaitingApproval, 3*time.Second); st != orchestrator.StateAwaitingApproval {
				t.Fatalf("TC-116-06[%s]: G state = %v, want AwaitingApproval", order, st)
			}

			// Race the cancel (mailbox → actor) and the Resume-approve (orchestrator),
			// in the requested order with maximum overlap.
			var raceWG sync.WaitGroup
			raceWG.Add(2)
			cancelFn := func() {
				defer raceWG.Done()
				mboxes.deliver(supervisor.Message{Kind: supervisor.MsgCancel, GoalID: "G"})
			}
			approveFn := func() {
				defer raceWG.Done()
				_, _ = oc.orch.Resume(context.Background(), orchestrator.Approval{From: "operator", To: "orchestrator", GoalID: "G", Approved: true})
			}
			if order == "cancel-first" {
				go cancelFn()
				go approveFn()
			} else {
				go approveFn()
				go cancelFn()
			}
			raceWG.Wait()

			// Allow the actor to process the mailbox cancel.
			time.Sleep(50 * time.Millisecond)

			// The plan is consumed exactly once: G's dispatch spy fired AT MOST once.
			if got := disp.dispatchCount("G"); got > 1 {
				t.Fatalf("TC-116-06[%s]: G dispatched %d times — cancel+approve double-dispatched (the plan was consumed twice)", order, got)
			}
			// The plan is gone from the store either way (consumed by whichever won).
			if oc.orch.HasPendingPlan("G") {
				t.Fatalf("TC-116-06[%s]: G's plan still pending after cancel+approve — it was not consumed", order)
			}

			// Drain: the source is exhausted; the actor is AwaitingApproval-then-resolved.
			// Closing the source (gatedMessageSource EOF) lets the loop shut the actor down.
			select {
			case err := <-loopDone:
				if err != nil {
					t.Fatalf("TC-116-06[%s]: control loop: %v", order, err)
				}
			case <-time.After(3 * time.Second):
				t.Fatalf("TC-116-06[%s]: control loop did not drain", order)
			}
		})
	}
}

// --- helpers -------------------------------------------------------------------

// releaseAllowEntry waits for goal gid's dispatch to enter (proving its worker is
// running) within a default timeout, failing the test if it does not.
func (d *cancelSpyDispatch) releaseAllowEntry(t *testing.T, gid string) {
	t.Helper()
	if !d.releaseEntryWithin(t, gid, 3*time.Second) {
		t.Fatalf("goal %q dispatch never entered", gid)
	}
}

// releaseEntryWithin reports whether goal gid's dispatch entered within timeout.
func (d *cancelSpyDispatch) releaseEntryWithin(t *testing.T, gid string, timeout time.Duration) bool {
	t.Helper()
	d.mu.Lock()
	enter := d.entered[gid]
	d.mu.Unlock()
	if enter == nil {
		return false
	}
	select {
	case <-enter:
		return true
	case <-time.After(timeout):
		return false
	}
}

// waitCancelled polls until goal gid's dispatch observed ctx cancellation, or timeout.
func waitCancelled(t *testing.T, d *cancelSpyDispatch, gid string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if d.wasCancelled(gid) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return d.wasCancelled(gid)
}
