package orchestrator

import (
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Clarification is the result of a Clarifier call (ADR 056).
type Clarification struct {
	Ready     bool
	Questions []string
}

// Clarifier decomposes a goal's specification to check if it has sufficient context
// to proceed to planning, returning questions if not.
type Clarifier interface {
	Clarify(goal supervisor.Task) (Clarification, error)
}

// HeuristicClarifier is a deterministic, rule-based Clarifier v1 (no LLM, no IO).
type HeuristicClarifier struct{}

// NewHeuristicClarifier constructs a HeuristicClarifier.
func NewHeuristicClarifier() *HeuristicClarifier {
	return &HeuristicClarifier{}
}

// Clarify implements Clarifier.Clarify.
func (c *HeuristicClarifier) Clarify(goal supervisor.Task) (Clarification, error) {
	trimmed := strings.TrimSpace(goal.Spec)
	if trimmed == "" {
		return Clarification{
			Ready:     false,
			Questions: []string{"What would you like me to build?"},
		}, nil
	}

	// Check if contains repository
	hasRepo := strings.Contains(trimmed, "github.com") ||
		strings.Contains(trimmed, "gitlab.com") ||
		strings.Contains(trimmed, "/")

	// Extract action part by ignoring repository words
	words := strings.Fields(trimmed)
	var nonRepoWords []string
	for _, w := range words {
		isRepo := strings.Contains(w, "github.com") ||
			strings.Contains(w, "gitlab.com") ||
			strings.Contains(w, "/")
		if !isRepo {
			nonRepoWords = append(nonRepoWords, w)
		}
	}
	actionClean := strings.TrimSpace(strings.Join(nonRepoWords, " "))
	actionClean = strings.TrimPrefix(actionClean, "in")
	actionClean = strings.TrimPrefix(actionClean, "on")
	actionClean = strings.TrimPrefix(actionClean, "for")
	actionClean = strings.TrimSpace(actionClean)

	if !hasRepo {
		return Clarification{
			Ready:     false,
			Questions: []string{"Which repository should I work on?"},
		}, nil
	}

	if len(actionClean) < 3 {
		return Clarification{
			Ready:     false,
			Questions: []string{"What would you like me to build in the repository?"},
		}, nil
	}

	return Clarification{
		Ready: true,
	}, nil
}
