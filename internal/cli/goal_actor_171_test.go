package cli

// Task 171 TC-171-08: goalActor.handleCommand routes MsgApprove/MsgDeny to
// Orchestrator.ResumeApproval. Driven through a real Orchestrator with a RunStore
// and a seeded pending approval; the recording dispatch proves the resume fired.

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

type cli171Planner struct{}

func (cli171Planner) Plan(g supervisor.Task) (orchestrator.Plan, error) {
	return orchestrator.Plan{GoalID: g.ID, Goal: g.Spec}, nil
}

type cli171Policy struct{}

func (cli171Policy) Decide(policy.DecideRequest) (policy.DecideResponse, error) {
	return policy.DecideResponse{Decision: policy.DecisionAllow}, nil
}

type cli171Reporter struct{}

func (cli171Reporter) Report(context.Context, string) error { return nil }

type cli171Dispatch struct {
	mu  sync.Mutex
	ids []string
}

func (d *cli171Dispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	d.mu.Lock()
	d.ids = append(d.ids, sub.Task.ID)
	d.mu.Unlock()
	return nil
}

func (d *cli171Dispatch) count(id string) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, x := range d.ids {
		if x == id {
			n++
		}
	}
	return n
}

func TestTC171_08_GoalActorRoutesApproveToResumeApproval(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	// Seed a paused sub-goal (task-3) for goal-7.
	planJSON, _ := json.Marshal(orchestrator.Plan{
		GoalID: "goal-7", Goal: "goal-7",
		SubGoals: []orchestrator.SubGoal{{RecipeName: orchestrator.DefaultRecipeName, Task: supervisor.Task{ID: "task-3", Spec: "do task-3"}}},
	})
	if err := store.Save(runstore.Record{
		GoalID:  "goal-7",
		Plan:    planJSON,
		Pending: []runstore.PendingApproval{{TaskID: "task-3", Reason: "policy: requires human approval"}},
		Status:  runstore.StatusAwaitingApproval,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	disp := &cli171Dispatch{}
	orch := orchestrator.New(
		cli171Planner{}, cli171Policy{}, cli171Reporter{}, runtimewiring.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc(disp.fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)

	actor := &goalActor{
		oc:   orchestrateConfig{orch: orch, reporter: cli171Reporter{}},
		goal: supervisor.Task{ID: "goal-7", Spec: "goal-7"},
	}

	// Route MsgApprove through handleCommand → applyApproval → ResumeApproval(true).
	actor.handleCommand(context.Background(), supervisor.Message{
		Kind: supervisor.MsgApprove, GoalID: "goal-7", TaskID: "task-3",
	}, nil)

	if disp.count("task-3") != 1 {
		t.Fatalf("task-3 dispatched %d times, want 1 (goalActor routed approve to ResumeApproval)", disp.count("task-3"))
	}
	rec, _, _ := store.Load("goal-7")
	if len(rec.Pending) != 0 {
		t.Errorf("Pending = %+v, want empty after approve", rec.Pending)
	}
}

type capturingCli171Reporter struct {
	mu   sync.Mutex
	msgs []string
}

func (r *capturingCli171Reporter) Report(_ context.Context, s string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, s)
	return nil
}

// TC-171-07 (live wiring): handleCommand triggers SweepApprovalTimeouts on the live
// command-drain path, so an overdue pending approval is escalated over the
// orchestrator's Reporter without a direct test call to the sweep. This closes the
// dead-wire: the sweep has a real caller.
func TestTC171_07_HandleCommandTriggersTimeoutSweep(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	planJSON, _ := json.Marshal(orchestrator.Plan{GoalID: "goal-7", SubGoals: []orchestrator.SubGoal{{RecipeName: orchestrator.DefaultRecipeName, Task: supervisor.Task{ID: "task-3"}}}})
	if err := store.Save(runstore.Record{
		GoalID:  "goal-7",
		Plan:    planJSON,
		Pending: []runstore.PendingApproval{{TaskID: "task-3", Reason: "x", RequestedAt: time.Now().Add(-2 * time.Hour).UTC()}},
		Status:  runstore.StatusAwaitingApproval,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rep := &capturingCli171Reporter{}
	orch := orchestrator.New(
		cli171Planner{}, cli171Policy{}, rep, runtimewiring.Config{},
		orchestrator.WithRunStore(store),
		orchestrator.WithDispatchFunc((&cli171Dispatch{}).fn),
		orchestrator.WithRequireApproval(false),
		orchestrator.WithAuditSink(audit.NewFakeSink()),
	)
	actor := &goalActor{
		oc:   orchestrateConfig{orch: orch, reporter: rep, approvalTimeout: time.Hour},
		goal: supervisor.Task{ID: "goal-7"},
	}

	// A status command does nothing in the switch, but the post-switch sweep runs.
	actor.handleCommand(context.Background(), supervisor.Message{Kind: supervisor.MsgStatus, GoalID: "goal-7"}, nil)

	rep.mu.Lock()
	defer rep.mu.Unlock()
	found := false
	for _, m := range rep.msgs {
		if strings.Contains(m, "timeout") && strings.Contains(m, "task-3") {
			found = true
		}
	}
	if !found {
		t.Fatalf("handleCommand did not trigger a live timeout escalation; msgs=%v", rep.msgs)
	}
}
