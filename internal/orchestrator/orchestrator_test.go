package orchestrator_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"

	// Import for registration side-effect: registers the "docs-fix" recipe so
	// recipe.SelectRecipe("docs-fix") resolves in dispatch tests. "coding-agent"
	// is registered transitively via the orchestrator's import of internal/runtime.
	_ "github.com/tkdtaylor/agent-builder/internal/recipe/docsfix"
)

// --- test doubles ------------------------------------------------------------

// fakePolicy returns a fixed decision for the plan-level spawn-plan action. The
// per-sub-goal spawn-worker action (task 085) defaults to allow so the 081
// pause/resume/dispatch tests exercise dispatch as before; tests that want to gate
// spawn-worker use recordingPolicy instead.
type fakePolicy struct {
	mu       sync.Mutex // guards calls; Decide runs concurrently under task-086 fan-out
	decision policy.Decision
	calls    int
}

func (f *fakePolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	// spawn-worker (task 085) defaults to allow under this fake — its decision is
	// only meaningful for spawn-plan, which is what this fake was written to gate.
	if req.Action.Name == orchestrator.SpawnWorkerAction {
		return policy.DecideResponse{Decision: policy.DecisionAllow}, nil
	}
	return policy.DecideResponse{Decision: f.decision}, nil
}

// fakeReporter records every reported text in order.
type fakeReporter struct {
	mu       sync.Mutex
	reported []string
}

func (r *fakeReporter) Report(_ context.Context, text string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reported = append(r.reported, text)
	return nil
}

func (r *fakeReporter) Reported() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.reported))
	copy(out, r.reported)
	return out
}

// dispatchSpy records every dispatch call and returns a configurable error.
//
// Task 086 note: dispatch is concurrent, so the ORDER in which fn is called is
// nondeterministic. Failures must therefore be keyed on sub-goal IDENTITY
// (errBySpec, keyed on Task.Spec), NOT on call index — errByIndex remains for the
// legacy "fail the i-th call" cases that don't assert which sub-goal failed, but any
// test asserting that a SPECIFIC sub-goal failed must use errBySpec.
type dispatchSpy struct {
	mu          sync.Mutex
	recipeNames []string
	specs       []string
	errByIndex  map[int]error
	errBySpec   map[string]error
}

func newDispatchSpy() *dispatchSpy {
	return &dispatchSpy{errByIndex: map[int]error{}, errBySpec: map[string]error{}}
}

func (s *dispatchSpy) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	idx := len(s.recipeNames)
	s.recipeNames = append(s.recipeNames, sub.RecipeName)
	s.specs = append(s.specs, sub.Task.Spec)
	if err, ok := s.errBySpec[sub.Task.Spec]; ok {
		return err
	}
	return s.errByIndex[idx]
}

func (s *dispatchSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recipeNames)
}

// dispatchedRecipes returns the SET of recipe names dispatched (order-independent).
// Under task 086 concurrent dispatch, the spy records calls in goroutine-completion
// order, which is nondeterministic — assertions on WHICH recipes dispatched must use
// this set, not the slice order. Deterministic outcome ORDER is asserted via the
// PlanResult.Outcomes slice (which dispatchPlan writes by sub-goal index), not here.
func (s *dispatchSpy) dispatchedRecipes() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]bool, len(s.recipeNames))
	for _, r := range s.recipeNames {
		set[r] = true
	}
	return set
}

// dispatchedSpecs returns the SET of sub-goal specs dispatched (order-independent),
// for the same reason as dispatchedRecipes.
func (s *dispatchSpy) dispatchedSpecs() map[string]bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	set := make(map[string]bool, len(s.specs))
	for _, sp := range s.specs {
		set[sp] = true
	}
	return set
}

// knownRecipes are the recipe names the StructuredPlanner recognizes as prefixes
// in the structured-plan format.
var knownRecipes = []string{"coding-agent", "docs-fix"}

// --- TC-081-01 ---------------------------------------------------------------

func TestTC081_01_FreeFormGoalCollapsesToSingleSubGoal(t *testing.T) {
	p := orchestrator.NewStructuredPlanner(knownRecipes...)
	goal := supervisor.Task{ID: "g1", Spec: "Fix the 3 broken links in docs/spec/"}

	plan, err := p.Plan(goal)
	if err != nil {
		t.Fatalf("Plan: unexpected error: %v", err)
	}
	if len(plan.SubGoals) != 1 {
		t.Fatalf("free-form goal: want 1 sub-goal, got %d", len(plan.SubGoals))
	}
	if plan.SubGoals[0].RecipeName != "coding-agent" {
		t.Errorf("default recipe: want %q, got %q", "coding-agent", plan.SubGoals[0].RecipeName)
	}
	if plan.SubGoals[0].Task.Spec != "Fix the 3 broken links in docs/spec/" {
		t.Errorf("sub-goal spec: want full goal text, got %q", plan.SubGoals[0].Task.Spec)
	}
}

