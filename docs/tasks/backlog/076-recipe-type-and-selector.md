# Task 076: Recipe type + in-process selector

**Project:** agent-builder
**Created:** 2026-06-27
**Revised:** 2026-06-27 (post-review — split coding-agent registration into task 077;
resolved leaf-purity contradiction)
**Status:** backlog

## Goal

Introduce the `Recipe` Go type and the in-process registry (`Register`, `SelectRecipe`,
`ListRecipes`) in package `internal/recipe`. Define the two new seam interfaces that
have no prior home (`GoalSource`, `ResultSink`) inside this package. Reference
`supervisor.Executor` and `supervisor.Gate` for the other two seam types (those
interfaces already live in `internal/supervisor`, which is import-clean against
concretes).

**`internal/recipe` must be a true leaf.** The coding-agent concrete registration
(which requires importing `internal/tasksource`, `internal/executor`, `internal/gate`,
and `internal/publisher`) is NOT part of this task — it belongs in task 077, where
`internal/runtime` already imports those concretes. This task proves only that the
`Recipe` type and registry mechanism work, using a fake recipe registered inside the
test file.

## Context

ADR 041 defines the agent-recipe seam. The four IO seams are already Go interfaces
in various packages; the blocker to a second agent is that `runtime.Config` hardwires
the coding concretes inline. This task creates the `Recipe` type and registry as the
foundation that tasks 077–079 and all downstream clusters depend on.

### Allowed imports for `internal/recipe`

`internal/supervisor` is allowed: it defines `Executor` and `Gate` as interfaces and
is itself clean against concretes (`go list -deps ./internal/supervisor/...` shows
only `internal/audit`, `internal/gate`, `internal/sandbox`). `GoalSource` and
`ResultSink` are defined in `internal/recipe` itself — they have no prior home.

Forbidden concrete imports: `internal/runtime`, `internal/tasksource`,
`internal/executor`, `internal/publisher`, `internal/vault`, `internal/policy`,
`internal/secrets`.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                         | Priority  |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-076-01 | A `Recipe` Go type (package `internal/recipe`) with fields for the four IO seams: `GoalSource` (new interface defined here), `ExecutorFactory` (factory returning `supervisor.Executor`), `GateFactory` (factory returning `supervisor.Gate`), `ResultSink` (new interface defined here), plus `BlockWiring` config. A `Recipe` with a nil `GateFactory` is rejected at construction with an error (or panic). `internal/recipe` imports only `internal/supervisor` plus stdlib. | must have |
| REQ-076-02 | `SelectRecipe(name string) (Recipe, error)` — returns the named recipe or a descriptive error. `SelectRecipe("")` and `SelectRecipe("unknown-name")` both return non-nil errors naming the problem. | must have |
| REQ-076-03 | `Register(name string, factory RecipeFactory)` — panics (or errors loudly) if the same name is registered twice; deterministic and loud, not last-writer-wins. | must have |
| REQ-076-04 | `ListRecipes() []string` returns the set of registered recipe names in stable, deterministic order. | must have |

## Readiness gate

- [x] Test spec `076-recipe-type-and-selector-test-spec.md` exists (written first, post-review revision)
- [ ] `make check` green on main before starting
- [ ] ADR 041 read and understood
- [ ] `go list -deps ./internal/supervisor/...` confirmed import-clean (no concretes)

## Acceptance criteria

- [ ] [REQ-076-01] TC-076-01: `Recipe` constructed with test-local fakes for all four seam fields compiles and returns valid value; nil `GateFactory` → constructor error/panic naming the defect
- [ ] [REQ-076-01] TC-076-02: `go list -deps ./internal/recipe/...` — `internal/supervisor` allowed; none of `internal/runtime`, `internal/tasksource`, `internal/executor`, `internal/publisher`, `internal/vault`, `internal/policy`, `internal/secrets` present
- [ ] [REQ-076-02, REQ-076-03] TC-076-03: `SelectRecipe("")` → error; `SelectRecipe("does-not-exist")` → error naming the name; duplicate `Register` call → panic/error (deterministic); `"coding-agent"` is NOT yet registered (that is task 077)
- [ ] [REQ-076-02, REQ-076-03] TC-076-04: `Register("test-fake", ...)` + `SelectRecipe("test-fake")` → `(Recipe, nil)` with non-nil seam fields; two calls produce independent values
- [ ] [REQ-076-04] TC-076-05: `ListRecipes()` returns stable-ordered slice; `"coding-agent"` is NOT in the list (coding-agent registration is task 077)

## Verification plan

- **Highest level achievable:** L3 — the recipe package has no runtime-observable
  surface of its own. Compile + unit tests + import-graph check are the verification.
- **Harness command:**
  ```
  go test -count=1 ./internal/recipe/...
  go list -deps ./internal/recipe/...
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/recipe`
  - `go list` → `internal/supervisor` present; no concrete package imports
  - `make check` → `All checks passed.`

## Out of scope

- Registering the `"coding-agent"` recipe with real concretes (task 077).
- Making `runtime.Run` read from the recipe (task 077).
- Any concrete seam implementation — this task defines only the `Recipe` type, the
  `GoalSource`/`ResultSink` interfaces, and the registry mechanism.
- A second recipe implementation (task 079).
- Runtime assembly-time gate-existence assertion (task 078).

## Dependencies

- None (this is the first Cluster A task and the critical-path root).
- Informs: tasks 077, 078, 079 (all Cluster A); tasks 080–086 (all downstream clusters).
