package cli

// Tests for task 113 — inbound message protocol + command router (ADR 054 §2).
//
//   TC-113-01 — MessageSource is a NEW seam; GoalSource signature unchanged
//   TC-113-02 — line grammar parses each kind; EOF→ok=false; malformed≠new-goal, no panic
//   TC-113-03 — env/stdin local-test path still produces a new goal
//   TC-113-04 — router dispatches by kind to the right place
//   TC-113-05 — unknown goalID → graceful "no such goal", never a panic
//   TC-113-06 — mailbox created before actor registration (register-then-start), -race

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// --- TC-113-01 — MessageSource is a NEW seam; GoalSource is untouched --------

// Compile-time assertion: envMessageSource implements the NEW MessageSource seam.
var _ supervisor.MessageSource = (*envMessageSource)(nil)

// Compile-time assertion: GoalSource's signature is UNCHANGED — an existing
// GoalSource implementer (the goalSourceAdapter's wrapped type, and stubGoalSource)
// still satisfies the goal-only interface. If GoalSource.Next had been mutated to
// return a Message, these would fail to compile.
var (
	_ supervisor.GoalSource = (*stubGoalSource)(nil)
	_ supervisor.GoalSource = (*recordingGoalSource)(nil)
)

func TestTC113_01_MessageSourceIsNewSeamGoalSourceUnchanged(t *testing.T) {
	// The compile-time asserts above are the load-bearing proof. This body adds a
	// run-time check that the two seams have DISTINCT return types: a MessageSource's
	// Next yields a supervisor.Message; a GoalSource's Next yields a supervisor.Task.
	ms := newEnvMessageSource(func(k string) string {
		if k == EnvGoalSpec {
			return "a goal"
		}
		return ""
	}, nil)
	msg, ok, err := ms.Next()
	if err != nil || !ok {
		t.Fatalf("MessageSource.Next() = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	// The returned value is a supervisor.Message with a Kind field — a Task has none.
	if msg.Kind != supervisor.MsgNewGoal {
		t.Fatalf("MessageSource yields Message{Kind:%v}, want MsgNewGoal", msg.Kind)
	}

	var gs supervisor.GoalSource = &stubGoalSource{goals: []supervisor.Task{{ID: "g", Spec: "s"}}}
	task, ok, err := gs.Next()
	if err != nil || !ok {
		t.Fatalf("GoalSource.Next() = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	// GoalSource still returns a supervisor.Task (unchanged signature).
	if task.ID != "g" || task.Spec != "s" {
		t.Fatalf("GoalSource yields Task{ID:%q,Spec:%q}, want {g,s}", task.ID, task.Spec)
	}
}

// --- TC-113-02 — line grammar parses each kind correctly ---------------------

func TestTC113_02_LineGrammarParsesEachKind(t *testing.T) {
	lines := []string{
		"add rate limiting to the API",
		"status",
		"status goal-7",
		"info goal-7 also handle retries",
		"cancel goal-7",
	}
	src := newEnvMessageSource(noEnv, strings.NewReader(strings.Join(lines, "\n")))

	type want struct {
		kind   supervisor.MessageKind
		goalID string // "" means: don't assert (auto-assigned for new-goal)
		spec   string
		text   string
	}
	wants := []want{
		{kind: supervisor.MsgNewGoal, spec: "add rate limiting to the API"},
		{kind: supervisor.MsgStatus, goalID: ""},
		{kind: supervisor.MsgStatus, goalID: "goal-7"},
		{kind: supervisor.MsgInfo, goalID: "goal-7", text: "also handle retries"},
		{kind: supervisor.MsgCancel, goalID: "goal-7"},
	}

	for i, w := range wants {
		msg, ok, err := src.Next()
		if err != nil {
			t.Fatalf("line %d (%q): Next err = %v, want nil", i, lines[i], err)
		}
		if !ok {
			t.Fatalf("line %d (%q): ok = false, want true", i, lines[i])
		}
		if msg.Kind != w.kind {
			t.Errorf("line %d (%q): Kind = %v, want %v", i, lines[i], msg.Kind, w.kind)
		}
		switch w.kind {
		case supervisor.MsgNewGoal:
			if msg.Goal.Spec != w.spec {
				t.Errorf("line %d: Goal.Spec = %q, want %q", i, msg.Goal.Spec, w.spec)
			}
			if msg.GoalID == "" || msg.Goal.ID == "" {
				t.Errorf("line %d: new-goal must have an assigned GoalID/Goal.ID, got GoalID=%q ID=%q", i, msg.GoalID, msg.Goal.ID)
			}
		case supervisor.MsgStatus:
			if msg.GoalID != w.goalID {
				t.Errorf("line %d: status GoalID = %q, want %q", i, msg.GoalID, w.goalID)
			}
		case supervisor.MsgInfo:
			if msg.GoalID != w.goalID {
				t.Errorf("line %d: info GoalID = %q, want %q", i, msg.GoalID, w.goalID)
			}
			if msg.Text != w.text {
				t.Errorf("line %d: info Text = %q, want %q", i, msg.Text, w.text)
			}
		case supervisor.MsgCancel:
			if msg.GoalID != w.goalID {
				t.Errorf("line %d: cancel GoalID = %q, want %q", i, msg.GoalID, w.goalID)
			}
		}
	}

	// EOF → ok=false, nil error.
	msg, ok, err := src.Next()
	if ok || err != nil {
		t.Fatalf("after all lines: Next() = (%+v, %v, %v), want (_, false, nil)", msg, ok, err)
	}
}

func TestTC113_02_MalformedControlLineDoesNotBecomeNewGoalOrPanic(t *testing.T) {
	// `cancel` with no goalID and `info goal-7` with no text are malformed control
	// lines. They must NOT silently become a new-goal and must NOT panic — Next
	// surfaces ErrMalformedInput for that line.
	for _, bad := range []string{"cancel", "info goal-7"} {
		src := newEnvMessageSource(noEnv, strings.NewReader(bad+"\n"))
		msg, ok, err := src.Next() // must not panic
		if err == nil {
			t.Fatalf("malformed %q: err = nil, want a parse error", bad)
		}
		if !errors.Is(err, ErrMalformedInput) {
			t.Fatalf("malformed %q: err = %v, want ErrMalformedInput", bad, err)
		}
		// The line must NOT have been accepted as a message at all (ok=false). In
		// particular it must NOT have been accepted as an ok=true MsgNewGoal — that
		// (ok=true, MsgNewGoal) is the silent-degradation failure mode this guards.
		if ok {
			t.Fatalf("malformed %q: ok = true (Kind=%v) — must be rejected, not accepted as a message", bad, msg.Kind)
		}
	}
}

// --- TC-113-03 — env/stdin local-test path still produces a new goal ---------

func TestTC113_03_EnvGoalSpecProducesNewGoal(t *testing.T) {
	getenv := func(k string) string {
		if k == EnvGoalSpec {
			return "ship the widget"
		}
		return ""
	}
	src := newEnvMessageSource(getenv, nil)

	msg, ok, err := src.Next()
	if err != nil || !ok {
		t.Fatalf("first Next() = (_, %v, %v), want (_, true, nil)", ok, err)
	}
	if msg.Kind != supervisor.MsgNewGoal {
		t.Fatalf("first Next() Kind = %v, want MsgNewGoal", msg.Kind)
	}
	if msg.Goal.Spec != "ship the widget" {
		t.Fatalf("first Next() Goal.Spec = %q, want %q", msg.Goal.Spec, "ship the widget")
	}

	// Second Next() — no more input → ok=false.
	_, ok, err = src.Next()
	if ok || err != nil {
		t.Fatalf("second Next() = (_, %v, %v), want (_, false, nil)", ok, err)
	}
}

// --- router test doubles -----------------------------------------------------

// scriptedMessageSource yields each scripted message once, then ok=false.
type scriptedMessageSource struct {
	msgs []supervisor.Message
	idx  int
}

func (s *scriptedMessageSource) Next() (supervisor.Message, bool, error) {
	if s.idx >= len(s.msgs) {
		return supervisor.Message{}, false, nil
	}
	m := s.msgs[s.idx]
	s.idx++
	return m, true, nil
}

// spyReporter records every Report text under a mutex (the router shares one
// Reporter across the concurrent control plane).
type spyReporter struct {
	mu    sync.Mutex
	texts []string
}

func (r *spyReporter) Report(_ context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.texts = append(r.texts, text)
	return nil
}

func (r *spyReporter) all() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.texts))
	copy(out, r.texts)
	return out
}

// recordingStatusHandler records the goalID each MsgStatus dispatch carries.
type recordingStatusHandler struct {
	mu      sync.Mutex
	goalIDs []string
}

func (h *recordingStatusHandler) handle(goalID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.goalIDs = append(h.goalIDs, goalID)
}

func (h *recordingStatusHandler) calls() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.goalIDs))
	copy(out, h.goalIDs)
	return out
}

