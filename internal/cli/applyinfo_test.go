package cli

// Tests for task 115 — apply-info-at-checkpoint, CLI/control-loop side (ADR 054 §4).
// These exercise the live goal actor: the queue-don't-interrupt write, the held
// worker never being mutated by an info, and the amendment sub-goal `G/amend-N`
// spawned (and gated) for an already-dispatched goal.
//
//   TC-115-01 — info appends to the pending-info queue, touches no held worker
//   TC-115-04 — already-dispatched goal: info spawns G/amend-1 through the gates;
//               a second info → G/amend-2 (monotonic, collision-free)
//   TC-115-05 — held worker never mutated mid-task (dispatched branch); the only
//               observable synchronous effect is the queue append
//   TC-115-06 — amendment passes the self-repo bright line + policy gate (no bypass);
//               SEC-003 deny-audit fires; G/amend-1 / G/amend-2 distinct

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

// gatedMessageSource yields each scripted message only after the test releases the
// corresponding gate, then blocks until released again — so a test can control the
// EXACT moment a message (e.g. an info) is read by the control loop relative to an
// observed goal state (e.g. "G is now Dispatching"). After all messages are
// released, Next returns ok=false (EOF). The first message is released eagerly so
// the new-goal flows without an explicit release call.
type gatedMessageSource struct {
	msgs  []supervisor.Message
	gates []chan struct{}
	idx   int
}

func newGatedMessageSource(msgs ...supervisor.Message) *gatedMessageSource {
	gates := make([]chan struct{}, len(msgs))
	for i := range gates {
		gates[i] = make(chan struct{})
	}
	s := &gatedMessageSource{msgs: msgs, gates: gates}
	if len(gates) > 0 {
		close(gates[0]) // first message (the new-goal) flows immediately
	}
	return s
}

// release opens the gate for message index i (0-based), letting the control loop's
// next Next() return it.
func (s *gatedMessageSource) release(i int) { close(s.gates[i]) }

func (s *gatedMessageSource) Next() (supervisor.Message, bool, error) {
	if s.idx >= len(s.msgs) {
		return supervisor.Message{}, false, nil
	}
	<-s.gates[s.idx] // block until the test releases this message
	m := s.msgs[s.idx]
	s.idx++
	return m, true, nil
}

// amendmentPlanner emits one sub-goal per goal, keyed off the goal's ID. For the
// PARENT goal (and any goal whose recipe/repo are not overridden) it produces a
// plain "coding-agent" sub-goal on no specific repo. For specific amendment goalIDs
// it can be configured to produce a sub-goal targeting the own-repo (to trip the
// self-repo bright line) or carrying a recipe the policy denies — so TC-115-06 can
// drive both gate paths through the amendment route.
type amendmentPlanner struct {
	mu sync.Mutex
	// recipeByGoal overrides the sub-goal recipe for a goalID (default "coding-agent").
	recipeByGoal map[string]string
	// repoByGoal overrides the sub-goal Task.Repo for a goalID (default "" = no repo).
	repoByGoal map[string]string
	calls      map[string]int
}

func newAmendmentPlanner() *amendmentPlanner {
	return &amendmentPlanner{
		recipeByGoal: map[string]string{},
		repoByGoal:   map[string]string{},
		calls:        map[string]int{},
	}
}

func (p *amendmentPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[goal.ID]++
	recipe := "coding-agent"
	if r, ok := p.recipeByGoal[goal.ID]; ok {
		recipe = r
	}
	repo := ""
	if r, ok := p.repoByGoal[goal.ID]; ok {
		repo = r
	}
	return orchestrator.Plan{
		Goal:   goal.Spec,
		GoalID: goal.ID,
		SubGoals: []orchestrator.SubGoal{{
			RecipeName: recipe,
			Task:       supervisor.Task{ID: goal.ID + "-sub-0", Spec: goal.Spec, Repo: repo},
		}},
	}, nil
}

func (p *amendmentPlanner) planCount(goalID string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls[goalID]
}

// mutationSpyDispatch records, per dispatched sub-goal, the exact target repo it was
// asked to dispatch, and (for held goals) blocks at a per-goal latch. It is the
// instrument that proves BOTH "the held worker is never touched a second time" and
// "the own-repo amendment sub-goal is never dispatched" — the dispatch spy is the
// single point a worker would be (re)launched, so its call set is the ground truth.
type mutationSpyDispatch struct {
	mu      sync.Mutex
	calls   []orchestrator.SubGoal // every sub-goal dispatch, in arrival order
	entered map[string]chan struct{}
	release map[string]chan struct{}
}

