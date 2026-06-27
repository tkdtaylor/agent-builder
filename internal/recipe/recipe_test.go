package recipe

import (
	"context"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestFakeGoalSource is a test-local fake implementation of supervisor.GoalSource.
type TestFakeGoalSource struct {
	task supervisor.Task
	ok   bool
	err  error
}

func (f *TestFakeGoalSource) Next() (supervisor.Task, bool, error) {
	return f.task, f.ok, f.err
}

// TestFakeResultSink is a test-local fake implementation of supervisor.ResultSink.
type TestFakeResultSink struct {
	results []supervisor.PublishRequest
}

func (f *TestFakeResultSink) Publish(ctx context.Context, req supervisor.PublishRequest) (supervisor.PublishResult, error) {
	f.results = append(f.results, req)
	return supervisor.PublishResult{Branch: req.Branch}, nil
}

// TestFakeGate is a test-local fake implementation of supervisor.Gate.
type TestFakeGate struct{}

func (f *TestFakeGate) Verify(repoPath string) gate.Verdict {
	return gate.Verdict{OK: true}
}

// TestRecipeTypeCompiles tests TC-076-01: Recipe type compiles with all four
// seam factories present; nil Gate is rejected; RoutingSpec round-trips.
func TestRecipeTypeCompiles(t *testing.T) {
	// Create test-local factories for all seam types.
	goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
		return &TestFakeGoalSource{task: supervisor.Task{ID: "test"}, ok: true}, nil
	}
	routingSpec := RoutingSpec{
		MinCapability:   2,
		SensitivityHint: SensitivitySensitive,
	}
	gateFactory := func() supervisor.Gate {
		return &TestFakeGate{}
	}
	resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
		return &TestFakeResultSink{}, nil
	}
	blockWiring := map[string]interface{}{}

	// Create a Recipe with all factory fields populated.
	r := New(goalSourceFactory, routingSpec, gateFactory, resultSinkFactory, blockWiring)

	// Verify the Recipe is valid and non-zero.
	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory is nil")
	}

	// Verify RoutingSpec round-trips correctly.
	if r.RoutingSpec.MinCapability != 2 {
		t.Errorf("MinCapability = %d, want 2", r.RoutingSpec.MinCapability)
	}
	if r.RoutingSpec.SensitivityHint != SensitivitySensitive {
		t.Errorf("SensitivityHint = %v, want SensitivitySensitive", r.RoutingSpec.SensitivityHint)
	}

	// Test zero-value RoutingSpec.
	zeroSpec := RoutingSpec{}
	if zeroSpec.MinCapability != 0 {
		t.Errorf("zero-value MinCapability = %d, want 0", zeroSpec.MinCapability)
	}
	if zeroSpec.SensitivityHint != SensitivityNone {
		t.Errorf("zero-value SensitivityHint = %v, want SensitivityNone", zeroSpec.SensitivityHint)
	}
}

// TestNilGateFactoryPanics tests TC-076-01 edge case: nil GateFactory panics.
func TestNilGateFactoryPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil GateFactory, but no panic occurred")
		}
	}()

	goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
		return &TestFakeGoalSource{}, nil
	}
	resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
		return &TestFakeResultSink{}, nil
	}
	New(goalSourceFactory, RoutingSpec{}, nil, resultSinkFactory, nil)
}

// TestNilGoalSourceFactoryPanics tests that nil GoalSourceFactory panics.
func TestNilGoalSourceFactoryPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil GoalSourceFactory, but no panic occurred")
		}
	}()

	gateFactory := func() supervisor.Gate {
		return &TestFakeGate{}
	}
	resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
		return &TestFakeResultSink{}, nil
	}
	New(nil, RoutingSpec{}, gateFactory, resultSinkFactory, nil)
}

// TestNilResultSinkFactoryPanics tests that nil ResultSinkFactory panics.
func TestNilResultSinkFactoryPanics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic for nil ResultSinkFactory, but no panic occurred")
		}
	}()

	goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
		return &TestFakeGoalSource{}, nil
	}
	gateFactory := func() supervisor.Gate {
		return &TestFakeGate{}
	}
	New(goalSourceFactory, RoutingSpec{}, gateFactory, nil, nil)
}

// TestRecipeConstructor tests that a Recipe constructed with New has all
// expected factory fields set.
func TestRecipeConstructor(t *testing.T) {
	goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
		return &TestFakeGoalSource{}, nil
	}
	routingSpec := RoutingSpec{MinCapability: 1, SensitivityHint: SensitivityNone}
	gateFactory := func() supervisor.Gate { return &TestFakeGate{} }
	resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
		return &TestFakeResultSink{}, nil
	}
	blockWiring := map[string]interface{}{"key": "value"}

	r := New(goalSourceFactory, routingSpec, gateFactory, resultSinkFactory, blockWiring)

	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory not set correctly")
	}
	if r.RoutingSpec != routingSpec {
		t.Error("RoutingSpec not set correctly")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil after construction")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory not set correctly")
	}
	// BlockWiring is a map, so we can only compare to nil or length check
	if len(r.BlockWiring) != len(blockWiring) {
		t.Error("BlockWiring length mismatch")
	}
}