func TestTC081_01_StructuredGoalDecomposesOneToOne(t *testing.T) {
	p := orchestrator.NewStructuredPlanner(knownRecipes...)
	goal := supervisor.Task{ID: "g2", Spec: "# a plan\ncoding-agent: implement feature X\n\ndocs-fix: update the changelog\n"}

	plan, err := p.Plan(goal)
	if err != nil {
		t.Fatalf("Plan: unexpected error: %v", err)
	}
	if len(plan.SubGoals) != 2 {
		t.Fatalf("structured goal: want 2 sub-goals (comment+blank ignored), got %d", len(plan.SubGoals))
	}
	if plan.SubGoals[0].RecipeName != "coding-agent" || plan.SubGoals[0].Task.Spec != "implement feature X" {
		t.Errorf("sub-goal 0: got recipe=%q spec=%q", plan.SubGoals[0].RecipeName, plan.SubGoals[0].Task.Spec)
	}
	if plan.SubGoals[1].RecipeName != "docs-fix" || plan.SubGoals[1].Task.Spec != "update the changelog" {
		t.Errorf("sub-goal 1: got recipe=%q spec=%q", plan.SubGoals[1].RecipeName, plan.SubGoals[1].Task.Spec)
	}
}

// Ordering: plan is produced and surfaced to the approval gate BEFORE any dispatch.
func TestTC081_01_NoDispatchBeforeApproval(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)

	_, err := o.Handle(context.Background(), supervisor.Task{ID: "g1", Spec: "coding-agent: do the thing"})
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if spy.count() != 0 {
		t.Fatalf("expected 0 dispatches before approval, got %d", spy.count())
	}
}

// --- TC-081-02 ---------------------------------------------------------------

func TestTC081_02_RequireApprovalPausesThenResumes(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)

	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}

	// Pause.
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if spy.count() != 0 {
		t.Fatalf("require_approval: want 0 dispatches, got %d", spy.count())
	}
	reported := rep.Reported()
	if len(reported) != 1 {
		t.Fatalf("require_approval: want exactly 1 report (approval request), got %d", len(reported))
	}
	if !strings.Contains(reported[0], "Approve?") {
		t.Errorf("approval request missing %q: %q", "Approve?", reported[0])
	}
	for _, want := range []string{"coding-agent", "implement X", "docs-fix", "update Y"} {
		if !strings.Contains(reported[0], want) {
			t.Errorf("approval request missing %q: %q", want, reported[0])
		}
	}
	if !o.HasPendingPlan("g1") {
		t.Errorf("expected pending plan held for goal g1")
	}

	// Resume with a valid operator->orchestrator approval.
	if _, err := o.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: "g1", Approved: true,
	}); err != nil {
		t.Fatalf("Resume: unexpected error: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("after approval: want 2 dispatches, got %d", spy.count())
	}
	// Task 086: dispatch is concurrent, so the SET of dispatched recipes is asserted
	// (call order is nondeterministic). Deterministic outcome ORDER is asserted via
	// the PlanResult below in the order-sensitive tests (TC-081-03/04).
	dispatched := spy.dispatchedRecipes()
	if !dispatched["coding-agent"] || !dispatched["docs-fix"] {
		t.Errorf("dispatched recipes: want {coding-agent, docs-fix}, got %v", dispatched)
	}
	// A PlanResult summary is reported after dispatch.
	if got := len(rep.Reported()); got != 2 {
		t.Fatalf("after approval: want 2 reports total (request+summary), got %d", got)
	}
}

func TestTC081_02_ResumeRejectsRoleMismatch(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	// task 098 SEC-001 carry-forward: a wrong-role approval is rejected, no dispatch.
	cases := []orchestrator.Approval{
		{From: "attacker", To: "orchestrator", GoalID: "g1", Approved: true},
		{From: "operator", To: "worker", GoalID: "g1", Approved: true},
	}
	for _, ap := range cases {
		if _, err := o.Resume(context.Background(), ap); err == nil {
			t.Errorf("Resume(%+v): want role-mismatch error, got nil", ap)
		}
	}
	if spy.count() != 0 {
		t.Fatalf("role mismatch: want 0 dispatches, got %d", spy.count())
	}
	// The plan must still be pending (a rejected role must not consume it).
	if !o.HasPendingPlan("g1") {
		t.Errorf("plan should remain pending after a rejected-role resume")
	}
}

// TC-081-02 no-replay: after a successful Resume consumes the plan, a second
// valid-role Resume on the same goal fails and produces 0 additional dispatches.
func TestTC081_02_ApprovalNoReplay(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionRequireApproval}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}
	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	// First valid Resume: must succeed, dispatch, and consume the plan.
	if _, err := o.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: "g1", Approved: true,
	}); err != nil {
		t.Fatalf("first Resume: unexpected error: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("after first Resume: want 2 dispatches, got %d", spy.count())
	}

	// Second Resume on the same goal: must fail (plan already consumed).
	if _, err := o.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: "g1", Approved: true,
	}); err == nil {
		t.Errorf("second Resume (replay): want error (plan consumed), got nil")
	}
	// No additional dispatch should have happened (spy count unchanged).
	if spy.count() != 2 {
		t.Fatalf("after replay attempt: want still 2 dispatches (no additional), got %d", spy.count())
	}
	// Plan must not be pending (it was consumed by the first Resume).
	if o.HasPendingPlan("g1") {
		t.Errorf("plan should NOT be pending after successful Resume consumed it")
	}
}

