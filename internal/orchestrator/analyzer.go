package orchestrator

import (
	"context"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Answerer answers a general (non-coding) goal in a single shot and returns the
// text (ADR 060). It is the Completer-backed seam the orchestrator calls for a
// KindAnswer goal; it is wired in internal/cli so internal/orchestrator never
// imports internal/executor (F-010/F-014). capabilityTier is the model-capability
// floor the router selects within (ADR 061 §4): the value the analyzer emitted in
// GoalAnalysis.CapabilityTier. A zero tier means "unset" — the wiring falls back to
// its own default floor. Passing the tier directly (not the complexity) keeps
// GoalAnalysis.CapabilityTier the single source of the capability floor, so the
// answer route cannot drift from a second complexity→tier mapping.
type Answerer interface {
	Answer(ctx context.Context, prompt string, capabilityTier int) (string, error)
}

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
//
// CapabilityTier is the model-capability floor the goal requires (ADR 061 §4), the
// single source the routing spec's MinCapability is built from — the router (ADR
// 043) then picks the cheapest eligible entry at or above it. The rubric:
//
//	1 — simple / mechanical goal
//	2 — complex goal (no design/security signal)
//	3 — design / architecture / security / concurrency / cryptography goal
//	0 — unset / ambiguous → the wiring falls back to defaultMinCapability
//
// The heuristic analyzer derives it from Complexity plus a small design/security
// keyword set; the LLM analyzer emits it directly (authoritative where available)
// and falls back to the heuristic on malformed output.
type GoalAnalysis struct {
	Kind           GoalKind
	Complexity     GoalComplexity
	CapabilityTier int    // required model-capability floor; 0 = unset (fall back to default)
	Rationale      string // short, human-readable; surfaced for audit/report
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
// The capability tier (ADR 061 §4) is derived from complexity plus a design/security
// keyword bump (see tierOf) and attached to every result.
func (a *HeuristicGoalAnalyzer) Analyze(goal supervisor.Task) (GoalAnalysis, error) {
	spec := strings.TrimSpace(goal.Spec)
	complexity := complexityOf(spec)
	tier := tierOf(spec, complexity)

	lower := strings.ToLower(spec)
	firstWord := ""
	if fields := strings.Fields(lower); len(fields) > 0 {
		firstWord = strings.Trim(fields[0], ".,;:!?")
	}

	switch {
	case namesRepoOrPath(spec):
		return GoalAnalysis{Kind: KindCoding, Complexity: complexity, CapabilityTier: tier, Rationale: "names a repo or path"}, nil
	case strings.HasSuffix(spec, "?") || interrogatives[firstWord]:
		return GoalAnalysis{Kind: KindAnswer, Complexity: complexity, CapabilityTier: tier, Rationale: "reads as a question"}, nil
	case codeBuildVerbs[firstWord]:
		return GoalAnalysis{Kind: KindCoding, Complexity: complexity, CapabilityTier: tier, Rationale: "starts with a code-build verb"}, nil
	default:
		return GoalAnalysis{Kind: KindAnswer, Complexity: complexity, CapabilityTier: tier, Rationale: "no repo/path or build verb — treated as a question"}, nil
	}
}

// designSecuritySubstrings are the (lowercased) markers that bump a goal to the top
// capability tier (3): design/architecture, security, concurrency, and cryptography
// work is where the strongest model earns its keep (ADR 061). Kept small and
// substring-matched so morphological variants (secure/security, concurren{t,cy},
// crypto/cryptography) all trip the same rule.
var designSecuritySubstrings = []string{
	"security", "secure", "auth", "crypto",
	"architecture", "design",
	"concurren", "distributed",
}

// tierOf maps a goal to its required model-capability floor (ADR 061 §4 rubric):
// simple → 1, complex → 2, bumped to 3 when a design/security signal is present.
// It never returns 0 for the heuristic path — a heuristic classification always has
// a definite complexity, so the tier is always known; 0 (unset) is reserved for the
// LLM path when it emits no tier and there is no fallback (never on this path).
func tierOf(spec string, complexity GoalComplexity) int {
	if hasDesignOrSecuritySignal(spec) {
		return 3
	}
	if complexity == ComplexityComplex {
		return 2
	}
	return 1
}

// hasDesignOrSecuritySignal reports whether the spec carries a design/architecture,
// security, concurrency, or cryptography marker (case-insensitive substring match).
func hasDesignOrSecuritySignal(spec string) bool {
	l := strings.ToLower(spec)
	for _, kw := range designSecuritySubstrings {
		if strings.Contains(l, kw) {
			return true
		}
	}
	return false
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