func newMutationSpyDispatch(heldGoalIDs ...string) *mutationSpyDispatch {
	d := &mutationSpyDispatch{
		entered: map[string]chan struct{}{},
		release: map[string]chan struct{}{},
	}
	for _, g := range heldGoalIDs {
		d.entered[g] = make(chan struct{})
		d.release[g] = make(chan struct{})
	}
	return d
}

func (d *mutationSpyDispatch) fn(ctx context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	d.mu.Lock()
	d.calls = append(d.calls, sub)
	gid := goalIDFromSub(sub.Task.ID)
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

func (d *mutationSpyDispatch) releaseGoal(gid string) {
	d.mu.Lock()
	rel := d.release[gid]
	d.mu.Unlock()
	if rel != nil {
		close(rel)
	}
}

// dispatchedRepos returns the Task.Repo of every dispatched sub-goal (used to assert
// the own-repo amendment was NEVER dispatched).
func (d *mutationSpyDispatch) dispatchedTaskIDs() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.calls))
	for _, c := range d.calls {
		out = append(out, c.Task.ID)
	}
	return out
}

// assembleApplyInfo builds an orchestrateConfig for an apply-info test: a scripted
// message source, a shared registry, an injected mailbox map, the mutation-spy
// dispatch, and a configurable per-action policy + audit sink (for the gate tests).
func assembleApplyInfo(t *testing.T, src supervisor.MessageSource, planner orchestrator.Planner, dispatch orchestrator.DispatchFunc, pol orchestrator.PolicyClient, sink audit.Sink, reg *orchestrator.StatusRegistry, mboxes *commandMailboxes) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient:  pol,
		dispatch:      dispatch,
		auditSink:     sink,
		planner:       planner,
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
	return oc
}

// waitPendingInfo polls the registry until the goal's pending-info queue has the
// wanted length (or the deadline elapses); returns the final queue.
func waitPendingInfo(t *testing.T, reg *orchestrator.StatusRegistry, goalID string, wantLen int, timeout time.Duration) []string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last []string
	for time.Now().Before(deadline) {
		last = reg.PendingInfo(goalID)
		if len(last) == wantLen {
			return last
		}
		time.Sleep(2 * time.Millisecond)
	}
	return last
}

// --- TC-115-01 — info appends to the pending-info queue, touches no worker ----

// TestTC115_01_InfoAppendsExactlyOneQueueEntry asserts the "pending-info queue has
// exactly the one entry" half of TC-115-01 deterministically, on the AwaitingApproval
// branch where applyInfo enqueues WITHOUT draining (so the entry is observable). A
// worker held at a latch during the info delivery proves the queue append does not
// touch the held worker.
func TestTC115_01_InfoAppendsExactlyOneQueueEntry(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	// require_approval pauses G at AwaitingApproval, so no worker dispatches yet AND the
	// info is enqueued-not-drained at this checkpoint.
	disp := newMutationSpyDispatch() // no held goal; nothing dispatches under approval
	pol := &perActionPolicy{spawnPlan: policy.DecisionRequireApproval, spawnWorker: map[string]policy.Decision{}}

	src := &scriptedMessageSource{msgs: []supervisor.Message{
		{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "build it"}},
		{Kind: supervisor.MsgInfo, GoalID: "G", Text: "also add a metrics endpoint"},
	}}
	oc := assembleApplyInfo(t, src, newAmendmentPlanner(), disp.fn, pol, audit.NewFakeSink(), reg, mboxes)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Wait until G is AwaitingApproval (the plan paused), then the info is enqueued.
	if st := waitState(t, reg, "G", orchestrator.StateAwaitingApproval, 3*time.Second); st != orchestrator.StateAwaitingApproval {
		t.Fatalf("G state = %v, want AwaitingApproval", st)
	}

	// The pending-info queue for G reaches EXACTLY one entry — the one info text.
	got := waitPendingInfo(t, reg, "G", 1, 3*time.Second)
	if len(got) != 1 || got[0] != "also add a metrics endpoint" {
		t.Fatalf("pending-info queue = %v, want exactly [\"also add a metrics endpoint\"]", got)
	}

	// No worker was dispatched (the only synchronous effect of the info is the queue
	// append; the held-at-approval goal dispatches nothing).
	if ids := disp.dispatchedTaskIDs(); len(ids) != 0 {
		t.Fatalf("dispatched sub-goals = %v, want none (info under AwaitingApproval must not dispatch)", ids)
	}

	// The control loop drains after the source is exhausted (the goal stays
	// AwaitingApproval with no approval delivered — the actor's Handle has returned).
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