// --- TC-081-03 ---------------------------------------------------------------

func TestTC081_03_AllowDispatchesPerSubGoal(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}

	result, err := o.Handle(context.Background(), goal)
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("allow: want 2 dispatches, got %d", spy.count())
	}
	// Task 086: concurrent dispatch → assert the dispatched SET, not call order.
	recipes := spy.dispatchedRecipes()
	if !recipes["coding-agent"] || !recipes["docs-fix"] {
		t.Errorf("dispatched recipes: want {coding-agent, docs-fix}, got %v", recipes)
	}
	specs := spy.dispatchedSpecs()
	if !specs["implement X"] || !specs["update Y"] {
		t.Errorf("dispatched specs: want {implement X, update Y}, got %v", specs)
	}
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	// Outcome ORDER is deterministic (sub-goal order) even under concurrent dispatch.
	if result.Outcomes[0].Recipe != "coding-agent" || result.Outcomes[1].Recipe != "docs-fix" {
		t.Errorf("outcome order: want [coding-agent docs-fix], got [%s %s]",
			result.Outcomes[0].Recipe, result.Outcomes[1].Recipe)
	}
}

func TestTC081_03_UnknownRecipeIsFailedOutcomeNotDispatch(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	// Configure the planner to recognize the unknown name as a recipe prefix so a
	// sub-goal carrying it is produced; SelectRecipe will then fail for it.
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner("coding-agent", "no-such-recipe"),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: ok\nno-such-recipe: should fail"}

	result, err := o.Handle(context.Background(), goal)
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("unknown recipe must not dispatch: want 1 dispatch, got %d", spy.count())
	}
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	if !result.Outcomes[0].Success {
		t.Errorf("sub-goal 0 (coding-agent) should succeed")
	}
	if result.Outcomes[1].Success {
		t.Errorf("sub-goal 1 (no-such-recipe) should be a failed outcome")
	}
	if !strings.Contains(result.Outcomes[1].Detail, "recipe not found") {
		t.Errorf("failed outcome detail: want 'recipe not found', got %q", result.Outcomes[1].Detail)
	}
}

// --- TC-081-04 ---------------------------------------------------------------

func TestTC081_04_AggregatesSuccessAndFailure(t *testing.T) {
	spy := newDispatchSpy()
	// Task 086: key the failure on the sub-goal's spec (concurrent dispatch has no
	// stable call order). "update Y" is the docs-fix sub-goal → outcome 1 must fail.
	spy.errBySpec["update Y"] = errString("gate failed: go test")
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}

	result, err := o.Handle(context.Background(), goal)
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	if result.Goal != "coding-agent: implement X\ndocs-fix: update Y" {
		t.Errorf("PlanResult.Goal: got %q", result.Goal)
	}
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	// Sequential: the second sub-goal is dispatched even though it fails.
	if spy.count() != 2 {
		t.Fatalf("want both sub-goals dispatched, got %d", spy.count())
	}
	if result.Outcomes[0].Recipe != "coding-agent" || !result.Outcomes[0].Success {
		t.Errorf("outcome 0: want coding-agent success, got %+v", result.Outcomes[0])
	}
	if result.Outcomes[1].Recipe != "docs-fix" || result.Outcomes[1].Success {
		t.Errorf("outcome 1: want docs-fix failure, got %+v", result.Outcomes[1])
	}
	if !strings.Contains(result.Outcomes[1].Detail, "gate failed") {
		t.Errorf("outcome 1 detail: want 'gate failed', got %q", result.Outcomes[1].Detail)
	}

	// Exactly one summary reported, containing both recipes and a pass+fail marker.
	reported := rep.Reported()
	if len(reported) != 1 {
		t.Fatalf("want exactly 1 summary report, got %d", len(reported))
	}
	summary := reported[0]
	for _, want := range []string{"coding-agent", "docs-fix", "OK", "FAIL"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary missing %q: %q", want, summary)
		}
	}
}

// errString is a tiny error type so tests can return a fixed-message error.
type errString string

func (e errString) Error() string { return string(e) }

// --- compile-time interface checks -------------------------------------------

// The production policy client must satisfy the orchestrator's narrow seam.
var _ orchestrator.PolicyClient = (*policy.PolicyClient)(nil)

// recipe.SelectRecipe must resolve the recipes the dispatch tests assert on, so
// the seam contract is real (not just a name the spy echoes).
func TestRecipesRegistered(t *testing.T) {
	for _, name := range []string{"coding-agent", "docs-fix"} {
		if _, err := recipe.SelectRecipe(name); err != nil {
			t.Errorf("recipe %q must be registered for dispatch tests: %v", name, err)
		}
	}
}
