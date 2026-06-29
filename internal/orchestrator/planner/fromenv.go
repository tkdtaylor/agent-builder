package planner

import (
	"fmt"
	"os"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
)

// EnvPlanner is the env var that selects the Planner concrete (REQ-100-06). It
// mirrors the constant the orchestrate subcommand (task 099) reads; both name the
// same contract so the CLI and this assembler agree on the value space.
const EnvPlanner = "AGENT_BUILDER_PLANNER"

// Planner-selection values for EnvPlanner.
const (
	// PlannerStructured selects the rule-based StructuredPlanner (default).
	PlannerStructured = "structured"
	// PlannerLLM selects the LLM-backed LLMPlanner (this task).
	PlannerLLM = "llm"
)

// NewPlannerFromEnv selects the Planner concrete per AGENT_BUILDER_PLANNER
// (REQ-100-06):
//
//   - unset or "structured" → the rule-based *orchestrator.StructuredPlanner
//     (no model, no executor path); the resolver/invoker are ignored.
//   - "llm" → an *LLMPlanner over the supplied resolver and invoker.
//   - any other value → a non-nil error (the CLI prints it and exits ExitUsage).
//
// Both returned concretes satisfy orchestrator.Planner, so the orchestrator adopts
// either behind the same seam with no change to orchestrator.go. The "llm" branch
// requires a non-nil resolver and invoker; passing nil for either when "llm" is
// selected is a configuration error returned to the caller (it never panics on the
// env-driven path).
func NewPlannerFromEnv(resolver ExecutorResolver, invoke Invoker) (orchestrator.Planner, error) {
	choice := strings.TrimSpace(os.Getenv(EnvPlanner))
	switch choice {
	case "", PlannerStructured:
		return orchestrator.NewStructuredPlanner(), nil
	case PlannerLLM:
		if resolver == nil || invoke == nil {
			return nil, fmt.Errorf("%s=%q requires a model resolver and invoker, but one was nil", EnvPlanner, PlannerLLM)
		}
		return New(resolver, invoke), nil
	default:
		return nil, fmt.Errorf("%s=%q is not a known planner (want %q or %q)", EnvPlanner, choice, PlannerStructured, PlannerLLM)
	}
}