func TestTC115_01_InfoAppendsToQueueTouchesNoWorker(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	// Hold G's single sub-goal worker at a latch (worker running, AwaitingApproval is
	// NOT used here — policy allows so G dispatches; the worker is held mid-dispatch).
	disp := newMutationSpyDispatch("G")
	pol := &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}}

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "build it"}},
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "also add a metrics endpoint"},
	)
	oc := assembleApplyInfo(t, src, newAmendmentPlanner(), disp.fn, pol, audit.NewFakeSink(), reg, mboxes)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// The worker for G enters dispatch and is HELD at the latch — G is Dispatching.
	select {
	case <-disp.entered["G"]:
	case <-time.After(3 * time.Second):
		t.Fatal("G's worker never entered dispatch")
	}
	if st := waitState(t, reg, "G", orchestrator.StateDispatching, 3*time.Second); st != orchestrator.StateDispatching {
		t.Fatalf("G state = %v, want Dispatching before releasing the info", st)
	}
	// Release the info now (G is held mid-dispatch). The actor enqueues it (the single
	// synchronous write, touching no running worker) then, because G is PAST its
	// approval gate, drains the queue and spawns an amendment. The amendment reaching
	// Done is the proof the info was consumed at a checkpoint without touching the held
	// worker.
	src.release(1)
	if st := waitState(t, reg, "G/amend-1", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("info was not applied at a checkpoint: amendment G/amend-1 state = %v, want Done", st)
	}

	// The held worker is UNTOUCHED: no extra dispatch call for G's original sub-goal
	// happened as a result of the info. The only NEW dispatch since the info is the
	// amendment's own sub-goal (G/amend-1-sub-0), never a re-dispatch of G-sub-0.
	ids := disp.dispatchedTaskIDs()
	gOriginalDispatches := 0
	for _, id := range ids {
		if id == "G-sub-0" {
			gOriginalDispatches++
		}
	}
	if gOriginalDispatches != 1 {
		t.Fatalf("G's original sub-goal G-sub-0 was dispatched %d times, want exactly 1 (info must not re-dispatch the held worker)", gOriginalDispatches)
	}

	// The held worker's latch is still held (it has not completed): G is still
	// Dispatching, not Done.
	if st, _ := reg.Get("G"); st.State != orchestrator.StateDispatching {
		t.Fatalf("G state while worker held = %v, want Dispatching (the info must not advance/cancel the held worker)", st.State)
	}

	// Releasing the latch lets the original sub-goal complete normally.
	disp.releaseGoal("G")
	if st := waitState(t, reg, "G", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("G did not complete after the latch released: state = %v, want Done", st)
	}
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- TC-115-04 — already-dispatched goal: info spawns G/amend-N through gates --