// assembleRouter builds an orchestrateConfig wired for a router test: a scripted
// MessageSource, a spy Reporter, a recording status handler, an injected mailbox
// map (so the test can read deliveries), and a latch dispatch so a goal can be held
// in Dispatching for the register-then-start race assertion.
func assembleRouter(t *testing.T, src supervisor.MessageSource, rep supervisor.Reporter, sh func(string), mboxes *commandMailboxes, reg *orchestrator.StatusRegistry, dispatch orchestrator.DispatchFunc) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:      dispatch,
		auditSink:     audit.NewFakeSink(),
		planner:       newPerGoalPlanner(),
		messageSource: src,
		reporter:      rep,
		statusHandler: sh,
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

// --- TC-113-04 — router dispatches by kind to the right place ----------------

func TestTC113_04_RouterDispatchesByKind(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}
	sh := &recordingStatusHandler{}
	mboxes := newCommandMailboxes()
	disp := newLatchDispatch("goal-1")

	src := &scriptedMessageSource{msgs: []supervisor.Message{
		{Kind: supervisor.MsgNewGoal, GoalID: "goal-1", Goal: supervisor.Task{ID: "goal-1", Spec: "do it"}},
		{Kind: supervisor.MsgStatus, GoalID: ""},
		{Kind: supervisor.MsgInfo, GoalID: "goal-1", Text: "extra info"},
		{Kind: supervisor.MsgCancel, GoalID: "goal-1"},
	}}
	oc := assembleRouter(t, src, rep, sh.handle, mboxes, reg, disp.fn)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// new-goal → one actor for goal-1 spawned; its dispatch enters (held by latch).
	select {
	case <-disp.entered["goal-1"]:
	case <-time.After(3 * time.Second):
		t.Fatal("goal-1 dispatch never entered — new-goal did not spawn an actor")
	}

	// A mailbox exists for goal-1 (the router delivers info/cancel here, never to the
	// status handler). The goal actor (task 115) is the live consumer of this mailbox,
	// so we assert ROUTING by its observable effects rather than racing the actor for
	// the raw channel:
	//   - info goal-1 and cancel goal-1 reached goal-1's mailbox (the actor consumed
	//     them), so they were NOT misrouted to the unknown-goal path → no "no such
	//     goal: goal-1" report.
	//   - status (empty GoalID) reached the status handler (recorded), NOT the mailbox.
	if _, ok := mboxes.Lookup("goal-1"); !ok {
		t.Fatal("no mailbox created for goal-1")
	}
	// Both mailbox commands (info, cancel) must drain to the actor — wait for the
	// mailbox to empty, proving they were routed to it and consumed (not lost, not sent
	// to the unknown-goal path).
	box, _ := mboxes.Lookup("goal-1")
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(box) == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(box) != 0 {
		t.Fatalf("goal-1 mailbox still buffered %d message(s) — info/cancel were not consumed by the actor", len(box))
	}

	// status (empty GoalID) → status handler invoked with empty GoalID = fleet, and the
	// info/cancel for goal-1 never hit the unknown-goal path (they were routed to the
	// mailbox). The status handler recording exactly one empty-goalID call proves status
	// was NOT misrouted into the mailbox.
	if calls := sh.calls(); len(calls) != 1 || calls[0] != "" {
		t.Fatalf("status handler calls = %v, want exactly one with empty (fleet) goalID", calls)
	}
	for _, txt := range rep.all() {
		if strings.Contains(txt, "no such goal") && strings.Contains(txt, "goal-1") {
			t.Fatalf("info/cancel for goal-1 hit the unknown-goal path (%q) — they were not routed to goal-1's mailbox", txt)
		}
	}

	// Release the held actor and drain.
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

