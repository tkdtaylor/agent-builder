package orchestrator

import "github.com/tkdtaylor/agent-builder/internal/skill"

// init registers the built-in "coding-agent" skill (ADR 066, task 177). This is
// the first migration onto the internal/skill seam: coding-agent becomes a
// governed skill.Manifest whose declared RecipeName ("coding-agent") points at
// the unchanged recipe.Register("coding-agent", ...) registration in
// internal/runtime. Registration is an init side-effect, mirroring how
// internal/recipe/docsfix's gate is force-imported to trigger its init
// (internal/cli/cli.go); it fires simply by importing internal/orchestrator,
// which the CLI already does. Register's duplicate error is intentionally
// ignored: a second import-triggered registration in the same process is a
// harmless no-op.
//
// The RequiredPermissions/GateChecks values are documentation-shaped metadata in
// this task's scope: nothing enforces them yet. Enforcement is a future task,
// once a SECOND skill with a different permission/gate profile exists to
// differentiate against (ADR 066's re-evaluation trigger).
func init() {
	_ = skill.Register("coding-agent", skill.Manifest{
		Name:                "coding-agent",
		Description:         "contribute code changes to a target repo, the reference build's default capability",
		RecipeName:          "coding-agent",
		RequiredPermissions: []string{"repo-write", "branch-publish"},
		GateChecks:          []string{"build", "test", "lint", "dep-scan", "code-scanner"},
	})
}

// liveSkillRegistry snapshots the package-level skill registry into a plain map
// SelectForGoal can consume. It is the production source of the registry a
// StructuredPlanner resolves against when it carries no injected Skills fixture.
func liveSkillRegistry() map[string]skill.Manifest {
	names := skill.List()
	reg := make(map[string]skill.Manifest, len(names))
	for _, name := range names {
		m, err := skill.Select(name)
		if err != nil {
			continue
		}
		reg[name] = m
	}
	return reg
}

// resolveRecipeName maps a goal/sub-goal spec text to a recipe name through the
// skill registry (ADR 066 / task 177). skill.SelectForGoal keyword-matches the
// text against each registered Manifest, falling back to fallback when none
// matches; the selected Manifest.RecipeName is returned. The only error case is
// an unregistered fallback, which is a hard configuration error: it is surfaced,
// never silently swallowed into an empty recipe name.
//
// This replaces the previous direct assignment of DefaultRecipeName as the sole
// no-prefix recipe selector: DefaultRecipeName now flows in as the fallback
// parameter, one layer down.
func resolveRecipeName(text string, registry map[string]skill.Manifest, fallback string) (string, error) {
	m, err := skill.SelectForGoal(text, registry, fallback)
	if err != nil {
		return "", err
	}
	return m.RecipeName, nil
}
