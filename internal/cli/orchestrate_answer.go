package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// EnvGoalAnalysis toggles goal analysis + the general-answer route on the
// orchestrate front door (ADR 060). Opt-in for now: when unset/false, every goal
// is a coding goal (pre-060 behavior, preserving the coding pipeline). When truthy,
// the orchestrator analyzes each goal and answers KindAnswer goals over the channel.
const EnvGoalAnalysis = "AGENT_BUILDER_GOAL_ANALYSIS"

// goalAnalyzerFromEnv returns the goal analyzer to inject, or nil when analysis is
// disabled (nil → the orchestrator treats every goal as coding). The heuristic is
// the default when enabled; the LLM analyzer is task 142 (REQ-142-03).
func goalAnalyzerFromEnv(getenv func(string) string, resolver planner.ExecutorResolver, invoke planner.Invoker) orchestrator.GoalAnalyzer {
	switch strings.ToLower(strings.TrimSpace(getenv(EnvGoalAnalysis))) {
	case "llm":
		if resolver == nil || invoke == nil {
			// If LLM seams are not configured, fall back to heuristic (never break intake).
			return orchestrator.NewHeuristicGoalAnalyzer()
		}
		return planner.NewLLMGoalAnalyzer(resolver, invoke)
	case "true", "1", "yes", "heuristic", "on":
		return orchestrator.NewHeuristicGoalAnalyzer()
	default:
		return nil
	}
}

// answerDefaultMinCapability is the routing-spec capability floor the answer route
// uses when the analyzer emitted no tier (CapabilityTier == 0). It mirrors the
// planner's defaultMinCapability (ADR 061 §4): the cheapest floor, letting the router
// pick the cheapest eligible brain.
const answerDefaultMinCapability = 1

// answerMinCapability resolves the model-capability floor for the answer route from
// the tier the goal analyzer emitted (ADR 061 §4). It is the single tier→MinCapability
// resolution on this path: a positive tier is used verbatim; a zero (unset/ambiguous)
// tier falls back to answerDefaultMinCapability. There is NO parallel complexity→tier
// mapping here — GoalAnalysis.CapabilityTier is the single source (task 146).
func answerMinCapability(capabilityTier int) int {
	if capabilityTier > 0 {
		return capabilityTier
	}
	return answerDefaultMinCapability
}

// cliAnswerer is the orchestrator.Answerer implementation for the orchestrate
// answer path (ADR 060). It selects a brain at the model-capability floor the goal
// analyzer emitted (ADR 061 §4) and answers via the single-shot Completer seam — the
// same construction the `ask` subcommand uses. Living in internal/cli keeps
// internal/executor out of internal/orchestrator (F-010/F-014).
type cliAnswerer struct{}

// Compile-time assertion: cliAnswerer satisfies orchestrator.Answerer.
var _ orchestrator.Answerer = cliAnswerer{}

// Answer routes the prompt to a brain at the model-capability floor derived from the
// analyzer's emitted tier (answerMinCapability) and returns the single-shot
// completion. The tier is wired straight into RoutingSpec.MinCapability, so the
// dynamic tier the analyzer chose is what the static router (ADR 043) selects within.
func (cliAnswerer) Answer(ctx context.Context, prompt string, capabilityTier int) (string, error) {
	minCap := answerMinCapability(capabilityTier)

	cat, err := buildBrainCatalog()
	if err != nil {
		return "", err
	}
	entry, err := router.New(cat).Select(router.RoutingSpec{MinCapability: minCap})
	if err != nil {
		// Fall back to the floor (a high-tier goal on a single low-tier brain still
		// gets answered rather than failing outright).
		entry, err = router.New(cat).Select(router.RoutingSpec{MinCapability: answerDefaultMinCapability})
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