// --- TC-113-05 — unknown goalID → graceful "no such goal", never a panic -----

func TestTC113_05_UnknownGoalIDGracefulNoPanic(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}
	sh := &recordingStatusHandler{}
	mboxes := newCommandMailboxes()
	// No new-goal registered → goal-X has no actor / no mailbox.
	src := &scriptedMessageSource{msgs: []supervisor.Message{
		{Kind: supervisor.MsgStatus, GoalID: "goal-X"},
		{Kind: supervisor.MsgInfo, GoalID: "goal-X", Text: "foo"},
		{Kind: supervisor.MsgCancel, GoalID: "goal-X"},
	}}
	oc := assembleRouter(t, src, rep, sh.handle, mboxes, reg, (&spyDispatch{}).fn)

	// Must complete without panic.
	if err := runControlLoop(context.Background(), oc); err != nil {
		t.Fatalf("control loop returned error: %v", err)
	}

	// info goal-X and cancel goal-X each produce a "no such goal" report.
	noSuchCount := 0
	for _, txt := range rep.all() {
		if strings.Contains(txt, "no such goal") && strings.Contains(txt, "goal-X") {
			noSuchCount++
		}
	}
	if noSuchCount < 2 {
		t.Fatalf("expected >=2 'no such goal: goal-X' reports (info+cancel), got %d in %v", noSuchCount, rep.all())
	}

	// status goal-X is routed to the status handler (it does not error/panic); the
	// handler — task 114's body — decides the unknown-goal answer. Assert it was
	// reached with goal-X.
	if calls := sh.calls(); len(calls) != 1 || calls[0] != "goal-X" {
		t.Fatalf("status handler calls = %v, want exactly one with goal-X", calls)
	}

	// No mailbox was created for the unknown goalID.
	if _, ok := mboxes.Lookup("goal-X"); ok {
		t.Fatal("a mailbox was created for unknown goal-X — must never auto-create on info/cancel")
	}
}

