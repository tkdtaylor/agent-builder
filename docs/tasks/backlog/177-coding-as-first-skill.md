# Task 177: register `coding-agent` as the first governed skill; route goal->skill selection through the registry

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Register the existing `coding-agent` recipe as a `skill.Manifest`, and route
the orchestrator's goal-to-recipe-name resolution through
`skill.SelectForGoal` before it reaches `recipe.SelectRecipe`, so
`orchestrator.DefaultRecipeName`'s role as the sole, unmediated selection
mechanism becomes the skill registry's fallback parameter instead.

## Context

Task 176 built `internal/skill` with zero callers. This is the migration ADR
066 named as the first: `coding-agent` becomes a governed skill, not just a
registered recipe. This is a routing change, not a recipe change:
`internal/recipe`'s `newCodingAgentRecipe`/`Register`/`SelectRecipe` are
completely unmodified; this task adds one `skill.Manifest` pointing at the
same recipe name and changes where the orchestrator's `RecipeName` decision
comes from.

**Reference:**
- `internal/runtime/run.go:310` (`recipe.Register("coding-agent",
  newCodingAgentRecipe)`, unmodified, the registration this task's
  `skill.Manifest` points at via `RecipeName`)
- `internal/orchestrator/orchestrator.go:63-65` (`DefaultRecipeName`, the
  constant that becomes the registry's fallback parameter)
- `internal/orchestrator/analyzer.go` (the likely home of the current
  goal-to-recipe-name mapping this task redirects through `skill.SelectForGoal`,
  read it to find the exact call site before editing)
- Task 176 (`internal/skill.Manifest`/`Register`/`SelectForGoal`, consumed
  unmodified)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-177-01 | `coding-agent` registered as a `skill.Manifest` pointing at the existing recipe. | must have |
| REQ-177-02 | Orchestrator's recipe-name resolution calls `skill.SelectForGoal` and uses its result. | must have |
| REQ-177-03 | Single-skill production registry is a true behavioral no-op. | must have |
| REQ-177-04 | `recipe.SelectRecipe`/the recipe layer completely unmodified. | must have |
| REQ-177-05 | Spec/doc footprint describes the new goal->skill->recipe chain. | must have |
| REQ-177-06 | Pre-existing suites pass unchanged apart from the registration/routing addition. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/177-coding-as-first-skill-test-spec.md` exists (written first)
- [x] Task 176 merged (`internal/skill` exists)
- [x] Task 095 merged (`coding-agent` recipe registered)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. Find the exact current goal-to-recipe-name mapping site: read
   `internal/orchestrator/analyzer.go` and `internal/orchestrator/orchestrator.go`'s
   `Plan`/`SubGoal` construction path (grep `DefaultRecipeName`) to identify
   the one or two call sites where a `SubGoal.RecipeName` field is currently
   set. Do not guess, this task's implementation MUST edit the real call
   site(s), not add a parallel unused one.
2. Registration site (new small file, e.g.
   `internal/orchestrator/skills.go`, or alongside the existing
   `recipe.Register` call in `internal/runtime/run.go`, executor's choice
   based on which keeps `internal/orchestrator`'s import graph cleanest per
   F-010): an `init()` (or an explicit `RegisterDefaultSkills()` function
   called once at CLI assembly, mirroring how `docsfix.DocsFixGate{}` is
   force-imported today, `internal/cli/cli.go:16`, executor's choice, document
   which) calling:
   ```go
   _ = skill.Register("coding-agent", skill.Manifest{
       Name:        "coding-agent",
       Description: "contribute code changes to a target repo, the reference build's default capability",
       RecipeName:  "coding-agent",
       RequiredPermissions: []string{"repo-write", "branch-publish"},
       GateChecks:  []string{"build", "test", "lint", "dep-scan", "code-scanner"},
   })
   ```
   (Permission/gate-check string values at the executor's discretion, matching
   whatever taxonomy makes sense given the gate steps this recipe's
   `GateFactory` already runs, `internal/gate`; they are documentation-shaped
   metadata in this task's scope, not yet enforced against anything, that
   enforcement is a future task once a SECOND skill with different
   permissions exists to differentiate against.)
3. At the identified call site(s) from step 1, replace the direct
   `DefaultRecipeName` assignment with:
   ```go
   registered := map[string]skill.Manifest{} // built from skill.List()+skill.Select, or a direct registry-snapshot helper, executor's choice
   for _, name := range skill.List() {
       m, _ := skill.Select(name)
       registered[name] = m
   }
   m, err := skill.SelectForGoal(goalText, registered, DefaultRecipeName)
   if err != nil {
       // fallback itself unregistered is a hard configuration error; should
       // never happen in production given step 2's registration, but must
       // fail loud, not silently default to an empty RecipeName
   }
   subGoal.RecipeName = m.RecipeName
   ```
4. Tests per the test spec, in particular TC-177-04's real-production-registry
   no-op proof, the load-bearing acceptance evidence.
5. Update `docs/spec/architecture.md`/`interfaces.md` per REQ-177-05.

## Acceptance criteria

- [ ] [REQ-177-01] TC-177-01: `coding-agent` registered as a skill.
- [ ] [REQ-177-02] TC-177-02/03: resolution calls `SelectForGoal`, result flows into `SubGoal`.
- [ ] [REQ-177-03] TC-177-04: single-skill production registry is a true no-op (load-bearing).
- [ ] [REQ-177-04] TC-177-05: `recipe.SelectRecipe` unaffected.
- [ ] [REQ-177-05] TC-177-06: spec/doc footprint updated.
- [ ] [REQ-177-06] TC-177-07: `go test -race -count=1 ./internal/orchestrator/... ./internal/recipe/... ./internal/skill/... ./internal/runtime/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L2/L3, TC-177-04's real-production-registry
  no-op regression proof (the strongest achievable evidence short of a live
  `orchestrate` run that this task changes no observable behavior today).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... ./internal/skill/... -run TestTC177
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** a live `agent-builder orchestrate` run
  against a real goal still dispatches to the `coding-agent` recipe exactly
  as before this task.

## Spec/doc footprint (update in the feat commit)

- `docs/spec/architecture.md`: orchestrator components section describes the
  goal -> skill -> recipe resolution chain, cross-references ADR 066.
- `docs/spec/interfaces.md`: the Tier-1 orchestrator's `SubGoal.RecipeName`
  resolution entry updated to name `skill.SelectForGoal` as the resolution
  mechanism, `DefaultRecipeName` documented as the registry's fallback
  parameter, not the sole mechanism.

## Out of scope

- Registering any second skill.
- Any change to `internal/recipe`.
- LLM-driven or config-driven skill selection.
- Enforcing `RequiredPermissions`/`GateChecks` against anything (metadata only
  in this task's scope).

## Dependencies

- **Blocks on:** task 176.
- **Blocks:** none (the last task in this batch).
