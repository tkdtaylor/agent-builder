# Task 176: ADR 066 + the `internal/skill` registry seam

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** completed

## Goal

Write ADR 066 recording the skill/recipe relationship decision (`skill =
governed capability`, `recipe = execution strategy`), and build
`internal/skill`, a new leaf package with a `Manifest` type, a
`Register`/`Select`/`List` registry, and pure `SelectForGoal` selection
semantics. No behavior migration in this task.

## Context

`docs/plans/roadmap.md`'s Forward arc item 2 and `AGENTS.md`'s "Coding is one
skill among many" framing both name this gap. Today `internal/recipe`'s
`Register`/`SelectRecipe` is a flat namespace of execution strategies with no
governance layer above it, no declared required permissions, no gate checks
distinct from a recipe's own `GateFactory`. `orchestrator.DefaultRecipeName =
"coding-agent"` is a single hardcoded default.

This task is deliberately ADR-plus-seam only, matching the "defer premature
decisions" design principle: build the typed seam and the decision record, let
task 177 do the first real migration once the seam exists to migrate onto.

**Reference:**
- `docs/plans/roadmap.md` Forward arc item 2
- `internal/recipe/recipe.go:121-175` (`Register`/`SelectRecipe`/`ListRecipes`,
  the sibling seam this ADR relates `Skill` to, structurally mirrored but NOT
  reused directly, `internal/skill` does not import `internal/recipe`)
- `internal/orchestrator/orchestrator.go:63-65` (`DefaultRecipeName`, the
  current single-default selection this seam will eventually replace, in
  task 177, not here)
- `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`
  (the numbering precedent, this task's ADR is 066)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-176-01 | ADR 066 records the skill/recipe relationship decision, no behavior migration. | must have |
| REQ-176-02 | `Manifest` type: name, description, declared recipe, required permissions, gate checks, all typed. | must have |
| REQ-176-03 | `Register` returns an error (not a panic) on a duplicate name. | must have |
| REQ-176-04 | `Select`/`List` with descriptive errors and deterministic ordering. | must have |
| REQ-176-05 | `SelectForGoal` v1 selection: pure, explicit-registry-parameter, deterministic keyword/fallback rule. | must have |
| REQ-176-06 | `internal/skill` is a strict leaf; new F-016 fitness function enforces it. | must have |
| REQ-176-07 | No existing package's behavior changes. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/176-skill-system-adr-and-seam-test-spec.md` exists (written first)
- [x] ADR 065 accepted (numbering precedent)
- [x] Task 095 merged (`internal/recipe`, the sibling seam)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. Write `docs/architecture/decisions/066-general-skill-system-seam.md`
   following this repo's existing ADR format (see ADR 065 for the current
   template shape: Status, Date, Motivated by, Context, Decision,
   Consequences). Content requirements: Context cites the roadmap's Forward
   arc item 2 and `AGENTS.md`'s "coding is one skill among many" framing;
   Decision states the relationship explicitly (`skill = governed capability
   that DECLARES which recipe.Recipe it executes through; recipe = the
   execution strategy itself, unchanged, still owns GoalSource/Gate/ResultSink
   factories`) and states this task (176) builds the seam only, task 177 does
   the first migration; Consequences names the re-evaluation trigger (e.g. "a
   second skill beyond coding demonstrates the v1 keyword-selection rule is
   insufficient" reopens `SelectForGoal`'s design).
2. New package `internal/skill`:
   - `manifest.go`:
     ```go
     type Manifest struct {
         Name                 string
         Description          string
         RecipeName           string   // the recipe.Register name this skill executes through
         RequiredPermissions  []string
         GateChecks           []string
     }
     ```
   - `registry.go`:
     ```go
     var (
         registryMu sync.RWMutex
         registry   = make(map[string]Manifest)
     )

     func Register(name string, m Manifest) error {
         registryMu.Lock()
         defer registryMu.Unlock()
         if _, exists := registry[name]; exists {
             return fmt.Errorf("skill.Register: skill %q is already registered", name)
         }
         registry[name] = m
         return nil
     }

     func Select(name string) (Manifest, error) { /* mirrors recipe.SelectRecipe's not-found error shape */ }
     func List() []string { /* sorted */ }
     ```
     Document explicitly in the package doc comment WHY `Register` returns an
     error here while `recipe.Register` panics: skill registration is
     expected to eventually happen from config/discovery (task 175's
     precedent), a panic there would crash the daemon on a bad config file
     rather than surfacing a clean startup error.
   - `select.go`:
     ```go
     func SelectForGoal(goalText string, registry map[string]Manifest, fallback string) (Manifest, error) {
         // v1: case-insensitive substring match of goalText against each
         // Manifest.Name/Description; first match wins (iterate registry in
         // sorted-key order for determinism); no match -> return
         // registry[fallback] (error if fallback itself is not registered).
     }
     ```
3. Add `fitness-skill-isolation` to `Makefile` (F-016, copy F-015's/F-012's
   grep pattern, substitute the package path) and a matching row in
   `docs/spec/fitness-functions.md`.
4. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-176-01] TC-176-01: ADR 066 exists, states the required decisions.
- [ ] [REQ-176-02] TC-176-02: `Manifest` shape, all five concepts typed.
- [ ] [REQ-176-03] TC-176-04/05: `Register` error-not-panic on duplicate, succeeds on unique.
- [ ] [REQ-176-04] TC-176-06/07: `Select` not-found error, `List` sorted.
- [ ] [REQ-176-05] TC-176-08/09: `SelectForGoal` keyword match and fallback.
- [ ] [REQ-176-06] TC-176-10: `make fitness-skill-isolation` passes.
- [ ] [REQ-176-07] TC-176-11: `internal/recipe`/`internal/orchestrator` unaffected.
- [ ] TC-176-12: `go test -race -count=1 ./internal/skill/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L3, this task ships an unconnected seam with
  no runtime caller (task 177 provides the first live wiring); L2 unit tests
  plus the new F-016 fitness check are correct and sufficient, matching how
  task 167's `internal/runstore` (also initially unconnected) was verified.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/skill/...
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/fitness-functions.md`: new F-016 row.
- `docs/spec/interfaces.md`: new `internal/skill` seam section.
- `docs/spec/architecture.md`: components section gains a one-line mention of
  the new (as-yet-unwired) skill registry, cross-referencing ADR 066.

## Out of scope

- Migrating `coding-agent` into a registered skill (task 177).
- Wiring `SelectForGoal` into the orchestrator's dispatch path (task 177).
- LLM-driven skill selection.
- The secure skill-writing loop.

## Dependencies

- **Blocks on:** ADR 065 (numbering precedent, already accepted), task 095
  (`internal/recipe`, already merged).
- **Blocks:** task 177.
