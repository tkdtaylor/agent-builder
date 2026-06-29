package cli

// Tests for task 114 — status-query handler + immediate reporter answer (ADR 054 §3).
//
//   TC-114-01 — status reads registry, zero Handle/Resume calls (spy orchestrator)
//   TC-114-02 — fleet status (empty goalID) renders all three goals + states
//   TC-114-03 — per-goal with two sub-goals renders state + sub-goal lines; empty SubGoals no panic
//   TC-114-04 — answer arrives before latch releases (status is immediate, not blocked by dispatch)

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- TC-114-01 — status reads registry, never calls Handle/Resume ------------

// TestTC114_01_StatusHandlerZeroHandleResumeCalls verifies REQ-114-01:
// the status handler (newStatusHandler) only reads the registry and writes to
// the Reporter — it never calls Handle or Resume.
//
// Proof strategy: we count dispatch invocations in the control loop. A Handle
// call goes through dispatchPlan → dispatchOne → o.dispatch. The status query
// must NOT trigger any additional dispatch (and hence no additional Handle).
// We hold goal-1 at the latch (so it has entered dispatch once, via Handle),
// then issue a status query and assert dispatch count is still 1 — the status
// path contributed ZERO additional Handle calls.
func TestTC114_01_StatusHandlerZeroHandleResumeCalls(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}
	disp := newLatchDispatch("goal-1")

	// dispatchCounter wraps latchDispatch and counts invocations. Each call
	// to o.dispatch is caused by exactly one Handle (via dispatchPlan/dispatchOne).
	var mu sync.Mutex
	var dispatchCount int
	countingDisp := orchestrator.DispatchFunc(func(ctx context.Context, sub orchestrator.SubGoal, cfg runtimewiring.Config) error {
		mu.Lock()
		dispatchCount++
		mu.Unlock()
		return disp.fn(ctx, sub, cfg)
	})

	// Two messages: new goal-1 (held at latch → Dispatching), then status goal-1.
	msgCh := make(chan supervisor.Message, 4)
	msgCh <- supervisor.Message{
		Kind: supervisor.MsgNewGoal, GoalID: "goal-1",
		Goal: supervisor.Task{ID: "goal-1", Spec: "work"},
	}
	msgCh <- supervisor.Message{Kind: supervisor.MsgStatus, GoalID: "goal-1"}
	close(msgCh)

	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(
		Config{Stdout: discard(), Stderr: discard()},
		assembleOverrides{
			policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
			dispatch:      countingDisp,
			auditSink:     audit.NewFakeSink(),
			planner:       newPerGoalPlanner(),
			messageSource: &chanMessageSource{ch: msgCh},
			reporter:      rep,
			signingKey:    testSigningKey(t),
			registry:      reg,
			maxWorkers:    4,
			maxGoals:      8,
		},
	)
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Wait for goal-1 to enter dispatch (latch held → Dispatching).
	select {
	case <-disp.entered["goal-1"]:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-114-01: goal-1 dispatch never entered")
	}

	// Wait for the status reply to arrive in the Reporter.
	var statusReply string
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, r := range rep.all() {
			if strings.Contains(r, "goal-1") {
				statusReply = r
				break
			}
		}
		if statusReply != "" {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}

	// TC-114-01a: exactly one Reporter reply mentioning goal-1.
	if statusReply == "" {
		t.Fatalf("TC-114-01: no Reporter reply mentioning goal-1 after status query; reports: %v", rep.all())
	}

	// TC-114-01b: dispatch count is exactly 1 (goal-1's own dispatch, via Handle).
	// The status query must NOT have added any Handle calls. dispatchCount > 1
	// would mean the status path triggered another Handle/Resume call.
	mu.Lock()
	cnt := dispatchCount
	mu.Unlock()
	if cnt != 1 {
		t.Fatalf("TC-114-01: dispatch called %d time(s) while goal-1 is in Dispatching — status must not call Handle/Resume (want exactly 1 from the goal actor)", cnt)
	}

	// Release and drain.
	disp.releaseGoal("goal-1")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- TC-114-02 — fleet status renders all three goals + states ---------------

