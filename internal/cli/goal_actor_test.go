package cli

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

// spyClarifier counts Clarify calls and allows configuring returns.
type spyClarifier struct {
	ready     bool
	questions []string
	calls     int
}

func (s *spyClarifier) Clarify(goal supervisor.Task) (orchestrator.Clarification, error) {
	s.calls++
	return orchestrator.Clarification{Ready: s.ready, Questions: s.questions}, nil
}

// TC-128-06: Clarifying linger loop drains the mailbox and exits on MsgConfirm
func TestTC128_06_ClarifyingLingerLoop(t *testing.T) {
	reg := orchestrator.NewStatusRegistry()
	mboxes := newCommandMailboxes()

	clar := &spyClarifier{
		ready:     false,
		questions: []string{"Which repository?"},
	}

	getenv := func(key string) string {
		if key == EnvIntake {
			return ""
		}
		if key == EnvRequireApproval {
			return "false"
		}
		return ""
	}

	src := newGatedMessageSource()
	setBaseConfigEnv(t)

	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:      func(ctx context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error { return nil },
		auditSink:     audit.NewFakeSink(),
		planner:       newPerGoalPlanner(),
		messageSource: src,
		signingKey:    testSigningKey(t),
		registry:      reg,
		maxWorkers:    4,
		maxGoals:      8,
		clarifier:     clar,
		getenv:        getenv,
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)

	loopDone := make(chan error, 1)
	go func() { loopDone <- runControlLoop(context.Background(), oc) }()

	reg.Register("goal-1", orchestrator.StateQueued)
	mailbox := mboxes.Create("goal-1")
	admitChan := make(chan struct{}, 8)
	var wg sync.WaitGroup
	shutdownChan := make(chan struct{})

	actor := &goalActor{
		oc:        oc,
		goal:      supervisor.Task{ID: "goal-1", Spec: "fix bug"},
		mailbox:   mailbox,
		mailboxes: mboxes,
		admit:     admitChan,
		wg:        &wg,
		shutdown:  shutdownChan,
	}
	
	actorDone := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		actor.run(ctx)
		close(actorDone)
	}()

	if st := waitState(t, reg, "goal-1", orchestrator.StateClarifying, 3*time.Second); st != orchestrator.StateClarifying {
		t.Fatalf("expected goal state StateClarifying, got %v", st)
	}

	if clar.calls != 1 {
		t.Fatalf("expected clarifier to be called 1 time, got %d", clar.calls)
	}

	mboxes.deliver(supervisor.Message{Kind: supervisor.MsgInfo, GoalID: "goal-1", Text: "repo: github.com/tkdtaylor/exec-sandbox"})

	clar.ready = true
	clar.questions = nil

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && clar.calls < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if clar.calls != 2 {
		t.Fatalf("expected clarifier to be called 2 times after info, got %d", clar.calls)
	}

	if !strings.Contains(actor.goal.Spec, "github.com/tkdtaylor/exec-sandbox") {
		t.Errorf("expected goal spec to contain folded info, got %q", actor.goal.Spec)
	}

	mboxes.deliver(supervisor.Message{Kind: supervisor.MsgConfirm, GoalID: "goal-1"})

	select {
	case <-actorDone:
	case <-time.After(3 * time.Second):
		t.Fatal("actor did not exit after MsgConfirm")
	}

	st, _ := reg.Get("goal-1")
	if st.State == orchestrator.StateClarifying {
		t.Errorf("goal is still in StateClarifying after MsgConfirm")
	}
}
