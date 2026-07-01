package orchestrator

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Compile-time assertion (TC-138-01): *HeuristicGoalAnalyzer satisfies GoalAnalyzer.
var _ GoalAnalyzer = (*HeuristicGoalAnalyzer)(nil)

func analyze(t *testing.T, spec string) GoalAnalysis {
	t.Helper()
	a := NewHeuristicGoalAnalyzer()
	res, err := a.Analyze(supervisor.Task{ID: "g1", Spec: spec})
	if err != nil {
		t.Fatalf("Analyze(%q) error = %v", spec, err)
	}
	return res
}

// TC-138-01: constructor returns non-nil.
func TestHeuristicAnalyzerSatisfiesSeam(t *testing.T) {
	if NewHeuristicGoalAnalyzer() == nil {
		t.Fatal("NewHeuristicGoalAnalyzer returned nil")
	}
}

// TC-138-02: kind classification, both directions, rule order pinned.
func TestHeuristicAnalyzerClassifiesKind(t *testing.T) {
	cases := []struct {
		spec string
		want GoalKind
	}{
		{"What is the capital of France?", KindAnswer},
		{"how does TCP work", KindAnswer},
		{"add a subtract function to github.com/x/calc", KindCoding}, // repo beats verb
		{"implement a REST endpoint", KindCoding},                    // build verb
		{"refactor internal/foo/bar.go", KindCoding},                 // path
		{"the capital of France", KindAnswer},                        // default
	}
	for _, tc := range cases {
		if got := analyze(t, tc.spec).Kind; got != tc.want {
			t.Errorf("Analyze(%q).Kind = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TC-138-03: complexity classification.
func TestHeuristicAnalyzerClassifiesComplexity(t *testing.T) {
	long := "please build a comprehensive multi tenant billing system with invoicing " +
		"reporting analytics tax handling refunds proration dunning and a full admin " +
		"dashboard covering every currency and locale we support across all regions"
	cases := []struct {
		spec string
		want GoalComplexity
	}{
		{"What is the capital of France?", ComplexitySimple},
		{long, ComplexityComplex},
		{"build the parser then wire it to the CLI", ComplexityComplex},
	}
	for _, tc := range cases {
		if got := analyze(t, tc.spec).Complexity; got != tc.want {
			t.Errorf("Analyze(%q).Complexity = %q, want %q", tc.spec, got, tc.want)
		}
	}
}

// TC-138-04: rationale is non-empty for both kinds.
func TestHeuristicAnalyzerRationaleNonEmpty(t *testing.T) {
	if analyze(t, "What is the capital of France?").Rationale == "" {
		t.Error("answer goal has empty rationale")
	}
	if analyze(t, "implement a parser in github.com/x/y").Rationale == "" {
		t.Error("coding goal has empty rationale")
	}
}

// TC-146-01: the heuristic analyzer emits a capability tier by the ADR 061 rubric:
// simple/mechanical → 1; complex-but-not-design/security → 2; design/architecture/
// security/concurrency/cryptography → 3. (The ambiguous → 0 case is the LLM path's
// unset sentinel; the heuristic always has a definite complexity so it never emits 0
// — asserted separately below.)
func TestAnalyzerEmitsTierBySimpleGoal(t *testing.T) {
	// A long, non-security complex goal (>30 words) to trip ComplexityComplex
	// without any design/security keyword.
	longNeutral := "please gather every quarterly sales figure across all regions and " +
		"stores and product lines and roll them up into one big spreadsheet with " +
		"subtotals grand totals and per region breakdowns for the annual review meeting"

	cases := []struct {
		name string
		spec string
		want int
	}{
		{"trivial mechanical", "What is the capital of France?", 1},
		{"simple add function", "add a subtract function", 1},
		{"complex not security", longNeutral, 2},
		{"multi-step not security", "build the parser then wire it to the CLI", 2},
		{"design keyword", "design the module boundaries", 3},
		{"architecture keyword", "review the system architecture", 3},
		{"security keyword", "audit the login flow for security holes", 3},
		{"auth keyword", "add auth to the API", 3},
		{"crypto keyword", "add crypto signing to the payload", 3},
		{"concurrency keyword", "fix the concurrency bug in the scheduler", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := analyze(t, tc.spec).CapabilityTier; got != tc.want {
				t.Errorf("Analyze(%q).CapabilityTier = %d, want %d", tc.spec, got, tc.want)
			}
		})
	}
}

// TC-146-01 (sentinel): the heuristic path never emits the 0 (unset) tier — it
// always has a definite complexity, so 0 is reserved for the LLM path's absent-tier
// fallback signal, not produced here.
func TestHeuristicAnalyzerNeverEmitsUnsetTier(t *testing.T) {
	for _, spec := range []string{
		"What is the capital of France?",
		"add a subtract function",
		"design a secure distributed lock",
		"",
	} {
		if got := analyze(t, spec).CapabilityTier; got == 0 {
			t.Errorf("Analyze(%q).CapabilityTier = 0 (unset); heuristic must always emit a definite tier", spec)
		}
	}
}

// TC-146-03: the emitted tier is independent of sensitivity. The heuristic analyzer
// takes no sensitivity input, so the same goal always yields the same tier — model
// tier (how strong) and sensitivity (how private) stay orthogonal (ADR 061). This
// pins that the analyzer has no hidden sensitivity coupling.
func TestTierIndependentOfSensitivity(t *testing.T) {
	// The same goal analyzed repeatedly must yield a stable tier; there is no
	// sensitivity knob on the analyzer to vary, which is exactly the invariant:
	// sensitivity cannot influence the emitted tier because it is not an input.
	const goal = "design a secure auth service"
	first := analyze(t, goal).CapabilityTier
	if first != 3 {
		t.Fatalf("precondition: Analyze(%q).CapabilityTier = %d, want 3", goal, first)
	}
	for i := 0; i < 5; i++ {
		if got := analyze(t, goal).CapabilityTier; got != first {
			t.Errorf("tier varied across calls: got %d, want %d — tier must not depend on any external axis", got, first)
		}
	}
}