// TestTC114_02_FleetStatusRendersAllGoals verifies REQ-114-02: an empty-goalID
// status query renders one entry per registered goal with its GoalState.
func TestTC114_02_FleetStatusRendersAllGoals(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}

	// Register three goals at different states.
	reg.Register("goal-1", orchestrator.StatePlanning)
	reg.Register("goal-2", orchestrator.StateDispatching)
	reg.Register("goal-3", orchestrator.StateDone)

	handler := newStatusHandler(context.Background(), reg, rep)

	// Invoke with empty GoalID → fleet query.
	handler("")

	reports := rep.all()
	if len(reports) != 1 {
		t.Fatalf("TC-114-02: fleet status produced %d report(s), want exactly 1", len(reports))
	}
	reply := reports[0]

	// TC-114-02a: all three goalIDs appear in the reply.
	for _, id := range []string{"goal-1", "goal-2", "goal-3"} {
		if !strings.Contains(reply, id) {
			t.Errorf("TC-114-02: reply does not contain %q:\n%s", id, reply)
		}
	}

	// TC-114-02b: all three state strings appear in the reply.
	wantStates := []string{
		orchestrator.StatePlanning.String(),
		orchestrator.StateDispatching.String(),
		orchestrator.StateDone.String(),
	}
	for _, state := range wantStates {
		if !strings.Contains(reply, state) {
			t.Errorf("TC-114-02: reply does not contain state %q:\n%s", state, reply)
		}
	}

	// TC-114-02c: exactly three goal entries (no duplicates, none dropped).
	// Count "goal-" occurrences as entry count.
	count := strings.Count(reply, "goal-")
	if count != 3 {
		t.Errorf("TC-114-02: fleet status has %d goal references, want exactly 3:\n%s", count, reply)
	}
}

// --- TC-114-03 — per-goal sub-goal progress; empty SubGoals no panic ---------

// TestTC114_03_PerGoalStatusRendersSuGoalProgress verifies REQ-114-03: a
// per-goal status query renders GoalState + per-sub-goal name/recipe/state.
func TestTC114_03_PerGoalStatusRendersSubGoalProgress(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}

	// Register goal-7 at Dispatching.
	reg.Register("goal-7", orchestrator.StateDispatching)

	// Set two sub-goals: auth/coding-agent=done, docs/docs-fix=running.
	reg.SetSubGoal("goal-7", orchestrator.SubGoalProgress{Name: "auth", Recipe: "coding-agent", State: "done"})
	reg.SetSubGoal("goal-7", orchestrator.SubGoalProgress{Name: "docs", Recipe: "docs-fix", State: "running"})

	handler := newStatusHandler(context.Background(), reg, rep)
	handler("goal-7")

	reports := rep.all()
	if len(reports) != 1 {
		t.Fatalf("TC-114-03: per-goal status produced %d report(s), want 1", len(reports))
	}
	reply := reports[0]

	// TC-114-03a: reply contains goal-7 and its state Dispatching.
	if !strings.Contains(reply, "goal-7") {
		t.Errorf("TC-114-03: reply does not contain goalID %q:\n%s", "goal-7", reply)
	}
	if !strings.Contains(reply, orchestrator.StateDispatching.String()) {
		t.Errorf("TC-114-03: reply does not contain state %q:\n%s", orchestrator.StateDispatching.String(), reply)
	}

	// TC-114-03b: reply renders auth/coding-agent=done.
	if !strings.Contains(reply, "auth") {
		t.Errorf("TC-114-03: reply does not contain sub-goal name %q:\n%s", "auth", reply)
	}
	if !strings.Contains(reply, "coding-agent") {
		t.Errorf("TC-114-03: reply does not contain recipe %q:\n%s", "coding-agent", reply)
	}
	if !strings.Contains(reply, "done") {
		t.Errorf("TC-114-03: reply does not contain sub-goal state %q:\n%s", "done", reply)
	}

	// TC-114-03c: reply renders docs/docs-fix=running.
	if !strings.Contains(reply, "docs") {
		t.Errorf("TC-114-03: reply does not contain sub-goal name %q:\n%s", "docs", reply)
	}
	if !strings.Contains(reply, "docs-fix") {
		t.Errorf("TC-114-03: reply does not contain recipe %q:\n%s", "docs-fix", reply)
	}
	if !strings.Contains(reply, "running") {
		t.Errorf("TC-114-03: reply does not contain sub-goal state %q:\n%s", "running", reply)
	}
}

