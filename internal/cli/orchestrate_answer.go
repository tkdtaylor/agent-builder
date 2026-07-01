package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// EnvGoalAnalysis toggles goal analysis + the general-answer route on the
// orchestrate front door (ADR 060). Opt-in for now: when unset/false, every goal
// is a coding goal (pre-060 behavior, preserving the coding pipeline). When truthy,
// the orchestrator analyzes each goal and answers KindAnswer goals over the channel.
const EnvGoalAnalysis = "AGENT_BUILDER_GOAL_ANALYSIS"

// goalAnalyzerFromEnv returns the goal analyzer to inject, or nil when analysis is
// disabled (nil → the orchestrator treats every goal as coding). The heuristic is
// the default when enabled; the LLM analyzer is task 140.
func goalAnalyzerFromEnv(getenv func(string) string) orchestrator.GoalAnalyzer {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvGoalAnalysis))) {
	case "true", "1", "yes", "heuristic", "on":
		return orchestrator.NewHeuristicGoalAnalyzer()
	default:
		return nil
	}
}

// cliAnswerer is the orchestrator.Answerer implementation for the orchestrate
// answer path (ADR 060). It selects a brain by the goal's complexity (→ capability
// floor) and answers via the single-shot Completer seam — the same construction
// the `ask` subcommand uses. Living in internal/cli keeps internal/executor out of
// internal/orchestrator (F-010/F-014).
type cliAnswerer struct{}

// Compile-time assertion: cliAnswerer satisfies orchestrator.Answerer.
var _ orchestrator.Answerer = cliAnswerer{}

// Answer routes the prompt to a brain at a capability floor derived from complexity
// (simple → 1, complex → 2) and returns the single-shot completion.
func (cliAnswerer) Answer(ctx context.Context, prompt string, complexity orchestrator.GoalComplexity) (string, error) {
	minCap := 1
	if complexity == orchestrator.ComplexityComplex {
		minCap = 2
	}

	cat, err := buildBrainCatalog()
	if err != nil {
		return "", err
	}
	entry, err := router.New(cat).Select(router.RoutingSpec{MinCapability: minCap})
	if err != nil {
		// Fall back to the floor (a complex goal on a single low-tier brain still
		// gets answered rather than failing outright).
		entry, err = router.New(cat).Select(router.RoutingSpec{MinCapability: 1})
		if err != nil {
			return "", fmt.Errorf("select brain for answer: %w", err)
		}
	}

	comp, err := completerForEntry(entry)
	if err != nil {
		return "", err
	}
	return comp.Complete(ctx, entry, prompt)
}
