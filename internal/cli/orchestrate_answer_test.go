package cli

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
)

// Compile-time assertion (TC-140-02): cliAnswerer satisfies orchestrator.Answerer.
var _ orchestrator.Answerer = cliAnswerer{}

// TC-140-01: the AGENT_BUILDER_GOAL_ANALYSIS gate — enabling values yield a heuristic
// analyzer; empty/false yields nil (default-off, coding-only).
func TestGoalAnalyzerFromEnv(t *testing.T) {
	cases := []struct {
		val     string
		wantNil bool
	}{
		{"", true},
		{"false", true},
		{"0", true},
		{"true", false},
		{"1", false},
		{"yes", false},
		{"heuristic", false},
		{"ON", false},
	}
	for _, tc := range cases {
		got := goalAnalyzerFromEnv(func(string) string { return tc.val })
		if tc.wantNil && got != nil {
			t.Errorf("goalAnalyzerFromEnv(%q) = %T, want nil", tc.val, got)
		}
		if !tc.wantNil && got == nil {
			t.Errorf("goalAnalyzerFromEnv(%q) = nil, want a heuristic analyzer", tc.val)
		}
	}
}
