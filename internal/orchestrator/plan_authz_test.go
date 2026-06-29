package orchestrator

import (
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// recordingAuthzPolicy is a white-box fake PolicyClient: it records every decide
// call and returns a scripted decision, so a test can assert BOTH the decision and
// whether the policy engine was consulted at all (the plan-derived gate must
// short-circuit before policy for out-of-plan resources).
type recordingAuthzPolicy struct {
	mu       sync.Mutex
	requests []policy.DecideRequest
	decision policy.Decision
}

func (p *recordingAuthzPolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	return policy.DecideResponse{Decision: p.decision}, nil
}

func (p *recordingAuthzPolicy) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.requests)
}

func sub(recipe, taskID string) SubGoal {
	return SubGoal{RecipeName: recipe, Task: supervisor.Task{ID: taskID}}
}

// TC-001: AllowedResources derives {GoalID} ∪ {recipe names} ∪ {task IDs}.
func TestAllowedResourcesDerivesPlanSet(t *testing.T) {
	plan := Plan{GoalID: "goal-1", SubGoals: []SubGoal{
		sub("coding-agent", "goal-1-0"),
		sub("docs-fix", "goal-1-1"),
	}}
	got := plan.AllowedResources()
	want := map[string]bool{"goal-1": true, "coding-agent": true, "docs-fix": true, "goal-1-0": true, "goal-1-1": true}
	if len(got) != len(want) {
		t.Fatalf("AllowedResources() = %v (len %d), want %d distinct", got, len(got), len(want))
	}
	for _, id := range got {
		if !want[id] {
			t.Errorf("unexpected resource %q in %v", id, got)
		}
	}
	if got[0] != "goal-1" {
		t.Errorf("goal ID must come first; got[0] = %q", got[0])
	}
}

// TC-002: repeated recipe names are deduped.
func TestAllowedResourcesDedupsRecipes(t *testing.T) {
	plan := Plan{GoalID: "goal-2", SubGoals: []SubGoal{
		sub("coding-agent", "goal-2-0"),
		sub("coding-agent", "goal-2-1"),
	}}
	got := plan.AllowedResources()
	if len(got) != 4 {
		t.Fatalf("AllowedResources() = %v (len %d), want 4 (goal-2, coding-agent, goal-2-0, goal-2-1)", got, len(got))
	}
	count := 0
	for _, id := range got {
		if id == "coding-agent" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("coding-agent appears %d times, want exactly 1", count)
	}
}

// TC-003: an in-plan spawn-worker resource proceeds to policy and is allowed.
func TestDecideSpawnWorkerInPlanConsultsPolicyAllow(t *testing.T) {
	pol := &recordingAuthzPolicy{decision: policy.DecisionAllow}
	o := &Orchestrator{policy: pol}
	plan := Plan{GoalID: "goal-3", SubGoals: []SubGoal{sub("coding-agent", "goal-3-0")}}

	decision, reason := o.decideSpawnWorker(plan, plan.SubGoals[0])
	if decision != policy.DecisionAllow {
		t.Fatalf("decision = %v (%s), want allow", decision, reason)
	}
	if pol.calls() != 1 {
		t.Fatalf("policy consulted %d times, want 1", pol.calls())
	}
	if got := pol.requests[0].Resource.ID; got != "coding-agent" {
		t.Errorf("policy resource ID = %q, want coding-agent", got)
	}
}

// TC-004: an out-of-plan resource is denied WITHOUT consulting policy.
func TestDecideSpawnWorkerOutOfPlanDeniedWithoutPolicy(t *testing.T) {
	pol := &recordingAuthzPolicy{decision: policy.DecisionAllow} // would allow if consulted
	o := &Orchestrator{policy: pol}
	plan := Plan{GoalID: "goal-4", SubGoals: []SubGoal{sub("coding-agent", "goal-4-0")}}

	// A foreign sub-goal whose recipe + task are not in the plan's derived set.
	foreign := sub("rogue-recipe", "goal-4-99")
	decision, reason := o.decideSpawnWorker(plan, foreign)
	if decision != policy.DecisionDeny {
		t.Fatalf("decision = %v, want deny for out-of-plan resource", decision)
	}
	if pol.calls() != 0 {
		t.Fatalf("policy consulted %d times for an out-of-plan resource, want 0 (gate must short-circuit)", pol.calls())
	}
	if reason == "" {
		t.Error("expected a deny reason naming the out-of-plan resource")
	}
}

// TC-005: effective decision is plan-allows ∧ policy-allows — an in-plan resource the
// policy engine denies is still denied (policy remains the independent ceiling).
func TestDecideSpawnWorkerInPlanPolicyDenyIsDeny(t *testing.T) {
	pol := &recordingAuthzPolicy{decision: policy.DecisionDeny}
	o := &Orchestrator{policy: pol}
	plan := Plan{GoalID: "goal-5", SubGoals: []SubGoal{sub("coding-agent", "goal-5-0")}}

	decision, _ := o.decideSpawnWorker(plan, plan.SubGoals[0])
	if decision != policy.DecisionDeny {
		t.Fatalf("decision = %v, want deny (policy is the independent ceiling)", decision)
	}
	if pol.calls() != 1 {
		t.Fatalf("policy consulted %d times, want 1 (in-plan resource reaches policy)", pol.calls())
	}
}
