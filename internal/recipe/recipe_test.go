package recipe

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TestFakeGoalSource is a test-local fake implementation of GoalSource.
type TestFakeGoalSource struct {
	goal string
	err  error
}

func (f *TestFakeGoalSource) FetchGoal() (string, error) {
	return f.goal, f.err
}

// TestFakeResultSink is a test-local fake implementation of ResultSink.
type TestFakeResultSink struct {
	results []string
}

func (f *TestFakeResultSink) WriteResult(result string) error {
	f.results = append(f.results, result)
	return nil
}

// TestFakeGate is a test-local fake implementation of supervisor.Gate.
type TestFakeGate struct{}

func (f *TestFakeGate) Verify(repoPath string) gate.Verdict {
	return gate.Verdict{OK: true}
}

// TestRecipeTypeCompiles tests TC-076-01: Recipe type compiles with all four
// seam fields present; nil Gate is rejected; RoutingSpec round-trips.
func TestRecipeTypeCompiles(t *testing.T) {
	// Create test-local fakes for all four seam fields.
	goalSource := &TestFakeGoalSource{goal: "test goal"}
	routingSpec := RoutingSpec{
		MinCapability:   2,
		SensitivityHint: SensitivitySensitive,
	}
	gateFactory := func() supervisor.Gate {
		return &TestFakeGate{}
	}
	resultSink := &TestFakeResultSink{}
	blockWiring := map[string]interface{}{}

	// Create a Recipe with all fields populated.
	r := New(goalSource, routingSpec, gateFactory, resultSink, blockWiring)

	// Verify the Recipe is valid and non-zero.
	if r.GoalSource == nil {
		t.Error("GoalSource is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSink == nil {
		t.Error("ResultSink is nil")
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

	goalSource := &TestFakeGoalSource{goal: "test"}
	New(goalSource, RoutingSpec{}, nil, &TestFakeResultSink{}, nil)
}

// TestRecipeConstructor tests that a Recipe constructed with New has all
// expected fields set.
func TestRecipeConstructor(t *testing.T) {
	goalSource := &TestFakeGoalSource{}
	routingSpec := RoutingSpec{MinCapability: 1, SensitivityHint: SensitivityNone}
	gateFactory := func() supervisor.Gate { return &TestFakeGate{} }
	resultSink := &TestFakeResultSink{}
	blockWiring := map[string]interface{}{"key": "value"}

	r := New(goalSource, routingSpec, gateFactory, resultSink, blockWiring)

	if r.GoalSource != goalSource {
		t.Error("GoalSource not set correctly")
	}
	if r.RoutingSpec != routingSpec {
		t.Error("RoutingSpec not set correctly")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil after construction")
	}
	if r.ResultSink != resultSink {
		t.Error("ResultSink not set correctly")
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
	if r.GoalSource != nil || r.GateFactory != nil {
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
	if r.GoalSource != nil || r.GateFactory != nil {
		t.Error("expected zero Recipe on error")
	}
}

// TestDuplicateRegisterPanics tests TC-076-03: duplicate Register panics.
func TestDuplicateRegisterPanics(t *testing.T) {
	// Clean up any state from previous tests.
	resetRegistry()

	factory := func() (Recipe, error) {
		return New(
			&TestFakeGoalSource{},
			RoutingSpec{},
			func() supervisor.Gate { return &TestFakeGate{} },
			&TestFakeResultSink{},
			nil,
		), nil
	}

	// Register the first time — should succeed.
	Register("test-dup", factory)

	// Register the second time — should panic.
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate Register, got nil")
		}
	}()

	Register("test-dup", factory)
}

// TestRegisterAndSelectRoundTrip tests TC-076-04: Register + SelectRecipe round-trip.
func TestRegisterAndSelectRoundTrip(t *testing.T) {
	resetRegistry()

	// Create a factory that returns a Recipe with all seam fields populated.
	factory := func() (Recipe, error) {
		return New(
			&TestFakeGoalSource{goal: "task"},
			RoutingSpec{MinCapability: 1},
			func() supervisor.Gate { return &TestFakeGate{} },
			&TestFakeResultSink{},
			nil,
		), nil
	}

	Register("test-fake", factory)

	// Select the registered recipe.
	r, err := SelectRecipe("test-fake")
	if err != nil {
		t.Fatalf("SelectRecipe failed: %v", err)
	}

	// Verify all seam fields are non-nil.
	if r.GoalSource == nil {
		t.Error("GoalSource is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSink == nil {
		t.Error("ResultSink is nil")
	}

	// Verify RoutingSpec is non-zero.
	if r.RoutingSpec.MinCapability != 1 {
		t.Errorf("MinCapability = %d, want 1", r.RoutingSpec.MinCapability)
	}

	// Verify there is no ExecutorFactory field (by verifying the struct compiles
	// without it; this is a compile-time check).

	// Test that two calls to SelectRecipe return independent values.
	r2, err := SelectRecipe("test-fake")
	if err != nil {
		t.Fatalf("second SelectRecipe failed: %v", err)
	}

	// Verify they are independent (different pointers for the goal source instances).
	if r.GoalSource == r2.GoalSource {
		t.Error("expected independent Recipe values on two SelectRecipe calls")
	}
}

// TestListRecipes tests TC-076-05: ListRecipes returns stable-ordered slice.
func TestListRecipes(t *testing.T) {
	resetRegistry()

	factory := func(name string) RecipeFactory {
		return func() (Recipe, error) {
			return New(
				&TestFakeGoalSource{goal: name},
				RoutingSpec{},
				func() supervisor.Gate { return &TestFakeGate{} },
				&TestFakeResultSink{},
				nil,
			), nil
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

	// Verify "coding-agent" is NOT in the list (task 077).
	for _, name := range list1 {
		if name == "coding-agent" {
			t.Error("coding-agent should not be registered in task 076")
		}
	}
}
