// Package skill is the general skill-system seam (ADR 066): a governed-capability
// layer above internal/recipe's execution strategies. A skill DECLARES which
// recipe.Recipe it executes through, plus its required permissions and gate checks;
// the recipe itself (unchanged) still owns the execution factories.
//
// This package is a strict stdlib-only leaf: it imports no other agent-builder
// internal package (not even internal/recipe), enforced by fitness function F-016.
// Keeping it a leaf lets the orchestrator or a future config/discovery loader compose
// it without a layering cycle.
//
// This is the seam only (ADR 066, task 176). No runtime path selects skills yet;
// task 177 registers coding-agent as a skill and wires SelectForGoal into dispatch.
package skill

// Manifest is a governed capability: the typed declaration of one skill. It carries
// the five concepts a skill needs, none of which live on a recipe.Recipe today: the
// human-facing name and description, the recipe it executes through (by registered
// name), its required permissions, and its gate checks. It owns no execution
// factories; those stay on the recipe RecipeName points at.
type Manifest struct {
	// Name is the unique registry key and human-facing skill name.
	Name string
	// Description is a short human-facing summary, also matched by SelectForGoal.
	Description string
	// RecipeName is the internal/recipe.Register name this skill executes through.
	RecipeName string
	// RequiredPermissions declares the permissions this skill needs. Declared now,
	// enforced by a follow-on once a non-coding skill needs a distinct set (ADR 066).
	RequiredPermissions []string
	// GateChecks declares the gate checks this skill requires, distinct from the
	// recipe's own blocking GateFactory. Declared now, enforced by a follow-on.
	GateChecks []string
}
