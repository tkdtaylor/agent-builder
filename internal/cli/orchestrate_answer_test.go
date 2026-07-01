package cli

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// Compile-time assertion (TC-140-02): cliAnswerer satisfies orchestrator.Answerer.
var _ orchestrator.Answerer = cliAnswerer{}

// Stub seams for testing goalAnalyzerFromEnv.
type stubSeams struct{}

func (s *stubSeams) Resolve(ctx context.Context, spec router.RoutingSpec) (registry.RegistryEntry, error) {
	return registry.RegistryEntry{}, nil
}

func (s *stubSeams) Invoke(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
	return `{"kind": "answer", "complexity": "simple"}`, nil
}

// TC-140-01: the AGENT_BUILDER_GOAL_ANALYSIS gate — enabling values yield a heuristic
// analyzer; empty/false yields nil (default-off, coding-only).
// REQ-142-03: the "llm" value yields an LLMGoalAnalyzer when seams are available.
func TestGoalAnalyzerFromEnv(t *testing.T) {
	cases := []struct {
		val            string
		wantNil        bool
		wantLLM        bool
		wantHeuristic  bool
		provideSeams   bool
	}{
		{"", true, false, false, false},
		{"false", true, false, false, false},
		{"0", true, false, false, false},
		{"true", false, false, true, false},
		{"1", false, false, true, false},
		{"yes", false, false, true, false},
		{"heuristic", false, false, true, false},
		{"ON", false, false, true, false},
		{"llm", false, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			var resolver planner.ExecutorResolver
			var invoke planner.Invoker

			if tc.provideSeams {
				// For llm case, provide stub seams.
				stubs := &stubSeams{}
				resolver = stubs
				invoke = stubs.Invoke
			}

			analyzer := goalAnalyzerFromEnv(
				func(string) string { return tc.val },
				resolver, invoke,
			)

			if tc.wantNil && analyzer != nil {
				t.Errorf("goalAnalyzerFromEnv(%q) = %T, want nil", tc.val, analyzer)
			}
			if tc.wantHeuristic && analyzer != nil {
				if _, ok := analyzer.(*orchestrator.HeuristicGoalAnalyzer); !ok {
					t.Errorf("goalAnalyzerFromEnv(%q) = %T, want *HeuristicGoalAnalyzer", tc.val, analyzer)
				}
			}
			if tc.wantLLM && analyzer != nil {
				if _, ok := analyzer.(*planner.LLMGoalAnalyzer); !ok {
					t.Errorf("goalAnalyzerFromEnv(%q) = %T, want *LLMGoalAnalyzer", tc.val, analyzer)
				}
			}
		})
	}
}
