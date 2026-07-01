package cli

// Tests for task 147 — the StateConversing linger terminates on source EOF
// (shutdown-close), not only on context cancellation (ADR 060 §6, ADR 054).
//
//   TC-147-01 — TestConversingGoalEndsOnShutdown: shutdown closes (ctx NOT
//               cancelled) → the actor's run returns and the goal reaches StateDone.
//   TC-147-02 — TestControlLoopReturnsOnFiniteSourceAfterAnswer: the full control
//               loop over a finite envMessageSource returns on its own after an
//               answer, with no cancellation.
//   TC-147-03 — TestConversingGoalStillEndsOnCancel: regression — shutdown OPEN,
//               ctx cancelled → the conversing goal still reaches StateDone.

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// answerStub is a minimal orchestrator.Answerer returning a canned reply, used to
// drive a goal into StateConversing deterministically (mirrors echoAnswerer in
// internal/orchestrator/answer_route_test.go).
type answerStub struct{ reply string }

func (a answerStub) Answer(_ context.Context, _ string, _ int) (string, error) {
	return a.reply, nil
}

// answerKindAnalyzer classifies every goal as KindAnswer/ComplexitySimple (mirrors
// fakeAnalyzer in internal/orchestrator/answer_route_test.go).
type answerKindAnalyzer struct{}

func (answerKindAnalyzer) Analyze(supervisor.Task) (orchestrator.GoalAnalysis, error) {
	return orchestrator.GoalAnalysis{Kind: orchestrator.KindAnswer, Complexity: orchestrator.ComplexitySimple}, nil
}

// newAnswerOrchestrateConfig builds an orchestrateConfig wired directly with
// orchestrator.New (bypassing assembleOrchestrate's hardcoded cliAnswerer{}) so the
// test can inject an Answerer + GoalAnalyzer that deterministically drives a goal
// into StateConversing (ADR 060 §6). Reuses the package's recordingReporter (task
// 120, orchestrate_120_test.go).
func newAnswerOrchestrateConfig(reg *orchestrator.StatusRegistry, reply string) orchestrateConfig {
	rep := &recordingReporter{}
	orch := orchestrator.New(nil, nil, rep, runtime.Config{},
		orchestrator.WithStatusRegistry(reg),
		orchestrator.WithGoalAnalyzer(answerKindAnalyzer{}),
		orchestrator.WithAnswerer(answerStub{reply: reply}),
	)
	return orchestrateConfig{
		orch:     orch,
		reporter: rep,
		registry: reg,
		maxGoals: 8,
	}
}

// TC-147-01: with ctx NOT cancelled, closing the shutdown channel alone must end a
// StateConversing goal — the actor's run returns and the goal reaches StateDone.
// Non-vacuity (verified by mutation, see task 147 report): reverting the linger to
// the old `<-ctx.Done()`-only code makes this test time out/fail; the `select`
// fix makes it pass.
func TestConversingGoalEndsOnShutdown(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	oc := newAnswerOrchestrateConfig(reg, "Paris")

	mboxes := newCommandMailboxes()
	mailbox := mboxes.Create("c1")
	admitChan := make(chan struct{}, 8)
	var wg sync.WaitGroup
	shutdownChan := make(chan struct{})

	actor := &goalActor{
		oc:        oc,
		goal:      supervisor.Task{ID: "c1", Spec: "What is the capital of France?"},
		mailbox:   mailbox,
		mailboxes: mboxes,
		admit:     admitChan,
		wg:        &wg,
		shutdown:  shutdownChan,
	}

	// ctx is deliberately never cancelled in this test.
	ctx := context.Background()

	actorDone := make(chan struct{})
	go func() {
		actor.run(ctx)
		close(actorDone)
	}()

	if st := waitState(t, reg, "c1", orchestrator.StateConversing, 3*time.Second); st != orchestrator.StateConversing {
		t.Fatalf("goal state = %v, want StateConversing before closing shutdown", st)
	}

	// Close shutdown ONLY (source exhausted); ctx stays live.
	close(shutdownChan)

	select {
	case <-actorDone:
	case <-time.After(3 * time.Second):
		t.Fatal("actor.run did not return after shutdown closed (ctx not cancelled) — the linger is still blocking on ctx.Done() only")
	}

	if st, _ := reg.Get("c1"); st.State != orchestrator.StateDone {
		t.Fatalf("goal state after shutdown = %v, want StateDone", st.State)
	}
}

// TC-147-03 (regression): with shutdown OPEN (source still live) and ctx cancelled,
// the conversing goal must still end (StateDone) — the pre-147 cancel path is
// preserved by the select.
func TestConversingGoalStillEndsOnCancel(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	oc := newAnswerOrchestrateConfig(reg, "Paris")

	mboxes := newCommandMailboxes()
	mailbox := mboxes.Create("c1")
	admitChan := make(chan struct{}, 8)
	var wg sync.WaitGroup
	shutdownChan := make(chan struct{}) // left OPEN for the whole test

	actor := &goalActor{
		oc:        oc,
		goal:      supervisor.Task{ID: "c1", Spec: "What is the capital of France?"},
		mailbox:   mailbox,
		mailboxes: mboxes,
		admit:     admitChan,
		wg:        &wg,
		shutdown:  shutdownChan,
	}

	ctx, cancel := context.WithCancel(context.Background())

	actorDone := make(chan struct{})
	go func() {
		actor.run(ctx)
		close(actorDone)
	}()

	if st := waitState(t, reg, "c1", orchestrator.StateConversing, 3*time.Second); st != orchestrator.StateConversing {
		t.Fatalf("goal state = %v, want StateConversing before cancel", st)
	}

	cancel() // shutdownChan remains open — only ctx is cancelled.

	select {
	case <-actorDone:
	case <-time.After(3 * time.Second):
		t.Fatal("actor.run did not return after ctx cancel with shutdown open — cancel path regressed")
	}

	if st, _ := reg.Get("c1"); st.State != orchestrator.StateDone {
		t.Fatalf("goal state after cancel = %v, want StateDone", st.State)
	}
}

// TC-147-02: the full orchestrate control loop, over a FINITE envMessageSource
// (AGENT_BUILDER_GOAL_SPEC set, empty/EOF stdin) with an injected answerer+analyzer
// that yields a KindAnswer -> StateConversing goal, returns on its own once the
// source drains — no external cancellation. This is the producer -> consumer proof
// that source-drain alone unblocks the whole process (the L6 scenario from task 146
// OBS B), not just the isolated actor.
func TestControlLoopReturnsOnFiniteSourceAfterAnswer(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	oc := newAnswerOrchestrateConfig(reg, "Paris")

	// Finite source: one env-delivered goal, then EOF (empty stdin reader).
	src := newEnvMessageSource(func(key string) string {
		switch key {
		case EnvGoalSpec:
			return "What is the capital of France?"
		case EnvGoalID:
			return "c1"
		}
		return ""
	}, strings.NewReader(""))
	oc.source = src

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	select {
	case err := <-loopDone:
		if err != nil {
			t.Fatalf("control loop returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control loop did not return after the finite source drained — conversing goal never unblocked (task 146 OBS B regression)")
	}

	if st, _ := reg.Get("c1"); st.State != orchestrator.StateDone {
		t.Fatalf("goal state after control loop returned = %v, want StateDone", st.State)
	}
}
