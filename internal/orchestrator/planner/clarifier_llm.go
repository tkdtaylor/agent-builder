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

// LLMClarifier is an LLM-backed Clarifier that sends goal text to a model
// via the Invoker seam to verify specification readiness (REQ-131-01).
type LLMClarifier struct {
	resolver ExecutorResolver
	invoke   Invoker
}

// NewLLMClarifier constructs an LLMClarifier. Resolver and invoker are required.
func NewLLMClarifier(resolver ExecutorResolver, invoke Invoker) *LLMClarifier {
	if resolver == nil {
		panic("planner.NewLLMClarifier: nil ExecutorResolver")
	}
	if invoke == nil {
		panic("planner.NewLLMClarifier: nil Invoker")
	}
	return &LLMClarifier{
		resolver: resolver,
		invoke:   invoke,
	}
}

// Clarify delegates evaluation to the model via resolve/invoke (REQ-131-01).
func (c *LLMClarifier) Clarify(goal supervisor.Task) (orchestrator.Clarification, error) {
	ctx := context.Background()

	// Decompose capability floor is 1 (comprehension, same as planner)
	entry, err := c.resolver.Resolve(ctx, router.RoutingSpec{
		MinCapability:   1,
		SensitivityHint: router.SensitivityNone,
	})
	if err != nil {
		return orchestrator.Clarification{}, fmt.Errorf("llm_clarifier: resolve model: %w", err)
	}

	prompt := c.buildPrompt(goal.Spec)
	response, err := c.invoke(ctx, entry, prompt)
	if err != nil {
		return orchestrator.Clarification{}, fmt.Errorf("llm_clarifier: invoke model: %w", err)
	}

	return c.parse(response)
}

func (c *LLMClarifier) buildPrompt(goalText string) string {
	var b strings.Builder
	b.WriteString("Analyze the following goal specification to check if it has sufficient context to proceed to planning.\n")
	b.WriteString("Specifically, check if it specifies what to build and which repository to build it in.\n")
	b.WriteString("You MUST respond in this exact JSON format (and nothing else):\n")
	b.WriteString("{\n")
	b.WriteString("  \"ready\": true,\n")
	b.WriteString("  \"questions\": []\n")
	b.WriteString("}\n")
	b.WriteString("If it is not ready, set \"ready\" to false and provide one or more clear clarifying questions in \"questions\".\n")
	b.WriteString("Goal specification:\n")
	b.WriteString(goalText)
	b.WriteString("\n")
	return b.String()
}

type rawClarification struct {
	Ready     *bool    `json:"ready"`
	Questions []string `json:"questions"`
}

// parse extracts Clarification.Ready and Clarification.Questions (REQ-131-02).
func (c *LLMClarifier) parse(response string) (orchestrator.Clarification, error) {
	trimmed := strings.TrimSpace(response)
	if trimmed == "" {
		return orchestrator.Clarification{}, errors.New("llm_clarifier: empty model response")
	}

	// 1. Try to find a JSON block in the response if the model returned extra text
	start := strings.IndexByte(trimmed, '{')
	end := strings.LastIndexByte(trimmed, '}')
	if start != -1 && end != -1 && start < end {
		trimmed = trimmed[start : end+1]
	}

	var rc rawClarification
	if err := json.Unmarshal([]byte(trimmed), &rc); err == nil && rc.Ready != nil {
		return orchestrator.Clarification{
			Ready:     *rc.Ready,
			Questions: rc.Questions,
		}, nil
	}

	// 2. Fallback lenient string checks for non-JSON responses
	lower := strings.ToLower(response)
	if strings.Contains(lower, `"ready": true`) || strings.Contains(lower, "ready: true") || (strings.Contains(lower, "ready") && !strings.Contains(lower, "not ready") && !strings.Contains(lower, "questions")) {
		return orchestrator.Clarification{
			Ready:     true,
			Questions: nil,
		}, nil
	}

	// If not ready, extract lines starting with hyphen or bullet points, or ending with a question mark
	var questions []string
	lines := strings.Split(response, "\n")
	for _, l := range lines {
		line := strings.TrimSpace(l)
		if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
			q := strings.TrimSpace(strings.TrimLeft(line, "-* "))
			if q != "" {
				questions = append(questions, q)
			}
		} else if strings.HasSuffix(line, "?") {
			questions = append(questions, line)
		}
	}

	if len(questions) == 0 {
		questions = []string{"Clarifying questions needed."}
	}

	return orchestrator.Clarification{
		Ready:     false,
		Questions: questions,
	}, nil
}