func TestTC115_04_DispatchedGoalInfoSpawnsAmendmentThroughGates(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	disp := newMutationSpyDispatch("G") // hold G's original worker; amendments run free
	planner := newAmendmentPlanner()
	// A recording policy so we can confirm the spawn-worker gate fired for the
	// amendment's sub-goal (the normal gated path, no bypass).
	pol := &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}}

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "ship feature"}},
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "add a rollback path"},
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "also log the rollback"},
	)
	oc := assembleApplyInfo(t, src, planner, disp.fn, pol, audit.NewFakeSink(), reg, mboxes)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// G's worker enters dispatch and is held — G is now PAST its approval gate
	// (Dispatching). ONLY now release the info, so the info is processed at the
	// dispatched-goal checkpoint (→ amendment), deterministically.
	select {
	case <-disp.entered["G"]:
	case <-time.After(3 * time.Second):
		t.Fatal("G's worker never entered dispatch")
	}
	if st := waitState(t, reg, "G", orchestrator.StateDispatching, 3*time.Second); st != orchestrator.StateDispatching {
		t.Fatalf("G state = %v, want Dispatching before releasing the info", st)
	}
	src.release(1) // first info

	// First info → amendment goal actor for G/amend-1, planned and gated + dispatched.
	if st := waitState(t, reg, "G/amend-1", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("first info did not spawn a completed G/amend-1: state = %v", st)
	}
	// Second info (released now, G still held in Dispatching) → G/amend-2 (monotonic,
	// collision-free).
	src.release(2)
	if st := waitState(t, reg, "G/amend-2", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("second info did not spawn a completed G/amend-2: state = %v", st)
	}

	// The amendment carries the info as its goal text and was planned through the normal
	// path (the planner was invoked for each amendment goalID).
	if n := planner.planCount("G/amend-1"); n != 1 {
		t.Fatalf("planner.Plan called %d times for G/amend-1, want exactly 1 (gated spawn-plan path)", n)
	}
	if n := planner.planCount("G/amend-2"); n != 1 {
		t.Fatalf("planner.Plan called %d times for G/amend-2, want exactly 1", n)
	}

	// The amendment's sub-goals were dispatched through the spawn-worker gate (the
	// dispatch spy saw G/amend-1-sub-0 and G/amend-2-sub-0).
	ids := disp.dispatchedTaskIDs()
	wantAmend := map[string]bool{"G/amend-1-sub-0": false, "G/amend-2-sub-0": false}
	for _, id := range ids {
		if _, ok := wantAmend[id]; ok {
			wantAmend[id] = true
		}
	}
	for id, seen := range wantAmend {
		if !seen {
			t.Fatalf("amendment sub-goal %q was not dispatched through the spawn path; dispatched = %v", id, ids)
		}
	}

	// G's original in-flight worker is unaffected: G-sub-0 was dispatched exactly once
	// (the info spawned siblings, it did not re-dispatch or mutate the held worker).
	gOriginal := 0
	for _, id := range ids {
		if id == "G-sub-0" {
			gOriginal++
		}
	}
	if gOriginal != 1 {
		t.Fatalf("G-sub-0 dispatched %d times, want exactly 1 (originals unaffected by info)", gOriginal)
	}

	// Release G's held worker and drain.
	disp.releaseGoal("G")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- TC-115-05 — held worker never mutated mid-task --------------------------

func TestTC115_05_HeldWorkerNeverMutatedByInfo(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	disp := newMutationSpyDispatch("G")
	pol := &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}}

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "long task"}},
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "extra requirement"},
	)
	oc := assembleApplyInfo(t, src, newAmendmentPlanner(), disp.fn, pol, audit.NewFakeSink(), reg, mboxes)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	select {
	case <-disp.entered["G"]:
	case <-time.After(3 * time.Second):
		t.Fatal("G's worker never entered dispatch")
	}
	if st := waitState(t, reg, "G", orchestrator.StateDispatching, 3*time.Second); st != orchestrator.StateDispatching {
		t.Fatalf("G state = %v, want Dispatching before releasing the info", st)
	}
	src.release(1) // info, while G is held in Dispatching

	// Wait for the amendment to complete — that is the proof the info was fully applied
	// at a checkpoint. Throughout this, the held worker (G-sub-0) must NOT be touched.
	if st := waitState(t, reg, "G/amend-1", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("info not applied: G/amend-1 state = %v", st)
	}

	// The held worker received no cancel/restart/mutate: G-sub-0 was dispatched exactly
	// once and G is still Dispatching (the worker is still at the latch). The ONLY
	// observable synchronous effect of the info was the queue append + the amendment
	// sibling — never a second interaction with the held worker.
	ids := disp.dispatchedTaskIDs()
	gOriginal := 0
	for _, id := range ids {
		if id == "G-sub-0" {
			gOriginal++
		}
	}
	if gOriginal != 1 {
		t.Fatalf("held worker G-sub-0 was interacted with %d times, want exactly 1 (no mutate/cancel/restart from info)", gOriginal)
	}
	if st, _ := reg.Get("G"); st.State != orchestrator.StateDispatching {
		t.Fatalf("G state while worker held = %v, want still Dispatching (info must not advance the held worker)", st.State)
	}

	// Releasing the latch lets the original worker finish normally.
	disp.releaseGoal("G")
	if st := waitState(t, reg, "G", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("G did not finish after release: state = %v", st)
	}
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- TC-115-06 — amendment passes self-repo + policy gate; IDs collision-free --

