package orchestrator

// Task 177: coding-agent registered as the first governed skill; goal->recipe
// resolution routed through skill.SelectForGoal. Internal test package so it can
// exercise the unexported resolveRecipeName/liveSkillRegistry helpers directly.

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/skill"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// TC-177-01: coding-agent is registered as a skill by the package init().
func TestTC177_01_CodingAgentRegisteredAsSkill(t *testing.T) {
	m, err := skill.Select("coding-agent")
	if err != nil {
		t.Fatalf("skill.Select(\"coding-agent\"): %v (init registration did not run?)", err)
	}
	if m.RecipeName != "coding-agent" {
		t.Errorf("RecipeName = %q, want \"coding-agent\" (points at the unchanged recipe)", m.RecipeName)
	}
	if m.Description == "" {
		t.Error("Description is empty; skill manifest must carry a description")
	}
	if m.RequiredPermissions == nil {
		t.Error("RequiredPermissions is nil; want a non-nil (documentation-shaped) slice")
	}
	if m.GateChecks == nil {
		t.Error("GateChecks is nil; want a non-nil (documentation-shaped) slice")
	}
}

// twoSkillFixture is the TC-177-02/03 test registry: the production coding-agent
// plus a fake docs-fix skill whose Name keyword-matches the test goal text.
func twoSkillFixture() map[string]skill.Manifest {
	return map[string]skill.Manifest{
		"coding-agent": {Name: "coding-agent", Description: "contribute code changes to a target repo", RecipeName: "coding-agent"},
		"docs-fix":     {Name: "docs-fix", Description: "fix documentation drift", RecipeName: "docs-fix-recipe"},
	}
}

// TC-177-02: the resolution function actually calls SelectForGoal and uses its
// result (not a hardcoded constant). With a two-skill fixture and a goal text
// that keyword-matches docs-fix, resolution returns docs-fix's recipe name.
func TestTC177_02_ResolutionCallsSelectForGoal(t *testing.T) {
	reg := twoSkillFixture()
	got, err := resolveRecipeName("please docs-fix the stale README", reg, "coding-agent")
	if err != nil {
		t.Fatalf("resolveRecipeName: %v", err)
	}
	if got != "docs-fix-recipe" {
		t.Errorf("resolveRecipeName = %q, want \"docs-fix-recipe\" (proves it routed through SelectForGoal, not DefaultRecipeName)", got)
	}
	// No-match text falls back to the configured default, not docs-fix.
	fb, err := resolveRecipeName("something completely unrelated", reg, "coding-agent")
	if err != nil {
		t.Fatalf("resolveRecipeName fallback: %v", err)
	}
	if fb != "coding-agent" {
		t.Errorf("no-match resolution = %q, want \"coding-agent\" (the fallback)", fb)
	}
}

// TC-177-03: the resolved RecipeName flows into the constructed SubGoal via the
// real Plan path, using the injected two-skill fixture.
func TestTC177_03_ResolvedRecipeFlowsIntoSubGoal(t *testing.T) {
	p := &StructuredPlanner{
		DefaultRecipe: "coding-agent",
		Skills:        twoSkillFixture(),
	}
	plan, err := p.Plan(supervisor.Task{ID: "g1", Spec: "please docs-fix the stale README"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.SubGoals) != 1 {
		t.Fatalf("got %d sub-goals, want 1", len(plan.SubGoals))
	}
	if plan.SubGoals[0].RecipeName != "docs-fix-recipe" {
		t.Errorf("SubGoal.RecipeName = %q, want \"docs-fix-recipe\" (resolved skill's recipe reached the dispatch struct)", plan.SubGoals[0].RecipeName)
	}
}

// TC-177-04: with only coding-agent registered (the real production registry),
// every goal resolves to coding-agent regardless of text, including text that
// would have keyword-matched a hypothetical OTHER skill. Load-bearing no-op proof.
func TestTC177_04_SingleSkillRegistryIsNoOp(t *testing.T) {
	reg := liveSkillRegistry()
	if len(reg) != 1 {
		t.Fatalf("production registry has %d skills, want exactly 1 (coding-agent); got %v", len(reg), reg)
	}
	goals := []string{
		"add a retry to the IB client",
		"fix documentation drift in the operator guide", // would match a docs-fix skill if one existed
		"docs-fix the stale README",                     // would match by name if a docs-fix skill existed
		"research the best archival approach",           // would match a research skill if one existed
		"refactor the structured planner",
	}
	for _, g := range goals {
		got, err := resolveRecipeName(g, reg, DefaultRecipeName)
		if err != nil {
			t.Fatalf("resolveRecipeName(%q): %v", g, err)
		}
		if got != "coding-agent" {
			t.Errorf("resolveRecipeName(%q) = %q, want \"coding-agent\" (single-skill registry must never mis-route)", g, got)
		}
	}
}

// TC-177-04b: the whole Plan path is a no-op for a free-form goal under the real
// production registry (Skills nil -> liveSkillRegistry), confirming the inserted
// selection layer changes nothing observable while only one skill exists.
func TestTC177_04b_PlanNoOpUnderProductionRegistry(t *testing.T) {
	p := NewStructuredPlanner() // DefaultRecipe = DefaultRecipeName, Skills nil -> live registry
	plan, err := p.Plan(supervisor.Task{ID: "g2", Spec: "fix documentation drift in the operator guide"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if len(plan.SubGoals) != 1 || plan.SubGoals[0].RecipeName != DefaultRecipeName {
		t.Fatalf("got %d sub-goals with recipe %v, want 1 on %q (byte-identical to pre-task behavior)",
			len(plan.SubGoals), recipeNamesOf(plan), DefaultRecipeName)
	}
}

func recipeNamesOf(p Plan) []string {
	out := make([]string, 0, len(p.SubGoals))
	for _, s := range p.SubGoals {
		out = append(out, s.RecipeName)
	}
	return out
}
