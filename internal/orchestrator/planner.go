package orchestrator

import (
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/skill"
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
	// Skills is the skill-registry snapshot a no-prefix goal/line is resolved
	// against via skill.SelectForGoal (ADR 066 / task 177). When nil, the live
	// package-level registry snapshot (liveSkillRegistry) is used, i.e. the
	// production path. Tests inject a fixture here to exercise multi-skill
	// routing without touching global registry state.
	Skills map[string]skill.Manifest
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
// model. It errors only on a hard configuration fault: an unregistered fallback
// skill (see resolveRecipeName), which cannot happen once the built-in
// coding-agent skill is registered (skills.go init).
func (p *StructuredPlanner) Plan(goal supervisor.Task) (Plan, error) {
	defRecipe := p.DefaultRecipe
	if defRecipe == "" {
		defRecipe = DefaultRecipeName
	}

	registry := p.Skills
	if registry == nil {
		registry = liveSkillRegistry()
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

		recipeName, spec, explicit := p.splitLine(line)
		if !explicit {
			// No explicit "<recipe>:" prefix: the recipe is chosen by routing the
			// goal/line text through the skill registry (ADR 066 / task 177),
			// with defRecipe as the fallback. This replaces the old direct
			// defRecipe assignment.
			resolved, err := resolveRecipeName(spec, registry, defRecipe)
			if err != nil {
				return Plan{}, err
			}
			recipeName = resolved
		}
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

	// No parseable sub-goal line: collapse the whole goal into one sub-goal, its
	// recipe resolved through the skill registry (ADR 046 §1 + ADR 066).
	if len(plan.SubGoals) == 0 {
		spec := strings.TrimSpace(goal.Spec)
		resolved, err := resolveRecipeName(spec, registry, defRecipe)
		if err != nil {
			return Plan{}, err
		}
		plan.SubGoals = append(plan.SubGoals, SubGoal{
			RecipeName: resolved,
			Task: supervisor.Task{
				ID:   subGoalID(goal.ID, 0),
				Repo: goal.Repo,
				Spec: spec,
			},
		})
	}

	return plan, nil
}

// splitLine separates a "<recipe>: <spec>" line into a recipe name and spec text.
// explicit reports whether the line named a recognized recipe prefix. When
// explicit is false the recipe name is left empty for the caller to resolve
// through the skill registry, and spec is the whole line.
func (p *StructuredPlanner) splitLine(line string) (recipeName, spec string, explicit bool) {
	colon := strings.IndexByte(line, ':')
	if colon <= 0 {
		return "", line, false
	}
	candidate := strings.TrimSpace(line[:colon])
	rest := strings.TrimSpace(line[colon+1:])
	// A recipe prefix must be a single token (no spaces) and, when a known-recipe
	// set is configured, must be in it.
	if candidate == "" || strings.ContainsAny(candidate, " \t") || rest == "" {
		return "", line, false
	}
	if p.KnownRecipes != nil && !p.KnownRecipes[candidate] {
		return "", line, false
	}
	return candidate, rest, true
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
