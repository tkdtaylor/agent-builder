package planner_test

// This test package imports internal/router and internal/registry (the routing
// path) but DELIBERATELY does NOT import internal/executor — proving the seam is
// the ExecutorResolver/Invoker interface, not the executor concrete (TC-100-04 L2).

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"

	// Registration side-effects so recipe.SelectRecipe / dispatch resolve the
	// recipes the decomposition responses name. "coding-agent" registers
	// transitively via the orchestrator's import of internal/runtime; "docs-fix"
	// needs its own blank import.
	_ "github.com/tkdtaylor/agent-builder/internal/recipe/docsfix"
)

// --- test doubles ------------------------------------------------------------

// stubResolver implements planner.ExecutorResolver AND carries the canned model
// text + an Invoker. It records the prompts the invoker saw and the number of
// Resolve calls. resolveErr forces the resolver to fail (TC-100-03 D); invokeErr
// forces the invoker to fail.
type stubResolver struct {
	mu          sync.Mutex
	cannedText  string
	resolveErr  error
	invokeErr   error
	resolveN    int
	invokePromp []string
	entry       registry.RegistryEntry
}

func (s *stubResolver) Resolve(_ context.Context, _ router.RoutingSpec) (registry.RegistryEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveN++
	if s.resolveErr != nil {
		return registry.RegistryEntry{}, s.resolveErr
	}
	return s.entry, nil
}

func (s *stubResolver) invoke(_ context.Context, _ registry.RegistryEntry, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.invokePromp = append(s.invokePromp, prompt)
	if s.invokeErr != nil {
		return "", s.invokeErr
	}
	return s.cannedText, nil
}

func (s *stubResolver) resolveCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolveN
}

func (s *stubResolver) prompts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.invokePromp))
	copy(out, s.invokePromp)
	return out
}

// allowAllPolicy authorizes every action (proving the self-repo guard is
// independent of the policy file, fail-closed by construction).
type allowAllPolicy struct{}

func (allowAllPolicy) Decide(_ policy.DecideRequest) (policy.DecideResponse, error) {
	return policy.DecideResponse{Decision: policy.DecisionAllow}, nil
}

// nopReporter discards reports.
type nopReporter struct{}

func (nopReporter) Report(_ context.Context, _ string) error { return nil }

// dispatchSpy records every dispatched sub-goal's target repo and spec.
type dispatchSpy struct {
	mu      sync.Mutex
	targets []string
	specs   []string
}

func (s *dispatchSpy) fn(_ context.Context, sub orchestrator.SubGoal, _ runtime.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.targets = append(s.targets, sub.TargetRepo)
	s.specs = append(s.specs, sub.Task.Spec)
	return nil
}

func (s *dispatchSpy) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.specs)
}

func (s *dispatchSpy) sawTarget(repo string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.targets {
		if t == repo {
			return true
		}
	}
	return false
}

// --- TC-100-01 ---------------------------------------------------------------

func TestTC100_01_PlanFromStubModel(t *testing.T) {
	stub := &stubResolver{
		cannedText: "coding-agent: Add rate limiting to the API layer\n" +
			"docs-fix: Update CHANGELOG for v1.2.0\n",
	}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "goal-1", Spec: "add rate limiting and update docs"})
	if err != nil {
		t.Fatalf("Plan: unexpected error: %v", err)
	}

	if plan.Goal != "add rate limiting and update docs" {
		t.Errorf("Plan.Goal = %q, want %q", plan.Goal, "add rate limiting and update docs")
	}
	if plan.GoalID != "goal-1" {
		t.Errorf("Plan.GoalID = %q, want %q", plan.GoalID, "goal-1")
	}
	if len(plan.SubGoals) != 2 {
		t.Fatalf("len(SubGoals) = %d, want 2", len(plan.SubGoals))
	}

	if plan.SubGoals[0].RecipeName != "coding-agent" {
		t.Errorf("SubGoals[0].RecipeName = %q, want %q", plan.SubGoals[0].RecipeName, "coding-agent")
	}
	if !strings.Contains(plan.SubGoals[0].Task.Spec, "Add rate limiting") {
		t.Errorf("SubGoals[0].Task.Spec = %q, want it to contain %q", plan.SubGoals[0].Task.Spec, "Add rate limiting")
	}
	if plan.SubGoals[1].RecipeName != "docs-fix" {
		t.Errorf("SubGoals[1].RecipeName = %q, want %q", plan.SubGoals[1].RecipeName, "docs-fix")
	}
	if !strings.Contains(plan.SubGoals[1].Task.Spec, "Update CHANGELOG") {
		t.Errorf("SubGoals[1].Task.Spec = %q, want it to contain %q", plan.SubGoals[1].Task.Spec, "Update CHANGELOG")
	}

	if got := stub.resolveCalls(); got != 1 {
		t.Errorf("Resolve called %d times, want exactly 1", got)
	}
	prompts := stub.prompts()
	if len(prompts) != 1 {
		t.Fatalf("invoker called %d times, want exactly 1", len(prompts))
	}
	if !strings.Contains(prompts[0], "add rate limiting and update docs") {
		t.Errorf("prompt missing goal text: %q", prompts[0])
	}
}

