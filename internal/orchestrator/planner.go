package orchestrator

import (
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// StructuredPlanner is the v1 rule-based Planner (ADR 046 §1 Option A). It
// imports no model seam and never reaches internal/executor: decomposition is
// fully deterministic.
//
// Structured-plan text format (autonomous default, ADR 046 §1): the goal text is
// read line by line. Each non-blank, non-comment line is one sub-goal of the form
//
//	<recipe-name>: <spec text>
//
// A line with no "<recipe>:" prefix uses the default recipe (DefaultRecipeName)
// with the whole line as the spec. Lines beginning with '#' are comments and are
// ignored, as are blank lines. If the goal text contains no parseable sub-goal
// line (e.g. a single free-form sentence), the whole goal text collapses into a
// single sub-goal on the default recipe.
type StructuredPlanner struct {
	// DefaultRecipe is the recipe a sub-goal uses when its line names no recipe.
	// Empty means DefaultRecipeName.
	DefaultRecipe string
	// KnownRecipes, when non-empty, is the set of recipe names the planner will
	// treat as a "<recipe>:" prefix. A "word:" prefix whose word is not in this
	// set is treated as part of the spec text (so free-form goals containing a
	// colon are not mis-split). When empty, any leading "<token>:" where token
	// has no spaces is treated as a recipe prefix.
	KnownRecipes map[string]bool
}

// NewStructuredPlanner constructs a StructuredPlanner with the given known recipe
// names. Passing no names disables the known-recipe guard (any "<token>:" prefix
// is treated as a recipe selector).
func NewStructuredPlanner(knownRecipes ...string) *StructuredPlanner {
	var known map[string]bool
	if len(knownRecipes) > 0 {
		known = make(map[string]bool, len(knownRecipes))
		for _, r := range knownRecipes {
			known[r] = true
		}
	}
	return &StructuredPlanner{
		DefaultRecipe: DefaultRecipeName,
		KnownRecipes:  known,
	}
}

// Plan decomposes the goal into an ordered plan (ADR 046 §1). It never calls a
// model and never errors on well-formed input.
func (p *StructuredPlanner) Plan(goal supervisor.Task) (Plan, error) {
	defRecipe := p.DefaultRecipe
	if defRecipe == "" {
		defRecipe = DefaultRecipeName
	}

	plan := Plan{
		Goal:   goal.Spec,
		GoalID: goal.ID,
	}

	lines := strings.Split(goal.Spec, "\n")
	idx := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		recipeName, spec := p.splitLine(line, defRecipe)
		plan.SubGoals = append(plan.SubGoals, SubGoal{
			RecipeName: recipeName,
			Task: supervisor.Task{
				ID:   subGoalID(goal.ID, idx),
				Repo: goal.Repo,
				Spec: spec,
			},
		})
		idx++
	}

	// No parseable sub-goal line: collapse the whole goal into one sub-goal on
	// the default recipe (ADR 046 §1).
	if len(plan.SubGoals) == 0 {
		plan.SubGoals = append(plan.SubGoals, SubGoal{
			RecipeName: defRecipe,
			Task: supervisor.Task{
				ID:   subGoalID(goal.ID, 0),
				Repo: goal.Repo,
				Spec: strings.TrimSpace(goal.Spec),
			},
		})
	}

	return plan, nil
}

// splitLine separates a "<recipe>: <spec>" line into a recipe name and spec text.
// When the line has no recognized recipe prefix, it returns the default recipe and
// the whole line as the spec.
func (p *StructuredPlanner) splitLine(line, defRecipe string) (recipeName, spec string) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return defRecipe, line
	}
	candidate := strings.TrimSpace(line[:colon])
	rest := strings.TrimSpace(line[colon+1:])
	// A recipe prefix must be a single token (no spaces) and, when a known-recipe
	// set is configured, must be in it.
	if candidate == "" || strings.ContainsAny(candidate, " \t") || rest == "" {
		return defRecipe, line
	}
	if p.KnownRecipes != nil && !p.KnownRecipes[candidate] {
		return defRecipe, line
	}
	return candidate, rest
}

// subGoalID derives a stable sub-goal Task ID from the goal ID and sub-goal index.
func subGoalID(goalID string, idx int) string {
	if goalID == "" {
		goalID = "goal"
	}
	return goalID + "-" + itoa(idx)
}

// itoa converts a small non-negative int to its decimal string without importing
// strconv (keeps the planner's import set minimal).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
