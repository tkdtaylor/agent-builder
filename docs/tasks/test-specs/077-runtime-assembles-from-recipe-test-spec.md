# Test spec — Task 077: runtime.Run assembles from a recipe

**Linked task:** `docs/tasks/backlog/077-runtime-assembles-from-recipe.md`
**Written:** 2026-06-27
**Revised:** 2026-06-27 (post-review — this task now owns the coding-agent recipe
registration and the `SelectRecipe("coding-agent")` assertion; dropped the stale
import-graph assertion that `internal/runtime` stops importing concretes — it still
imports them, but via the recipe binding, not inline in `Run`; clarified the
structural property being tested in TC-077-03); 2026-06-27 (ADR 043 amendment —
coding-agent recipe carries `RoutingSpec` not `ExecutorFactory`; stub resolver
introduced; TC-077-01 updated; TC-077-07 added)
**Status:** ready

## Context

Task 076 provides the `Recipe` type, the `RoutingSpec`/`Sensitivity` value types, and
the registry mechanism. This task has two tightly-coupled responsibilities:

1. **Register `"coding-agent"`.** The coding-agent concrete bindings
   (`tasksource.New`, `newProductionGate`, `branchpub.NewGitHubCLI`) live in
   `internal/runtime` (or a sub-package it imports). The recipe carries a
   `RoutingSpec{MinCapability: 1}` (or whatever tier Claude is assigned) rather than
   an `ExecutorFactory` — per ADR 043. The registration call
   (`recipe.Register("coding-agent", ...)`) is made from `internal/runtime`'s `init()`
   or an explicit `RegisterBuiltins()` call that `Run` invokes.

2. **Refactor `runtime.Run` to be a thin assembler with a stub executor resolver.**
   Instead of constructing the concretes inline, `Run` calls
   `recipe.SelectRecipe(config.RecipeName)`, uses the recipe's seam factories, and
   resolves the recipe's `RoutingSpec` to a concrete `supervisor.Executor` via a
   **stub resolver** (`stubResolveExecutor` or similar) that returns
   `executor.NewClaudeCLI(...)` unconditionally. The stub carries a comment:
   `// stubResolver — replaced by registry+router in task 095`.

**Zero behavior change for the existing coding agent** is the primary acceptance
criterion. The env-var surface stays identical. All existing tests must pass without
modification. The stub resolver is the designed, explicit placeholder for the real
router; it is not a workaround.

## Requirements coverage

| Req ID     | Test cases           | Covered? |
|------------|----------------------|----------|
| REQ-077-01 | TC-077-01            | yes      |
| REQ-077-02 | TC-077-02, TC-077-03 | yes      |
| REQ-077-03 | TC-077-04, TC-077-05 | yes      |
| REQ-077-04 | TC-077-06            | yes      |
| REQ-077-05 | TC-077-07            | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-077-01 — SelectRecipe("coding-agent") returns a non-nil Recipe with RoutingSpec after this task

- **Requirement:** REQ-077-01
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/recipe_test.go` or `internal/runtime/runtime_test.go`

**Input:** After task 077 lands, call `recipe.SelectRecipe("coding-agent")`.

**Expected output:**
- Returns `(Recipe, nil)` — no error.
- The returned `Recipe` has non-nil `GoalSource`, `GateFactory`, and `ResultSink` fields.
- The returned `Recipe` has a non-zero `RoutingSpec` (at minimum `MinCapability >= 1`).
- There is NO `ExecutorFactory` field on the returned recipe (the type does not have
  that field — it was removed by the ADR 043 amendment in task 076).
- The recipe name field equals `"coding-agent"`.
- `recipe.ListRecipes()` includes `"coding-agent"`.

**Note:** This test was NOT part of task 076 (the recipe type/registry task). Task
076 left the registry empty at the end of the test. This task registers the real
coding-agent recipe and asserts it resolves. The stub resolver in `internal/runtime`
maps this recipe's `RoutingSpec` to `executor.NewClaudeCLI(...)` for now.

---

### TC-077-02 — Existing Phase 0/Phase 1 end-to-end test passes without modification

- **Requirement:** REQ-077-02
- **Level:** L5 (end-to-end acceptance harness)
- **Test file / harness:** `tests/e2e` (`TestPhase0EndToEndAcceptance`,
  `TestPhase1EndToEndAcceptance`)

**Input:** `go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'`

**Expected output:**
- Both tests pass without any change to the test files.
- The run record produced by the phase-0/1 fake harness still carries
  `containment=exec-sandbox` (or equivalent) and the task is marked done.
- No test helper file is modified; this is a pure behavioral-regression guard.

---

### TC-077-03 — runtime.Run no longer constructs coding-agent concretes inline

- **Requirement:** REQ-077-02
- **Level:** L2 (structural test — source inspection)
- **Test file / harness:** source code review (recorded in verify commit)

**Input:** Review `internal/runtime/run.go` post-task.

**Expected output:**
- `run.go` does NOT contain inline construction calls like `tasksource.New(...)`,
  `executor.NewClaudeCLI(...)`, `newProductionGate()`, or `branchpub.NewGitHubCLI(...)`
  inside the `Run` function body.