// TestTC114_03_EmptySubGoalsNoPanic verifies the defensive requirement: invoking
// the per-goal renderer for a known goal with an empty SubGoals slice does not
// panic and still renders the goal's state.
func TestTC114_03_EmptySubGoalsNoPanic(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}

	// Register a goal with NO sub-goals.
	reg.Register("goal-empty", orchestrator.StatePlanning)

	handler := newStatusHandler(context.Background(), reg, rep)

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("TC-114-03 (empty SubGoals): per-goal renderer panicked: %v", r)
		}
	}()
	handler("goal-empty")

	reports := rep.all()
	if len(reports) != 1 {
		t.Fatalf("TC-114-03 (empty SubGoals): produced %d reports, want 1", len(reports))
	}
	if !strings.Contains(reports[0], orchestrator.StatePlanning.String()) {
		t.Errorf("TC-114-03 (empty SubGoals): reply does not contain state Planning:\n%s", reports[0])
	}
}

// --- TC-114-04 — answer arrives before dispatch latch is released ------------

// TestTC114_04_StatusAnswersBeforeLatchReleases verifies REQ-114-04: a status
// query issued while a goal is held at a dispatch latch (state = Dispatching)
// returns the Dispatching reply to the Reporter BEFORE the test releases the
// latch. The ordering proof:
//   1. goal-1 enters dispatch and is held (latch acquired; state = Dispatching).
//   2. MsgStatus goal-1 is fed to the control loop.
//   3. Status reply (with "dispatching") arrives in the Reporter spy.
//   4. THEN (and only then) the latch is released.
//
// If step 3 did not complete before step 4, the status path was blocking on
// the goal actor — which is forbidden by REQ-114-01/ADR 054 §3.
func TestTC114_04_StatusAnswersBeforeLatchReleases(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}
	disp := newLatchDispatch("goal-1")

	msgCh := make(chan supervisor.Message, 4)

	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(
		Config{Stdout: discard(), Stderr: discard()},
		assembleOverrides{
			policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
			dispatch:      disp.fn,
			auditSink:     audit.NewFakeSink(),
			planner:       newPerGoalPlanner(),
			messageSource: &chanMessageSource{ch: msgCh},
			reporter:      rep,
			signingKey:    testSigningKey(t),
			registry:      reg,
			maxWorkers:    4,
			maxGoals:      8,
		},
	)
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// Step 1: send new-goal goal-1; wait for its dispatch to enter (latch held).
	msgCh <- supervisor.Message{
		Kind: supervisor.MsgNewGoal, GoalID: "goal-1",
		Goal: supervisor.Task{ID: "goal-1", Spec: "hold"},
	}
	select {
	case <-disp.entered["goal-1"]:
	case <-time.After(3 * time.Second):
		t.Fatal("TC-114-04: goal-1 dispatch never entered")
	}

	// Step 2: send the status query while goal-1 is held in Dispatching.
	msgCh <- supervisor.Message{Kind: supervisor.MsgStatus, GoalID: "goal-1"}
	close(msgCh) // no more messages; source will EOF after status is processed

	// Step 3: wait for the status reply (with "dispatching") to appear.
	// This must happen WHILE the latch is still held (step 4 has not occurred).
	replyArrived := make(chan struct{})
	go func() {
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			for _, r := range rep.all() {
				if strings.Contains(r, "goal-1") && strings.Contains(strings.ToLower(r), "dispatching") {
					close(replyArrived)
					return
				}
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	select {
	case <-replyArrived:
		// Reply arrived while latch is still held — TC-114-04 PASS.
		// The latch is released in step 4 below.
	case <-time.After(3 * time.Second):
		t.Fatal("TC-114-04: status reply did not arrive within 3s while dispatch latch held — status is blocking on dispatch")
	}

	// Step 4: release the latch AFTER the reply arrived.
	disp.releaseGoal("goal-1")

	// Drain the loop.
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- test helper: channel-driven MessageSource -------------------------------

// chanMessageSource is a MessageSource backed by a buffered channel, allowing
// the test to push messages in precise order. Closing the channel signals EOF.
type chanMessageSource struct {
	ch <-chan supervisor.Message
}

func (s *chanMessageSource) Next() (supervisor.Message, bool, error) {
	msg, ok := <-s.ch
	if !ok {
		return supervisor.Message{}, false, nil
	}
	return msg, true, nil
}
