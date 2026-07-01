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
