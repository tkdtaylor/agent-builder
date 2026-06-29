package orchestrator

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// scopingPolicy is a PolicyClient that also implements PlanScoper, recording the
// plan it was configured with. This proves the LIVE Handle path calls
// ConfigureForPlan before issuing the plan's decisions (ADR 055 seam 1, task 122) —
// the producer→consumer wire: orchestrator (producer of "this plan") → PlanScoper
// (consumer that feeds the daemon's --allow).
type scopingPolicy struct {
	configured  []Plan
	decision    policy.Decision
	configErr   error
	decideCalls int
}

func (p *scopingPolicy) ConfigureForPlan(plan Plan) error {
	p.configured = append(p.configured, plan)
	return p.configErr
}

func (p *scopingPolicy) Decide(policy.DecideRequest) (policy.DecideResponse, error) {
	p.decideCalls++
	return policy.DecideResponse{Decision: p.decision}, nil
}

// scopePlanner returns a fixed plan seeded with the goal's ID/Spec from Plan(goal),
// so the GoalID (and therefore the plan-derived allow set) is the live goal's ID.
type scopePlanner struct{ plan Plan }

func (s scopePlanner) Plan(goal supervisor.Task) (Plan, error) {
	pl := s.plan
	pl.Goal = goal.Spec
	pl.GoalID = goal.ID
	return pl, nil
}

// TC-001 (live wire): Handle calls ConfigureForPlan with the admitted plan BEFORE
// issuing any of its decisions. The consumer (PlanScoper) thus receives the plan
// whose AllowedResources feed the daemon's --allow.
func TestHandleConfiguresPolicyForPlanBeforeDeciding(t *testing.T) {
	pol := &scopingPolicy{decision: policy.DecisionAllow}
	plan := Plan{SubGoals: []SubGoal{{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g-0"}}}}
	o := New(scopePlanner{plan: plan}, pol, nopReporter{}, runtime.Config{},
		WithDispatchFunc(func(context.Context, SubGoal, runtime.Config) error { return nil }))

	_, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "do a thing"})
	if err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(pol.configured) != 1 {
		t.Fatalf("ConfigureForPlan called %d times, want 1", len(pol.configured))
	}
	if got := pol.configured[0].GoalID; got != "g" {
		t.Fatalf("ConfigureForPlan plan.GoalID = %q, want g", got)
	}
	// The configured plan derives the resource set fed to the daemon's --allow.
	want := map[string]bool{"g": true, "coding-agent": true, "g-0": true}
	for _, r := range pol.configured[0].AllowedResources() {
		if !want[r] {
			t.Errorf("unexpected resource %q in configured plan's allow set", r)
		}
	}
}

// TC-004 (fail-closed): a ConfigureForPlan error fails the goal and dispatches
// nothing — the daemon could not be scoped to the plan, so no decision is issued.
func TestHandleFailsClosedWhenConfigureForPlanErrors(t *testing.T) {
	pol := &scopingPolicy{decision: policy.DecisionAllow, configErr: context.DeadlineExceeded}
	plan := Plan{SubGoals: []SubGoal{{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g-0"}}}}
	dispatched := 0
	o := New(scopePlanner{plan: plan}, pol, nopReporter{}, runtime.Config{},
		WithDispatchFunc(func(context.Context, SubGoal, runtime.Config) error { dispatched++; return nil }))

	_, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "do a thing"})
	if err == nil {
		t.Fatal("Handle returned nil error, want a configure-policy failure (fail-closed)")
	}
	if pol.decideCalls != 0 {
		t.Fatalf("policy decided %d times after a config failure, want 0 (no decision issued)", pol.decideCalls)
	}
	if dispatched != 0 {
		t.Fatalf("dispatched %d workers after a config failure, want 0", dispatched)
	}
}
