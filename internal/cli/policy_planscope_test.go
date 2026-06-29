package cli

import (
	"sort"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// planFromSpec builds an orchestrator.Plan whose AllowedResources() derives the
// given goal ID, recipe names, and task IDs (ADR 055 seam 1, task 118).
func planFromSpec(goalID string, subs ...orchestrator.SubGoal) orchestrator.Plan {
	return orchestrator.Plan{GoalID: goalID, SubGoals: subs}
}

func subGoal(recipe, taskID string) orchestrator.SubGoal {
	return orchestrator.SubGoal{RecipeName: recipe, Task: supervisor.Task{ID: taskID}}
}

// sortedSet returns a sorted copy so set equality is order-insensitive.
func sortedSet(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func setEqual(t *testing.T, got, want []string) {
	t.Helper()
	g, w := sortedSet(got), sortedSet(want)
	if len(g) != len(w) {
		t.Fatalf("set mismatch: got %v (len %d), want %v (len %d)", got, len(g), want, len(w))
	}
	for i := range g {
		if g[i] != w[i] {
			t.Fatalf("set mismatch: got %v, want %v", got, want)
		}
	}
}

// --- effectiveAllow: pure-helper table-driven set tests --------------------

// TC-002 / TC-003 / TC-004 (pure side): effectiveAllow intersects the plan-derived
// set with the deployment base, narrowing only. Base empty/whitespace → full set;
// disjoint base → empty set (fail-closed).
func TestEffectiveAllowTable(t *testing.T) {
	plan := planFromSpec("goal-7",
		subGoal("coding-agent", "goal-7-0"),
		subGoal("docs-fix", "goal-7-1"),
	)
	// Plan-derived set = {goal-7, coding-agent, docs-fix, goal-7-0, goal-7-1}.
	full := []string{"goal-7", "coding-agent", "docs-fix", "goal-7-0", "goal-7-1"}

	cases := []struct {
		name string
		base []string
		want []string
	}{
		// TC-003: nil base → full plan-derived set unchanged.
		{name: "nil base → full set", base: nil, want: full},
		// TC-003: empty base → full set.
		{name: "empty base → full set", base: []string{}, want: full},
		// TC-002: base intersects/narrows. "other" is dropped (not in plan);
		// docs-fix/goal-7-0/goal-7-1 dropped (not in base).
		{name: "narrowing base", base: []string{"coding-agent", "goal-7", "other"}, want: []string{"coding-agent", "goal-7"}},
		// TC-002 variant: base narrows to a single recipe.
		{name: "single-element base", base: []string{"docs-fix"}, want: []string{"docs-fix"}},
		// TC-004: disjoint base → empty effective set (fail-closed).
		{name: "disjoint base → empty", base: []string{"unrelated"}, want: []string{}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := effectiveAllow(plan, tc.base)
			setEqual(t, got, tc.want)
		})
	}
}

// TC-003 (whitespace): parseAllowBase treats a whitespace-only env value as no base,
// so the full plan-derived set is used unchanged.
func TestEffectiveAllowWhitespaceBaseIsFullSet(t *testing.T) {
	plan := planFromSpec("goal-1", subGoal("coding-agent", "goal-1-0"))
	base := parseAllowBase("   ,  , \t ")
	if len(base) != 0 {
		t.Fatalf("parseAllowBase(whitespace) = %v, want empty (no narrowing)", base)
	}
	setEqual(t, effectiveAllow(plan, base), plan.AllowedResources())
}

// TC-002: parseAllowBase parses a real comma list, trimming and dropping blanks.
func TestParseAllowBaseTrimsAndDropsBlanks(t *testing.T) {
	got := parseAllowBase(" coding-agent , goal-7 ,, other ")
	setEqual(t, got, []string{"coding-agent", "goal-7", "other"})
}

// --- cross-module: the policy daemon RECEIVES the derived set --------------

// recordingDaemon is a fake policyDaemon; recordingStarter records the --allow argv
// each ConfigureForPlan hands the daemon, proving the consumer (the daemon's --allow)
// receives the producer's value (effectiveAllow(plan, base)), not merely that the
// helper returns it.
type recordingDaemon struct{ stopped bool }

func (d *recordingDaemon) Stop() error { d.stopped = true; return nil }

type recordingStarter struct {
	mu       sync.Mutex
	allowArg [][]string // one entry per daemon launch, in order
}

func (r *recordingStarter) start(_, _ string, allow []string) (policyDaemon, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allowArg = append(r.allowArg, append([]string(nil), allow...))
	return &recordingDaemon{}, nil
}

func (r *recordingStarter) lastAllow(t *testing.T) []string {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.allowArg) == 0 {
		t.Fatalf("daemon was never launched (no --allow recorded)")
	}
	return r.allowArg[len(r.allowArg)-1]
}