// TC-100-01 compile-time assertion: LLMPlanner satisfies orchestrator.Planner.
var _ orchestrator.Planner = (*planner.LLMPlanner)(nil)

// --- TC-100-02 ---------------------------------------------------------------

func TestTC100_02_TargetRepoSinkParsed(t *testing.T) {
	const repo = "github.com/tkdtaylor/exec-sandbox"
	stub := &stubResolver{
		cannedText: "coding-agent: task A | target_repo=" + repo + " | sink=" + repo + "\n",
	}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "do task A"})
	if err != nil {
		t.Fatalf("Plan: unexpected error: %v", err)
	}
	if len(plan.SubGoals) != 1 {
		t.Fatalf("len(SubGoals) = %d, want 1", len(plan.SubGoals))
	}
	sg := plan.SubGoals[0]
	if sg.TargetRepo != repo {
		t.Errorf("TargetRepo = %q, want %q", sg.TargetRepo, repo)
	}
	if sg.Sink != repo {
		t.Errorf("Sink = %q, want %q", sg.Sink, repo)
	}
	if sg.Task.Spec != "task A" {
		t.Errorf("Task.Spec = %q, want %q (metadata stripped)", sg.Task.Spec, "task A")
	}
	if strings.Contains(sg.Task.Spec, "target_repo") || strings.Contains(sg.Task.Spec, "sink=") {
		t.Errorf("Task.Spec still carries metadata: %q", sg.Task.Spec)
	}
}

// TC-100-02 self-repo bright line: an own-repo sub-goal is never dispatched. The
// LLMPlanner filters it defensively (so the plan that reaches the orchestrator has
// only the non-own-repo sub-goal), and the assembled orchestrator confirms the
// own-repo target is never handed to dispatch.
func TestTC100_02_SelfRepoNeverDispatched(t *testing.T) {
	stub := &stubResolver{
		cannedText: "coding-agent: edit self | target_repo=" + orchestrator.OwnRepo + "\n" +
			"docs-fix: update other | target_repo=github.com/tkdtaylor/some-other-repo\n",
	}
	llm := planner.New(stub, stub.invoke)

	spy := &dispatchSpy{}
	sink := audit.NewFakeSink()
	o := orchestrator.New(
		llm,
		allowAllPolicy{},
		nopReporter{},
		runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithAuditSink(sink),
	)

	result, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "edit self and update other"})
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	if spy.sawTarget(orchestrator.OwnRepo) {
		t.Errorf("own-repo target was dispatched; bright line breached")
	}
	// The non-own-repo sub-goal IS dispatched (the guard is targeted, not blanket).
	if !spy.sawTarget("github.com/tkdtaylor/some-other-repo") {
		t.Errorf("non-own-repo target was not dispatched; got targets %v", spy.targets)
	}
	if spy.count() != 1 {
		t.Fatalf("dispatch count = %d, want exactly 1 (own-repo filtered, other dispatched)", spy.count())
	}
	if len(result.Outcomes) == 0 {
		t.Fatal("expected at least one outcome")
	}
}

// --- TC-100-03 ---------------------------------------------------------------

func TestTC100_03_A_EmptyResponse(t *testing.T) {
	stub := &stubResolver{cannedText: ""}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "anything"})
	if err == nil {
		t.Fatal("Plan: want non-nil error on empty response, got nil")
	}
	if len(plan.SubGoals) != 0 || plan.Goal != "" || plan.GoalID != "" {
		t.Errorf("Plan must be the zero value on error, got %+v", plan)
	}
}

func TestTC100_03_B_GarbageResponse(t *testing.T) {
	// Garbage prose that names no known recipe must fail closed — the LLMPlanner
	// parses a MODEL response and (unlike StructuredPlanner on goal text) never
	// collapses unrecognized prose onto a default-recipe worker (TC-100-03 B).
	stub := &stubResolver{cannedText: "🤔 I cannot help with that."}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "anything"})
	if err == nil {
		t.Fatal("Plan: want non-nil error on garbage (no parseable sub-goal line), got nil")
	}
	if !errors.Is(err, planner.ErrEmptyResponse) {
		t.Errorf("error = %v, want it to wrap ErrEmptyResponse (describes the parse failure)", err)
	}
	if len(plan.SubGoals) != 0 || plan.Goal != "" || plan.GoalID != "" {
		t.Errorf("error path must yield the zero Plan, got %+v", plan)
	}
}

func TestTC100_03_B_OnlyBlankAndComments(t *testing.T) {
	// A response of only blank/comment lines yields no sub-goal → must error.
	stub := &stubResolver{cannedText: "\n  \n# just a comment\n   \n"}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "anything"})
	if err == nil {
		t.Fatal("Plan: want non-nil error when no parseable sub-goal line, got nil")
	}
	if !errors.Is(err, planner.ErrEmptyResponse) {
		t.Errorf("error = %v, want it to wrap ErrEmptyResponse", err)
	}
	if len(plan.SubGoals) != 0 {
		t.Errorf("error path must yield zero Plan, got %+v", plan)
	}
}

