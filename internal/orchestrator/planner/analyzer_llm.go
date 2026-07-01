package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// LLMGoalAnalyzer is an LLM-backed GoalAnalyzer that classifies a goal's Kind
// and Complexity with a model via the Invoker seam (ADR 060 §4, task 142).
// It mirrors the LLMClarifier pattern (task 131) and the LLMPlanner (ADR 053).
// On parse failure or invoke error, it falls back to the heuristic analyzer
// to ensure analysis never breaks goal intake.
type LLMGoalAnalyzer struct {
	resolver ExecutorResolver
	invoke   Invoker
	// fallback is the heuristic analyzer for fail-safe fallback on LLM failures.
	fallback *orchestrator.HeuristicGoalAnalyzer
}

// Compile-time assertion: *LLMGoalAnalyzer satisfies orchestrator.GoalAnalyzer.
var _ orchestrator.GoalAnalyzer = (*LLMGoalAnalyzer)(nil)

// NewLLMGoalAnalyzer constructs an LLMGoalAnalyzer. Resolver and invoker are
// required; a nil resolver or invoker is a programmer error and panics.
func NewLLMGoalAnalyzer(resolver ExecutorResolver, invoke Invoker) *LLMGoalAnalyzer {
	if resolver == nil {
		panic("planner.NewLLMGoalAnalyzer: nil ExecutorResolver")
	}
	if invoke == nil {
		panic("planner.NewLLMGoalAnalyzer: nil Invoker")
	}
	return &LLMGoalAnalyzer{
		resolver: resolver,
		invoke:   invoke,
		fallback: orchestrator.NewHeuristicGoalAnalyzer(),
	}
}

// Analyze classifies a goal's Kind and Complexity by sending it to a model
// via the Invoker seam (REQ-142-01). On any error (parse, invoke, malformed
// output), it falls back to the heuristic analyzer (REQ-142-02) to ensure
// the analysis never breaks goal intake.
func (a *LLMGoalAnalyzer) Analyze(goal supervisor.Task) (orchestrator.GoalAnalysis, error) {
	ctx := context.Background()

	// Resolve a model at the comprehension capability floor (same as clarifier/planner).
	entry, err := a.resolver.Resolve(ctx, router.RoutingSpec{
		MinCapability:   1,
		SensitivityHint: router.SensitivityNone,
	})
	if err != nil {
		// Fallback to heuristic on resolver error (never break intake).
		return a.fallback.Analyze(goal)
	}

	prompt := a.buildPrompt(goal.Spec)
	response, err := a.invoke(ctx, entry, prompt)
	if err != nil {
		// Fallback to heuristic on invoke error (never break intake).
		return a.fallback.Analyze(goal)
	}

	// Parse the response into a GoalAnalysis; fallback on parse error.
	analysis, err := a.parse(response)
	if err != nil {
		// Fallback to heuristic on parse error (never break intake).
		return a.fallback.Analyze(goal)
	}

	return analysis, nil
}

func (a *LLMGoalAnalyzer) buildPrompt(goalText string) string {
	var b strings.Builder
	b.WriteString("Classify the following goal as either a general question (answer) or a coding task (coding).\n")
	b.WriteString("Also estimate its complexity (simple or complex).\n")
	b.WriteString("You MUST respond in this exact JSON format (and nothing else):\n")
	b.WriteString("{\n")
	b.WriteString("  \"kind\": \"answer\",\n")
	b.WriteString("  \"complexity\": \"simple\",\n")
	b.WriteString("  \"rationale\": \"...\"\n")
	b.WriteString("}\n")
	b.WriteString("Goal text:\n")
	b.WriteString(goalText)
	b.WriteString("\n")
	return b.String()
}

type rawAnalysis struct {
	Kind       *string `json:"kind"`
	Complexity *string `json:"complexity"`
	Rationale  *string `json:"rationale"`
}

// parse extracts Kind, Complexity, and Rationale from the model response.
// It attempts JSON parsing first; if that fails, it falls back to lenient
// string heuristics (same pattern as LLMClarifier).
func (a *LLMGoalAnalyzer) parse(response string) (orchestrator.GoalAnalysis, error) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return orchestrator.GoalAnalysis{}, errors.New("analyzer_llm: empty model response")
	}

	// 1. Try to extract and parse JSON from the response.
	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start != -1 && end != -1 && start < end {
		trimmed = trimmed[start : end+1]
	}

	var ra rawAnalysis
	if err := json.Unmarshal([]byte(trimmed), &ra); err == nil && ra.Kind != nil && ra.Complexity != nil {
		// Validate the kind and complexity values.
		kind := orchestrator.GoalKind(*ra.Kind)
		complexity := orchestrator.GoalComplexity(*ra.Complexity)

		// Normalize kind to lowercase.
		kind = orchestrator.GoalKind(strings.ToLower(string(kind)))
		if kind != orchestrator.KindAnswer && kind != orchestrator.KindCoding {
			return orchestrator.GoalAnalysis{}, fmt.Errorf("analyzer_llm: invalid kind %q (want answer or coding)", *ra.Kind)
		}

		// Normalize complexity to lowercase.
		complexity = orchestrator.GoalComplexity(strings.ToLower(string(complexity)))
		if complexity != orchestrator.ComplexitySimple && complexity != orchestrator.ComplexityComplex {
			return orchestrator.GoalAnalysis{}, fmt.Errorf("analyzer_llm: invalid complexity %q (want simple or complex)", *ra.Complexity)
		}

		rationale := ""
		if ra.Rationale != nil {
			rationale = *ra.Rationale
		}

		return orchestrator.GoalAnalysis{
			Kind:       kind,
			Complexity: complexity,
			Rationale:  rationale,
		}, nil
	}

	// 2. Fallback: parse lenient string heuristics if JSON fails.
	lower := strings.ToLower(response)

	// Infer kind from keywords.
	kind := orchestrator.KindAnswer // default
	if strings.Contains(lower, "coding") || strings.Contains(lower, "build") || strings.Contains(lower, "task") {
		kind = orchestrator.KindCoding
	}

	// Infer complexity from keywords.
	complexity := orchestrator.ComplexitySimple // default
	if strings.Contains(lower, "complex") || strings.Contains(lower, "multi") || strings.Contains(lower, "involved") {
		complexity = orchestrator.ComplexityComplex
	}

	return orchestrator.GoalAnalysis{
		Kind:       kind,
		Complexity: complexity,
		Rationale:  "parsed from lenient string heuristics",
	}, nil
}