// TestSelectRecipeEmptyName tests TC-076-03: SelectRecipe("") returns error.
func TestSelectRecipeEmptyName(t *testing.T) {
	resetRegistry()

	r, err := SelectRecipe("")
	if err == nil {
		t.Error("expected error for empty recipe name, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "empty") {
		t.Errorf("error message should mention emptiness, got: %v", err)
	}
	if r.GoalSourceFactory != nil || r.GateFactory != nil {
		t.Error("expected zero Recipe on error")
	}
}

// TestSelectRecipeUnknownName tests TC-076-03: SelectRecipe("unknown") returns error.
func TestSelectRecipeUnknownName(t *testing.T) {
	resetRegistry()

	r, err := SelectRecipe("does-not-exist")
	if err == nil {
		t.Error("expected error for unknown recipe name, got nil")
	}
	if err != nil && !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error message should mention the recipe name, got: %v", err)
	}
	if r.GoalSourceFactory != nil || r.GateFactory != nil {
		t.Error("expected zero Recipe on error")
	}
}

// TestDuplicateRegisterPanics tests TC-076-03: duplicate Register panics.
func TestDuplicateRegisterPanics(t *testing.T) {
	// Clean up any state from previous tests.
	resetRegistry()

	factory := func() (Recipe, error) {
		goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
			return &TestFakeGoalSource{}, nil
		}
		gateFactory := func() supervisor.Gate { return &TestFakeGate{} }
		resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
			return &TestFakeResultSink{}, nil
		}
		return New(goalSourceFactory, RoutingSpec{}, gateFactory, resultSinkFactory, nil), nil
	}

	// Register the first time — should succeed.
	Register("test-dup", factory)

	// Register the second time — should panic.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Register, got nil")
		} else {
			// Check that the panic message mentions the duplicate name.
			panicMsg := r.(string)
			if !strings.Contains(panicMsg, "test-dup") {
				t.Errorf("panic message should mention the duplicate name, got: %s", panicMsg)
			}
		}
	}()

	Register("test-dup", factory)
}

// TestRegisterAndSelectRoundTrip tests TC-076-04: Register + SelectRecipe round-trip.
func TestRegisterAndSelectRoundTrip(t *testing.T) {
	resetRegistry()

	// Create a factory that returns a Recipe with all seam factories populated.
	factory := func() (Recipe, error) {
		goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
			return &TestFakeGoalSource{task: supervisor.Task{ID: "task"}}, nil
		}
		gateFactory := func() supervisor.Gate { return &TestFakeGate{} }
		resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
			return &TestFakeResultSink{}, nil
		}
		return New(goalSourceFactory, RoutingSpec{MinCapability: 1}, gateFactory, resultSinkFactory, nil), nil
	}

	Register("test-fake", factory)

	// Select the registered recipe.
	r, err := SelectRecipe("test-fake")
	if err != nil {
		t.Fatalf("SelectRecipe failed: %v", err)
	}

	// Verify all seam factory fields are non-nil.
	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory is nil")
	}

	// Verify RoutingSpec is non-zero.
	if r.RoutingSpec.MinCapability != 1 {
		t.Errorf("MinCapability = %d, want 1", r.RoutingSpec.MinCapability)
	}

	// Verify the Name field equals the registered name.
	if r.Name != "test-fake" {
		t.Errorf("Name = %q, want %q", r.Name, "test-fake")
	}

	// Verify there is no ExecutorFactory field (by verifying the struct compiles
	// without it; this is a compile-time check).

	// Test that two calls to SelectRecipe return independent Recipe values
	// (the factories themselves are the same, but each call creates a new Recipe).
	r2, err := SelectRecipe("test-fake")
	if err != nil {
		t.Fatalf("second SelectRecipe failed: %v", err)
	}

	// Verify both have the same Name (it should be set by SelectRecipe).
	if r2.Name != "test-fake" {
		t.Errorf("second recipe Name = %q, want %q", r2.Name, "test-fake")
	}
}

// TestListRecipes tests TC-076-05: ListRecipes returns stable-ordered slice.
func TestListRecipes(t *testing.T) {
	resetRegistry()

	factory := func(name string) RecipeFactory {
		return func() (Recipe, error) {
			goalSourceFactory := func(cfg SeamConfig) (supervisor.GoalSource, error) {
				return &TestFakeGoalSource{task: supervisor.Task{ID: name}}, nil
			}
			gateFactory := func() supervisor.Gate { return &TestFakeGate{} }
			resultSinkFactory := func(cfg SeamConfig) (supervisor.ResultSink, error) {
				return &TestFakeResultSink{}, nil
			}
			return New(goalSourceFactory, RoutingSpec{}, gateFactory, resultSinkFactory, nil), nil
		}
	}

	Register("test-alpha", factory("alpha"))
	Register("test-beta", factory("beta"))
	Register("test-gamma", factory("gamma"))

	// First call to ListRecipes.
	list1 := ListRecipes()

	// Second call to ListRecipes — should return the same order.
	list2 := ListRecipes()

	if len(list1) != 3 {
		t.Errorf("expected 3 recipes, got %d", len(list1))
	}

	// Verify order is deterministic (alphabetical).
	expected := []string{"test-alpha", "test-beta", "test-gamma"}
	for i, name := range list1 {
		if name != expected[i] {
			t.Errorf("list1[%d] = %q, want %q", i, name, expected[i])
		}
	}

	for i, name := range list2 {
		if name != expected[i] {
			t.Errorf("list2[%d] = %q, want %q", i, name, expected[i])
		}
	}

	// Verify "coding-agent" is NOT in the list (task 076).
	for _, name := range list1 {
		if name == "coding-agent" {
			t.Error("coding-agent should not be registered in task 076")
		}
	}
}