func TestTC115_06_AmendmentPassesSelfRepoAndPolicyGate(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()
	disp := newMutationSpyDispatch("G")
	planner := newAmendmentPlanner()
	// G/amend-1's sub-goal targets the OWN-REPO → the self-repo bright line must deny it.
	planner.repoByGoal["G/amend-1"] = orchestrator.OwnRepo
	// G/amend-2's sub-goal uses a recipe the policy DENIES at spawn-worker.
	planner.recipeByGoal["G/amend-2"] = "docs-fix"

	sink := audit.NewFakeSink()
	// Policy: allow spawn-plan and the parent's coding-agent spawn-worker; DENY
	// spawn-worker for the "docs-fix" recipe (the amend-2 sub-goal).
	pol := &perActionPolicy{
		spawnPlan:   policy.DecisionAllow,
		spawnWorker: map[string]policy.Decision{"docs-fix": policy.DecisionDeny},
	}

	src := newGatedMessageSource(
		supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: "G", Goal: supervisor.Task{ID: "G", Spec: "ship"}},
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "edit my own repo"},  // → G/amend-1 (own-repo)
		supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "G", Text: "do a denied thing"}, // → G/amend-2 (policy deny)
	)
	oc := assembleApplyInfo(t, src, planner, disp.fn, pol, sink, reg, mboxes)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	select {
	case <-disp.entered["G"]:
	case <-time.After(3 * time.Second):
		t.Fatal("G's worker never entered dispatch")
	}
	if st := waitState(t, reg, "G", orchestrator.StateDispatching, 3*time.Second); st != orchestrator.StateDispatching {
		t.Fatalf("G state = %v, want Dispatching before releasing the info", st)
	}
	src.release(1) // own-repo info → G/amend-1
	if st := waitState(t, reg, "G/amend-1", orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
		t.Fatalf("G/amend-1 did not reach a terminal state: %v", st)
	}
	src.release(2) // policy-denied info → G/amend-2

	// Both amendment goals run to a terminal state (the goals complete; their SUB-GOALS
	// are denied — a denied sub-goal yields a completed goal with a failed/denied
	// outcome, NOT a dispatched worker).
	for _, gid := range []string{"G/amend-1", "G/amend-2"} {
		if st := waitState(t, reg, gid, orchestrator.StateDone, 3*time.Second); st != orchestrator.StateDone {
			t.Fatalf("%s did not reach a terminal state: %v", gid, st)
		}
	}

	ids := disp.dispatchedTaskIDs()
	// The own-repo amendment sub-goal (G/amend-1-sub-0) was NEVER dispatched — the
	// self-repo bright line fired on it exactly as on any sub-goal.
	for _, id := range ids {
		if id == "G/amend-1-sub-0" {
			t.Fatalf("own-repo amendment sub-goal G/amend-1-sub-0 was DISPATCHED — the self-repo bright line did not fire on the amendment route; dispatched=%v", ids)
		}
	}
	// The policy-denied amendment sub-goal (G/amend-2-sub-0) was NEVER dispatched.
	for _, id := range ids {
		if id == "G/amend-2-sub-0" {
			t.Fatalf("policy-denied amendment sub-goal G/amend-2-sub-0 was DISPATCHED — the spawn-worker deny did not fire on the amendment route; dispatched=%v", ids)
		}
	}

	// SEC-003: a spawn-decided DENY audit event was emitted for the denied amendment
	// sub-goals (fail-closed: the deny is durably recorded, never silently dropped).
	denyEvents := 0
	for _, ev := range sink.Events() {
		if ev.Action == audit.ActionSpawnDecided && ev.Detail.PolicyDecision == string(policy.DecisionDeny) {
			denyEvents++
		}
	}
	if denyEvents < 2 {
		t.Fatalf("spawn-decided DENY audit events = %d, want >= 2 (own-repo bright line + policy deny on the two amendments)", denyEvents)
	}

	// The two amendments produced DISTINCT, collision-free IDs.
	_, ok1 := reg.Get("G/amend-1")
	_, ok2 := reg.Get("G/amend-2")
	if !ok1 || !ok2 {
		t.Fatalf("amendment IDs not both registered: G/amend-1=%v G/amend-2=%v", ok1, ok2)
	}

	disp.releaseGoal("G")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}
