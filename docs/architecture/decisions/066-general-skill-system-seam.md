# ADR 066 — General skill system: skill is a governed capability, recipe is an execution strategy

**Status:** accepted
**Date:** 2026-07-12

**Motivated by:** the forward arc (roadmap "Forward arc — the general agent", item 2) and `AGENTS.md`'s framing that "coding is one skill among many" both call for a general, self-extending skill system. Today the agent has exactly one hardcoded capability, `orchestrator.DefaultRecipeName = "coding-agent"`, and no layer above `internal/recipe` that declares what a capability requires, what governs it, or how one is selected for a goal. Before the agent can carry more than one skill, the relationship between a "skill" and the existing "recipe" seam must be decided.

## Context

`internal/recipe` (task 095) is a flat registry of **execution strategies**: each `recipe.Recipe` owns the `GoalSourceFactory`, `GateFactory`, and `ResultSink` factories that assemble a worker run. It has no governance layer: no declared required permissions, no gate checks distinct from a recipe's own blocking `GateFactory`, and no selection logic beyond a single hardcoded default recipe name in the orchestrator.

Two shapes were considered for adding skills:

- **Skill replaces recipe.** A single richer type that carries both the execution factories AND the governance metadata. Rejected: it collapses two concerns (how a capability runs vs. what governs and selects it) into one type, breaks the existing `internal/recipe` seam that `internal/runtime` already composes over, and forces a large migration before any second skill exists to justify it.
- **Skill wraps recipe (chosen).** A skill is a thin governance/selection layer that DECLARES which existing `recipe.Recipe` it executes through, leaving `internal/recipe` completely unchanged.

This follows the project's "defer premature decisions" principle: with exactly one real capability (coding), the selection rule cannot yet be validated against a second, so v1 keeps it deliberately simple and pure.

## Decision

Adopt the **skill-wraps-recipe** relationship:

- **A skill is a governed capability.** It DECLARES which `recipe.Recipe` (by registered name) it executes through, plus its required permissions and gate checks. It does not own execution factories.
- **A recipe is the execution strategy itself, unchanged.** It still owns the `GoalSource`/`Gate`/`ResultSink` factories. `internal/recipe` is not modified by this arc.

This task (176) builds the seam and this decision record only: a new stdlib-only leaf package `internal/skill` with a typed `Manifest`, a `Register`/`Select`/`List` registry, and a pure `SelectForGoal(goalText, registry, fallback)` v1 selection rule (case-insensitive substring match against each manifest's name/description, sorted-key iteration for determinism, fallback when no match). No behavior migration: no existing package's behavior changes, and no runtime path selects skills yet. **Task 177 does the first migration**, registering `coding-agent` as a skill and wiring `SelectForGoal` into the dispatch path.

`internal/skill` is a strict stdlib-only leaf (it does not import `internal/recipe`, `internal/orchestrator`, or any other agent-builder internal package), enforced by a new fitness function F-016, mirroring F-012 (`internal/memoryguard`) and F-015 (`internal/runstore`). Keeping it a leaf is what lets the orchestrator (or a future config/discovery loader) compose it without a layering cycle.

**Register returns an error, unlike `recipe.Register` which panics.** Recipe registration happens at compile-time `init()` on a fixed, developer-authored set, where a duplicate is a programming bug worth crashing on. Skill registration is expected to eventually happen from config/discovery (the schedule-file precedent, task 175), where a duplicate or bad entry is an operator input error that must surface as a clean startup failure, not a daemon crash.

## Consequences

- The agent gains a typed governance/selection layer above execution strategies without touching `internal/recipe` or the running dispatch path. The seam is unconnected until task 177, matching how `internal/runstore` (task 167) shipped before its task-168 wiring.
- **Re-evaluation trigger:** the v1 `SelectForGoal` keyword/fallback rule is a deliberate placeholder. The first time a **second** skill beyond coding demonstrates that substring keyword matching mis-selects (ambiguous goals, overlapping keywords, or a need for capability-tier/permission-aware routing), that reopens `SelectForGoal`'s design, likely toward the goal-analyzer/router path (ADR 060/043) rather than a flat keyword scan. That is a new ADR, not a rewrite of the seam.
- Required-permissions and gate-checks fields are declared now but not yet enforced by any gate; enforcement is a follow-on once a skill other than coding needs a distinct permission set. Declaring them now keeps the `Manifest` shape stable for that follow-on.