- Instead, `run.go` calls `recipe.SelectRecipe(config.RecipeName)` and uses the
  returned recipe's factories.
- `internal/runtime` CONTINUES to import `internal/tasksource`, `internal/executor`,
  `internal/gate`, and `internal/publisher` (they are still needed for the recipe
  binding). The structural change is the construction site, not the import set.

**Rationale:** This is a refactor assertion, not an import-graph assertion. The import
graph of `internal/runtime` is unchanged by this task (the concretes were always
imported; they just move from inline in `Run` to a recipe factory). The invariant
being tested is that `Run` no longer hardwires "coding-agent behavior" inline — it
dispatches through the recipe.

---

### TC-077-04 — An unknown recipe name returns an error before dispatch

- **Requirement:** REQ-077-04
- **Level:** L2 (unit test)
- **Test file:** `tests/cli/run_wiring_test.go`

**Input:** Set `AGENT_BUILDER_RECIPE=does-not-exist` (or pass the name via Config)
and call `runtime.Run`.

**Expected output:**
- `Run` returns a non-nil error before creating any sandbox box.
- The error message names the unrecognized recipe.
- No audit events are emitted (the supervisor is never constructed).

---

### TC-077-05 — runtime.Run with recipe="coding-agent" behaves identically to before

- **Requirement:** REQ-077-02
- **Level:** L2 (unit test)
- **Test file:** `tests/cli/run_wiring_test.go` (extend existing)

**Input:** Call `runtime.Run(ctx, config)` with `config` populated the same way as
today (same env vars, same fake-launcher injection via `AGENT_BUILDER_EXEC_BOX_LAUNCHER`).

**Expected output:**
- The supervisor dispatches using the coding-agent seam concretes (fake-launcher
  injection still works — `AGENT_BUILDER_EXEC_BOX_LAUNCHER` is still honored).
- No new env var is required to select the coding-agent recipe.
- The run record matches the pre-refactor run record format.

---

### TC-077-06 — AGENT_BUILDER_RECIPE env var defaults to "coding-agent"

- **Requirement:** REQ-077-03
- **Level:** L2 (unit test)
- **Test file:** `tests/cli/run_wiring_test.go`

**Input:** `runtime.ConfigFromEnv()` with `AGENT_BUILDER_RECIPE` unset.

**Expected output:**
- `Config.RecipeName` equals `"coding-agent"`.
- `runtime.Run` with this config behaves identically to the pre-refactor behavior.

---

### TC-077-07 — Stub resolver exists, is named, routes any RoutingSpec to Claude; zero-drift check

- **Requirement:** REQ-077-05
- **Level:** L2 (structural test — source inspection + e2e regression)
- **Test file / harness:** source code inspection (recorded in verify commit) + e2e harness

**Input A (source inspection):** Review `internal/runtime/` post-task.

**Expected output A:**
- A function named `stubResolveExecutor` (or a clearly-named equivalent) exists in
  `internal/runtime` package.
- The function takes a `recipe.RoutingSpec` (or equivalent) and returns a
  `supervisor.Executor` (or equivalent seam value).
- The function body calls `executor.NewClaudeCLI(...)` unconditionally.
- The function carries a source comment containing the text `task 095` (its planned
  replacement task) so the replacement site is unambiguous.

**Input B (zero-drift e2e):** Run the same Phase-0 and Phase-1 e2e tests as before
the task:
```
go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
```

**Expected output B:**
- Both tests pass without any modification to the test files.
- The behavior is identical to the pre-refactor run: same run record format, same
  containment label, same audit events.

**Note:** This is the load-bearing "zero-drift" check that proves the stub resolver
introduces no behavioral regression. The ADR 043 cluster of tasks (087–095) does not
change behavior for the coding-agent recipe — only task 095 does, and it carries its
own zero-drift assertion.

---

## Verification plan

- **Highest level achievable:** L5 — the existing end-to-end acceptance harness
  exercises the live `runtime.Run` path through the new recipe assembler (TC-077-02).
- **L2 harness command:**
  ```
  go test -count=1 ./internal/recipe/... ./internal/runtime/... ./tests/cli/...
  ```
  Expected: `ok` on all packages; `SelectRecipe("coding-agent")` resolves.
- **L5 harness command:**
  ```
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  ```
  Expected: both pass, no test files modified.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Changing any env var name or semantics (pure refactor).
- Adding a second recipe (task 079).
- The runtime gate-existence assertion for generated recipes (task 078).
- Any change to `internal/tasksource`, `internal/executor`, `internal/gate`, or
  `internal/publisher` packages — they remain untouched; only their construction
  site moves from inline in `Run` to a recipe factory + stub resolver.
- Making `internal/runtime`'s import graph smaller — it still imports all four
  concrete packages. That import-graph cleanup is a separate concern and NOT a
  goal of this refactor.
- The real registry+router that replaces the stub resolver — that is task 095. The
  stub is the intentional, named placeholder.