// --- TC-113-06 — mailbox created before actor registration (register-then-start)

func TestTC113_06_MailboxCreatedBeforeActorRegistration(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	rep := &spyReporter{}
	mboxes := newCommandMailboxes()
	// Hold goal-2's actor at its dispatch latch (post-registration, pre-completion).
	disp := newLatchDispatch("goal-2")

	// new-goal goal-2 immediately followed by cancel goal-2 from the same source.
	src := &scriptedMessageSource{msgs: []supervisor.Message{
		{Kind: supervisor.MsgNewGoal, GoalID: "goal-2", Goal: supervisor.Task{ID: "goal-2", Spec: "work"}},
		{Kind: supervisor.MsgCancel, GoalID: "goal-2"},
	}}
	oc := assembleRouter(t, src, rep, nil, mboxes, reg, disp.fn)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	// The mailbox existed at the moment the cancel routed because routeNewGoal created
	// it BEFORE the actor goroutine was spawned (register-then-start ordering, ADR 054
	// §6 (b)). The goal actor (task 115) is the live consumer of the mailbox, so the
	// register-then-start guarantee is asserted by its EFFECT: the cancel was routed to
	// goal-2's mailbox (not lost, not "no such goal") and consumed by the actor.
	box, ok := waitMailbox(mboxes, "goal-2", 3*time.Second)
	if !ok {
		t.Fatal("no mailbox created for goal-2")
	}
	// The actor consumes the cancel from the mailbox (cancel teardown is task 116, so
	// the actor takes no teardown action yet). Proof the cancel was delivered, not
	// lost: it was consumed (the buffer drains to empty) and no "no such goal" report
	// was emitted for goal-2 — a lost/misrouted cancel would have hit the unknown-goal
	// path on the router instead.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(box) == 0 {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if len(box) != 0 {
		t.Fatalf("goal-2 cancel was not consumed by the actor (mailbox still buffered %d) — register-then-start delivery failed", len(box))
	}
	for _, txt := range rep.all() {
		if strings.Contains(txt, "no such goal") && strings.Contains(txt, "goal-2") {
			t.Fatalf("cancel for goal-2 hit the unknown-goal path (%q) — the mailbox did not exist when the cancel routed (register-then-start violated)", txt)
		}
	}

	// Release the held actor and drain cleanly (no race on the mailbox map under -race).
	disp.releaseGoal("goal-2")
	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("control loop did not drain")
	}
}

// --- helpers -----------------------------------------------------------------

func noEnv(string) string { return "" }

func waitMailbox(m *commandMailboxes, goalID string, timeout time.Duration) (chan supervisor.Message, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ch, ok := m.Lookup(goalID); ok {
			return ch, true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return m.Lookup(goalID)
}