func newPlanScoped(rec *recordingStarter, base []string) *planScopedPolicy {
	return &planScopedPolicy{
		binPath:    "/fake/policy-engine",
		socketPath: "/tmp/fake.sock",
		base:       base,
		newDaemon:  rec.start,
	}
}

// TC-001: with no deployment base, the daemon serving the plan's decisions is
// launched with --allow == exactly the plan-derived set (set equality), replacing
// the previously-empty allow that denied everything.
func TestDaemonReceivesPlanDerivedAllowNoBase(t *testing.T) {
	rec := &recordingStarter{}
	psp := newPlanScoped(rec, nil)
	plan := planFromSpec("goal-1", subGoal("coding-agent", "goal-1-0"))
	// Plan-derived = {goal-1, coding-agent, goal-1-0}.

	if err := psp.ConfigureForPlan(plan); err != nil {
		t.Fatalf("ConfigureForPlan: %v", err)
	}
	// The consumer (daemon --allow) received the producer's full plan-derived set.
	setEqual(t, rec.lastAllow(t), []string{"goal-1", "coding-agent", "goal-1-0"})

	// And the allow is non-empty — the previously-empty deny-all allow is gone.
	if len(rec.lastAllow(t)) == 0 {
		t.Fatal("daemon launched with empty --allow; expected the plan-derived set")
	}
}

// TC-002: a deployment base intersects (narrows) the plan-derived set; the daemon
// receives exactly the intersection.
func TestDaemonReceivesIntersectionWithBase(t *testing.T) {
	rec := &recordingStarter{}
	psp := newPlanScoped(rec, []string{"coding-agent", "goal-7", "other"})
	plan := planFromSpec("goal-7",
		subGoal("coding-agent", "goal-7-0"),
		subGoal("docs-fix", "goal-7-1"),
	)
	if err := psp.ConfigureForPlan(plan); err != nil {
		t.Fatalf("ConfigureForPlan: %v", err)
	}
	setEqual(t, rec.lastAllow(t), []string{"coding-agent", "goal-7"})
}

// TC-003: empty base → the daemon receives the full plan-derived set unchanged.
func TestDaemonReceivesFullSetWhenBaseEmpty(t *testing.T) {
	rec := &recordingStarter{}
	psp := newPlanScoped(rec, nil)
	plan := planFromSpec("goal-3",
		subGoal("coding-agent", "goal-3-0"),
		subGoal("docs-fix", "goal-3-1"),
	)
	if err := psp.ConfigureForPlan(plan); err != nil {
		t.Fatalf("ConfigureForPlan: %v", err)
	}
	setEqual(t, rec.lastAllow(t), plan.AllowedResources())
}

// TC-004: a disjoint base yields an empty effective set; the daemon is launched with
// an empty --allow → fail-closed deny. And the client is wired (so Decide reaches the
// daemon, which denies on the empty allow), not short-circuited.
func TestDaemonReceivesEmptyAllowOnDisjointBaseFailClosed(t *testing.T) {
	rec := &recordingStarter{}
	psp := newPlanScoped(rec, []string{"unrelated"})
	plan := planFromSpec("goal-4", subGoal("coding-agent", "goal-4-0"))

	if err := psp.ConfigureForPlan(plan); err != nil {
		t.Fatalf("ConfigureForPlan: %v", err)
	}
	got := rec.lastAllow(t)
	if len(got) != 0 {
		t.Fatalf("daemon --allow = %v, want empty (disjoint base → fail-closed deny)", got)
	}
}

// TC-004 (pre-config): before any plan is configured there is no daemon, so Decide is
// fail-closed deny — no spawn proceeds without a plan-scoped engine behind it.
func TestDecideBeforeConfigureIsDeny(t *testing.T) {
	rec := &recordingStarter{}
	psp := newPlanScoped(rec, nil)
	resp, err := psp.Decide(policy.DecideRequest{
		Action:   policy.Action{Name: orchestrator.SpawnAction},
		Resource: policy.Resource{Type: "plan", ID: "goal-x"},
	})
	if err != nil {
		t.Fatalf("Decide error = %v", err)
	}
	if resp.Decision != policy.DecisionDeny {
		t.Fatalf("Decide before ConfigureForPlan = %v, want deny (fail-closed)", resp.Decision)
	}
	if len(rec.allowArg) != 0 {
		t.Fatalf("daemon launched %d times before ConfigureForPlan, want 0", len(rec.allowArg))
	}
}

// planScopedPolicy must satisfy the orchestrator's optional PlanScoper extension so
// the orchestrator's Handle path calls ConfigureForPlan before issuing decisions.
var _ orchestrator.PlanScoper = (*planScopedPolicy)(nil)
var _ orchestrator.PolicyClient = (*planScopedPolicy)(nil)
