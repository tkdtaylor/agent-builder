# Task 077: runtime.Run assembles from a recipe

**Project:** agent-builder
**Created:** 2026-06-27
**Revised:** 2026-06-27 (post-review — this task now owns the coding-agent recipe
registration and the `SelectRecipe("coding-agent")` assertion, moved from task 076)
**Status:** backlog

## Goal

Two things in one task (tightly coupled — they share the same concrete imports):

1. **Register the coding-agent recipe.** In `internal/runtime` (or a sub-package
   `internal/recipe/codingagent` imported by `internal/runtime`), register a recipe
   named `"coding-agent"` that binds `tasksource.New`, `executor.NewClaudeCLI`,
   `newProductionGate`, and `branchpub.NewGitHubCLI`. This is the right home for the
   registration: `internal/runtime` already imports all four concretes; the recipe
   leaf (`internal/recipe`) does not and must not.

2. **Make `runtime.Run` a thin assembler.** Call `recipe.SelectRecipe(config.RecipeName)`
   and use the recipe's seam factories to construct the supervisor, instead of
   constructing the coding-agent concretes inline.

Zero behavior change: all env vars, all acceptance tests, all fitness checks pass
without modification.

## Context

ADR 041 requires `runtime` to become a thin assembler so that adding a second agent
requires zero changes to `runtime`. Today `runtime.Run` hardwires `tasksource.New`,
`executor.NewClaudeCLI`, `newProductionGate`, and `branchpub.NewGitHubCLI` directly.
This refactor moves the binding of those concretes into a recipe, while keeping the
binding site inside `internal/runtime` (the only package that already imports them).

The coding-agent registration belongs here — not in `internal/recipe` (which must
stay leaf-pure). `internal/recipe` knows only about the registry mechanism and the
seam interface types; the concrete bindings live one level up in `internal/runtime`.

## Requirements

| Req ID     | Description                                                                                                                                                               | Priority  |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-077-01 | The coding-agent recipe is registered under `"coding-agent"` (via `recipe.Register` called from `internal/runtime`'s `init()` or an explicit `RegisterBuiltins()` call). `recipe.SelectRecipe("coding-agent")` returns a non-nil Recipe with non-nil seam factories. | must have |
| REQ-077-02 | `runtime.Run` calls `recipe.SelectRecipe(config.RecipeName)` and constructs the supervisor from the recipe's seam factories; all existing Phase-0 and Phase-1 e2e tests pass without modification. | must have |
| REQ-077-03 | `runtime.Config` gains a `RecipeName` field; `ConfigFromEnv()` reads `AGENT_BUILDER_RECIPE` and defaults to `"coding-agent"` when unset. | must have |
| REQ-077-04 | An unknown recipe name causes `runtime.Run` to return a descriptive error before any sandbox creation. No audit events are emitted. | must have |

## Readiness gate

- [x] Test spec `077-runtime-assembles-from-recipe-test-spec.md` exists (written first, post-review revision)
- [ ] Task 076 merged (`internal/recipe` package: type, registry mechanism, `GoalSource`/`ResultSink` interfaces — but NOT the coding-agent registration)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-077-01] TC-077-01: `recipe.SelectRecipe("coding-agent")` returns a non-nil Recipe with non-nil factories for all four seams; `recipe.ListRecipes()` includes `"coding-agent"` after this task
- [ ] [REQ-077-02] TC-077-02: `go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'` passes without modifying the test files
- [ ] [REQ-077-02] TC-077-03: `runtime.Run` with `recipe="coding-agent"` produces the same supervisor behavior as before (fake-launcher injection still works); `run.go` no longer constructs concretes inline (source inspection)
- [ ] [REQ-077-03] TC-077-05: `ConfigFromEnv()` with `AGENT_BUILDER_RECIPE` unset → `Config.RecipeName == "coding-agent"`
- [ ] [REQ-077-04] TC-077-06: `AGENT_BUILDER_RECIPE=does-not-exist` → `Run` returns error naming the unknown recipe before any sandbox creation; no audit events emitted

## Verification plan

- **Highest level achievable:** L5 — the existing end-to-end harness exercises the
  live `runtime.Run` path through the new recipe assembler (TC-077-01).
- **Harness command:**
  ```
  go test -count=1 ./internal/recipe/... ./internal/runtime/...
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  make check
  ```
  Expected:
  - `SelectRecipe("coding-agent")` test → `ok`
  - e2e → both pass, no test files modified
  - `make check` → `All checks passed.`

## Out of scope

- Changing any env var name or semantics.
- Adding a second recipe (task 079).
- Runtime gate-existence assertion for generated recipes (task 078).
- Modifying `internal/tasksource`, `internal/executor`, `internal/gate`, or
  `internal/publisher` — those packages are unchanged; only their construction
  site moves into the recipe binding.

## Dependencies

- Task 076 (recipe type + registry mechanism) — must be merged before this task starts.
- Informs: tasks 078, 079 (remaining Cluster A); all downstream clusters.