func TestTC100_03_C_OwnRepoOnly(t *testing.T) {
	// The only sub-goal targets the own-repo. The defensive filter drops it, leaving
	// zero sub-goals → the planner must fail closed (never a zero-sub-goal plan that
	// would silently succeed with no dispatch).
	stub := &stubResolver{
		cannedText: "coding-agent: edit self | target_repo=" + orchestrator.OwnRepo + "\n",
	}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "edit self"})
	if err == nil {
		t.Fatal("Plan: want non-nil error when only sub-goal is own-repo (filtered to zero), got nil")
	}
	if len(plan.SubGoals) != 0 {
		t.Errorf("error path must yield zero Plan, got %+v", plan)
	}
}

func TestTC100_03_D_ResolverError(t *testing.T) {
	resolveErr := errors.New("no eligible executor")
	stub := &stubResolver{resolveErr: resolveErr, cannedText: "coding-agent: x\n"}
	p := planner.New(stub, stub.invoke)

	plan, err := p.Plan(supervisor.Task{ID: "g", Spec: "anything"})
	if err == nil {
		t.Fatal("Plan: want non-nil error when resolver fails, got nil")
	}
	if !errors.Is(err, resolveErr) {
		t.Errorf("error = %v, want it to wrap the resolver error %v", err, resolveErr)
	}
	if len(plan.SubGoals) != 0 {
		t.Errorf("error path must yield zero Plan, got %+v", plan)
	}
}

func TestTC100_03_D_InvokerError(t *testing.T) {
	invokeErr := errors.New("model call failed")
	stub := &stubResolver{invokeErr: invokeErr}
	p := planner.New(stub, stub.invoke)

	_, err := p.Plan(supervisor.Task{ID: "g", Spec: "anything"})
	if err == nil {
		t.Fatal("Plan: want non-nil error when invoker fails, got nil")
	}
	if !errors.Is(err, invokeErr) {
		t.Errorf("error = %v, want it to wrap the invoker error %v", err, invokeErr)
	}
}

// --- TC-100-04 ---------------------------------------------------------------

// TC-100-04 L2: the seam is the ExecutorResolver interface, satisfied by a value
// that is NOT *executor.Executor (this test does not even import internal/executor).
// If this test file compiles with internal/router + internal/registry but no
// internal/executor import, the interface is functioning as the seam.
func TestTC100_04_SeamIsInterface(t *testing.T) {
	// The stub satisfies planner.ExecutorResolver but is NOT *executor.Executor —
	// the seam is the interface, not the concrete. acceptResolver takes the
	// interface type, so this only compiles because *stubResolver implements it.
	stub := &stubResolver{cannedText: "coding-agent: x\n"}
	acceptResolver := func(r planner.ExecutorResolver) planner.ExecutorResolver { return r }

	// Exercise the interface end-to-end through the planner (no executor concrete).
	p := planner.New(acceptResolver(stub), stub.invoke)
	if _, err := p.Plan(supervisor.Task{ID: "g", Spec: "x"}); err != nil {
		t.Fatalf("Plan via interface seam: %v", err)
	}
}

// --- TC-100-06 ---------------------------------------------------------------

func TestTC100_06_SelectByEnv(t *testing.T) {
	stub := &stubResolver{cannedText: "coding-agent: x\n"}

	t.Run("unset → StructuredPlanner", func(t *testing.T) {
		t.Setenv(planner.EnvPlanner, "")
		p, err := planner.NewPlannerFromEnv(stub, stub.invoke)
		if err != nil {
			t.Fatalf("NewPlannerFromEnv: %v", err)
		}
		if _, ok := p.(*orchestrator.StructuredPlanner); !ok {
			t.Errorf("want *orchestrator.StructuredPlanner, got %T", p)
		}
	})

	t.Run("structured → StructuredPlanner", func(t *testing.T) {
		t.Setenv(planner.EnvPlanner, "structured")
		p, err := planner.NewPlannerFromEnv(stub, stub.invoke)
		if err != nil {
			t.Fatalf("NewPlannerFromEnv: %v", err)
		}
		if _, ok := p.(*orchestrator.StructuredPlanner); !ok {
			t.Errorf("want *orchestrator.StructuredPlanner, got %T", p)
		}
	})

	t.Run("llm → LLMPlanner", func(t *testing.T) {
		t.Setenv(planner.EnvPlanner, "llm")
		p, err := planner.NewPlannerFromEnv(stub, stub.invoke)
		if err != nil {
			t.Fatalf("NewPlannerFromEnv: %v", err)
		}
		if _, ok := p.(*planner.LLMPlanner); !ok {
			t.Errorf("want *planner.LLMPlanner, got %T", p)
		}
	})

	t.Run("unknown → error", func(t *testing.T) {
		t.Setenv(planner.EnvPlanner, "magic")
		p, err := planner.NewPlannerFromEnv(stub, stub.invoke)
		if err == nil {
			t.Fatal("want non-nil error for unknown planner, got nil")
		}
		if p != nil {
			t.Errorf("want nil planner on error, got %T", p)
		}
	})
}
