package orchestrator

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// Task 121 orchestrator-side wire (ADR 055 seam 4): the live dispatch path PRODUCES
// a typed BlockedAction on a necessary-action deny, and ReevaluateBlockedSpawn
// CONSUMES it — re-deriving the plan + its allow set, then escalating. These tests
// close the producer→consumer trace on the live path (not a hand-set field).

// denyingPolicy denies every spawn-worker decision (the spawn-plan is allowed so the
// plan reaches dispatch). It models a deployment policy that refuses the recipe.
type denyingPolicy struct{}

func (denyingPolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	if req.Action.Name == SpawnAction {
		return policy.DecideResponse{Decision: policy.DecisionAllow}, nil
	}
	return policy.DecideResponse{Decision: policy.DecisionDeny}, nil
}

// nopReporter discards every report.
type nopReporter struct{}

func (nopReporter) Report(context.Context, string) error { return nil }

// memWriter is an in-memory StatusWriter capturing needs-human writes.
type memWriter struct {
	writes []string
}

func (w *memWriter) WriteStatus(taskID string, status tasksource.WritableStatus) (tasksource.StatusWriteResult, error) {
	w.writes = append(w.writes, taskID+":"+string(status))
	return tasksource.StatusWriteResult{Path: "docs/tasks/backlog/" + taskID + ".md", Changed: true}, nil
}

// fixedPlanner returns the same plan for any goal (so the re-derived plan is
// deterministic and STILL needs the denied recipe).
type fixedPlanner struct{ plan Plan }

func (p fixedPlanner) Plan(_ supervisor.Task) (Plan, error) { return p.plan, nil }

// rerouter returns a plan WITHOUT the denied recipe on replan (routes around).
type rerouter struct{ plan Plan }

func (p rerouter) Plan(_ supervisor.Task) (Plan, error) { return p.plan, nil }

func dispatchNoop(context.Context, SubGoal, runtime.Config) error { return nil }

// TC-121-PROD: the live dispatchOne PRODUCES a typed BlockedAction outcome on a
// necessary-action deny — distinct from a dispatch error / gate failure.
func TestDispatchOneProducesBlockedActionOnSpawnDeny(t *testing.T) {
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}
	o := New(
		fixedPlanner{plan: plan},
		denyingPolicy{},
		nopReporter{},
		runtime.Config{},
		WithDispatchFunc(dispatchNoop),
		WithAuditSink(audit.NewFakeSink()),
	)

	outcome, auditErr := o.dispatchOne(context.Background(), plan, plan.SubGoals[0])
	if auditErr != nil {
		t.Fatalf("TC-121-PROD dispatchOne audit error = %v", auditErr)
	}
	if outcome.Success {
		t.Fatalf("TC-121-PROD denied spawn must not succeed")
	}
	if outcome.Blocked == nil {
		t.Fatalf("TC-121-PROD outcome.Blocked = nil, want a typed BlockedAction on the live deny path")
	}
	if outcome.Blocked.Resource != "coding-agent" {
		t.Fatalf("TC-121-PROD Blocked.Resource = %q, want coding-agent", outcome.Blocked.Resource)
	}
	if outcome.Blocked.Action != SpawnWorkerAction {
		t.Fatalf("TC-121-PROD Blocked.Action = %q, want %q", outcome.Blocked.Action, SpawnWorkerAction)
	}
	if outcome.Blocked.Reason == "" {
		t.Fatalf("TC-121-PROD Blocked.Reason is empty, want the deny reason")
	}
}

// TC-121-CONS-escalate: ReevaluateBlockedSpawn CONSUMES the blocked action and, when
// the re-derived plan STILL needs the denied recipe, escalates to needs-human after
// exactly N replans — carrying the denied action + reason.
func TestReevaluateBlockedSpawnEscalatesWhenStillNeeded(t *testing.T) {
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "coding-agent", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}
	o := New(fixedPlanner{plan: plan}, denyingPolicy{}, nopReporter{}, runtime.Config{}, WithDispatchFunc(dispatchNoop))

	goal := supervisor.Task{ID: "goal-1", Spec: "goal text"}
	blocked := loop.BlockedAction{Resource: "coding-agent", Action: SpawnWorkerAction, Reason: "policy: worker spawn denied"}
	writer := &memWriter{}

	outcome, err := o.ReevaluateBlockedSpawn(goal, blocked, 3, writer)
	if err != nil {
		t.Fatalf("TC-121-CONS ReevaluateBlockedSpawn error = %v", err)
	}
	if outcome.Kind != loop.ReevaluationEscalated {
		t.Fatalf("TC-121-CONS Kind = %q, want %q", outcome.Kind, loop.ReevaluationEscalated)
	}
	if outcome.Reevaluations != 3 {
		t.Fatalf("TC-121-CONS Reevaluations = %d, want 3", outcome.Reevaluations)
	}
	if len(writer.writes) != 1 || writer.writes[0] != "goal-1:needs-human" {
		t.Fatalf("TC-121-CONS writes = %v, want [goal-1:needs-human]", writer.writes)
	}
	if outcome.Escalation.Blocked != blocked {
		t.Fatalf("TC-121-CONS escalation does not carry the denied action: %+v", outcome.Escalation.Blocked)
	}
}

// TC-121-CONS-resolve: when the re-derived plan ROUTES AROUND the denial (the new
// plan no longer needs the denied recipe), reevaluation resolves WITHOUT escalation
// and WITHOUT granting — the applied allow set is the re-derived plan's set, which
// excludes the denied resource.
func TestReevaluateBlockedSpawnResolvesWhenReplanRoutesAround(t *testing.T) {
	// Re-derived plan uses a DIFFERENT recipe — does not need the denied "coding-agent".
	plan := Plan{
		Goal:   "goal text",
		GoalID: "goal-1",
		SubGoals: []SubGoal{
			{RecipeName: "docs-fix", Task: supervisor.Task{ID: "goal-1-0", Spec: "do work"}},
		},
	}
	o := New(rerouter{plan: plan}, denyingPolicy{}, nopReporter{}, runtime.Config{}, WithDispatchFunc(dispatchNoop))

	goal := supervisor.Task{ID: "goal-1", Spec: "goal text"}
	blocked := loop.BlockedAction{Resource: "coding-agent", Action: SpawnWorkerAction, Reason: "policy: worker spawn denied"}
	writer := &memWriter{}

	outcome, err := o.ReevaluateBlockedSpawn(goal, blocked, 3, writer)
	if err != nil {
		t.Fatalf("TC-121-CONS ReevaluateBlockedSpawn error = %v", err)
	}
	if outcome.Kind != loop.ReevaluationResolved {
		t.Fatalf("TC-121-CONS Kind = %q, want %q", outcome.Kind, loop.ReevaluationResolved)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("TC-121-CONS writes = %v, want none (resolved without escalation)", writer.writes)
	}
	// Never-self-grant: the applied allow set is the re-derived plan's set, which does
	// NOT contain the denied resource.
	for _, r := range outcome.AllowedResources {
		if r == blocked.Resource {
			t.Fatalf("TC-121-CONS SELF-GRANT: denied resource %q in re-derived allow set %v", blocked.Resource, outcome.AllowedResources)
		}
	}
	if len(outcome.AllowedResources) == 0 {
		t.Fatalf("TC-121-CONS re-derived allow set is empty, want the new plan's resources")
	}
}
