package orchestrator

import (
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// GoalKind is what the orchestrator decides a goal is (ADR 060): a general
// question to answer, or a coding task to plan and dispatch.
type GoalKind string

const (
	// KindAnswer is a general (non-coding) goal — answered via the single-shot
	// Completer and returned over the channel, no worker/gate/branch.
	KindAnswer GoalKind = "answer"
	// KindCoding is a build/act goal — clarified, planned, gated, and dispatched.
	KindCoding GoalKind = "coding"
)

// GoalComplexity is a coarse difficulty estimate used to pick the brain-capability
// floor the router selects within (ADR 060 / ADR 043).
type GoalComplexity string

const (
	// ComplexitySimple → capability floor 1 (local/cheap brains eligible).
	ComplexitySimple GoalComplexity = "simple"
	// ComplexityComplex → a higher capability floor (stronger brains).
	ComplexityComplex GoalComplexity = "complex"
)

// GoalAnalysis is the result of classifying a goal (ADR 060).
type GoalAnalysis struct {
	Kind       GoalKind
	Complexity GoalComplexity
	Rationale  string // short, human-readable; surfaced for audit/report
}

// GoalAnalyzer classifies an incoming goal so the orchestrator can route it
// (answer vs dispatch) and size the brain to its complexity. Mirrors the
// Clarifier seam (ADR 058).
type GoalAnalyzer interface {
	Analyze(goal supervisor.Task) (GoalAnalysis, error)
}

// HeuristicGoalAnalyzer is the deterministic, rule-based analyzer (no LLM, no IO).
// It is the seam's floor; the LLM analyzer (task 140) improves accuracy.
type HeuristicGoalAnalyzer struct{}

// NewHeuristicGoalAnalyzer constructs a HeuristicGoalAnalyzer.
func NewHeuristicGoalAnalyzer() *HeuristicGoalAnalyzer { return &HeuristicGoalAnalyzer{} }

// Compile-time assertion: *HeuristicGoalAnalyzer satisfies GoalAnalyzer.
var _ GoalAnalyzer = (*HeuristicGoalAnalyzer)(nil)

// interrogatives are the leading words that mark a spec as a question.
var interrogatives = map[string]bool{
	"what": true, "who": true, "whom": true, "whose": true, "when": true,
	"where": true, "why": true, "how": true, "which": true, "is": true,
	"are": true, "do": true, "does": true, "can": true, "could": true,
	"should": true, "would": true,
}

// codeBuildVerbs are the leading verbs that mark a spec as a coding task.
var codeBuildVerbs = map[string]bool{
	"build": true, "implement": true, "create": true, "add": true, "write": true,
	"fix": true, "refactor": true, "debug": true, "update": true, "patch": true,
	"remove": true, "delete": true, "make": true,
}

// Analyze implements GoalAnalyzer.Analyze. Rules are applied in order (ADR 060):
// repo/path → coding; question → answer; code-build verb → coding; else answer.
func (a *HeuristicGoalAnalyzer) Analyze(goal supervisor.Task) (GoalAnalysis, error) {
	spec := strings.TrimSpace(goal.Spec)
	complexity := complexityOf(spec)

	lower := strings.ToLower(spec)
	firstWord := ""
	if fields := strings.Fields(lower); len(fields) > 0 {
		firstWord = strings.Trim(fields[0], ".,;:!?")
	}

	switch {
	case namesRepoOrPath(spec):
		return GoalAnalysis{Kind: KindCoding, Complexity: complexity, Rationale: "names a repo or path"}, nil
	case strings.HasSuffix(spec, "?") || interrogatives[firstWord]:
		return GoalAnalysis{Kind: KindAnswer, Complexity: complexity, Rationale: "reads as a question"}, nil
	case codeBuildVerbs[firstWord]:
		return GoalAnalysis{Kind: KindCoding, Complexity: complexity, Rationale: "starts with a code-build verb"}, nil
	default:
		return GoalAnalysis{Kind: KindAnswer, Complexity: complexity, Rationale: "no repo/path or build verb — treated as a question"}, nil
	}
}

// namesRepoOrPath reports whether the spec references a repository or file path.
func namesRepoOrPath(spec string) bool {
	l := strings.ToLower(spec)
	return strings.Contains(l, "github.com") ||
		strings.Contains(l, "gitlab.com") ||
		strings.HasSuffix(l, ".git") ||
		strings.Contains(spec, "/")
}

// complexityOf estimates goal complexity: multi-line, long, or multi-step → complex.
func complexityOf(spec string) GoalComplexity {
	if nonBlankLineCount(spec) >= 2 {
		return ComplexityComplex
	}
	if len(strings.Fields(spec)) > 30 {
		return ComplexityComplex
	}
	l := strings.ToLower(spec)
	if strings.Contains(l, " and then ") || strings.Contains(l, " then ") {
		return ComplexityComplex
	}
	return ComplexitySimple
}

// nonBlankLineCount counts non-blank lines in spec.
func nonBlankLineCount(spec string) int {
	n := 0
	for _, line := range strings.Split(spec, "\n") {
		if strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
