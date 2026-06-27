# Test spec — Task 076: Recipe type + in-process selector

**Linked task:** `docs/tasks/backlog/076-recipe-type-and-selector.md`
**Written:** 2026-06-27
**Revised:** 2026-06-27 (post-review — split coding-agent registration into task 077;
resolved leaf-purity contradiction; clarified allowed imports); 2026-06-27 (ADR 043
amendment — executor seam field changed from `ExecutorFactory` to `RoutingSpec` value
type; TC-076-01 and TC-076-02 updated accordingly; `REQ-076-01a` added)
**Status:** ready

## Context

This task introduces the `Recipe` type and the registry (`Register`, `SelectRecipe`,
`ListRecipes`) in package `internal/recipe`. The package must be a true leaf: it
defines the seam interface types itself (for the two seams that have no existing home
— `GoalSource` and `ResultSink`) and references `supervisor.Gate` for the gate type
(that interface already lives in `internal/supervisor`, which is itself import-clean
against concretes).

**ADR 043 amendment (applied before implementation):** the executor seam field is
NOT an `ExecutorFactory`. Per ADR 043, a recipe declares a `RoutingSpec` value type —
`MinCapability int` + `SensitivityHint Sensitivity` — rather than binding a concrete
executor factory. The router (tasks 092/095) resolves `RoutingSpec` at dispatch.
This preserves leaf-purity exactly as ADR 041 requires: `internal/recipe` still
imports no registry, router, or executor concrete.

The coding-agent registration (which requires importing `internal/tasksource`,
`internal/executor`, `internal/gate`, and `internal/publisher`) is NOT part of this
task. Task 076 proves only that the `Recipe` type and registry mechanism work, using
a **fake recipe registered inside the test file**. Moving the real coding-agent
registration to task 077 is what makes 076 a true leaf.

### Why `internal/supervisor` is an allowed import

`internal/supervisor` defines `Gate` as an interface and imports only
`internal/audit`, `internal/gate` (for the `Verdict` type), and `internal/sandbox`.
It does NOT import `internal/executor`, `internal/tasksource`, or `internal/publisher`.
Its import graph (`go list -deps ./internal/supervisor/...`) confirms it is clean
against concretes.

`internal/recipe` may import `internal/supervisor` (for the `Gate` interface type)
without pulling in any concrete seam implementation. The allowed import set for
`internal/recipe` is: `internal/supervisor` (interface only — `Gate`) plus stdlib.
`GoalSource`, `ResultSink`, `RoutingSpec`, and `Sensitivity` are all defined in
`internal/recipe` itself.

Note: `supervisor.Executor` is no longer referenced from `internal/recipe` — the old
`ExecutorFactory` field is replaced by `RoutingSpec`. The forbidden import set
additionally includes `internal/router` and `internal/registry` (which do not yet
exist but must never be imported from the leaf).

## Requirements coverage

| Req ID      | Test cases           | Covered? |
|-------------|----------------------|----------|
| REQ-076-01  | TC-076-01, TC-076-02 | yes      |
| REQ-076-01a | TC-076-01, TC-076-02 | yes      |
| REQ-076-02  | TC-076-03            | yes      |
| REQ-076-03  | TC-076-04            | yes      |
| REQ-076-04  | TC-076-05            | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-076-01 — Recipe type compiles with all four seam fields present; nil Gate is rejected; RoutingSpec round-trips

- **Requirement:** REQ-076-01, REQ-076-01a
- **Level:** L2 (compile-time + unit test)
- **Test file:** `internal/recipe/recipe_test.go`

**Input:** Construct a `Recipe` value (or call `recipe.New(...)`) using:
- `GoalSource`: a test-local type implementing `recipe.GoalSource`
- `RoutingSpec`: `recipe.RoutingSpec{MinCapability: 2, SensitivityHint: recipe.SensitivitySensitive}`
- `GateFactory`: a test-local factory returning a type implementing `supervisor.Gate`
- `ResultSink`: a test-local type implementing `recipe.ResultSink`

Note: there is NO `ExecutorFactory` field. The executor seam is replaced by `RoutingSpec`.

**Expected output:**
- The package compiles without error.
- A `Recipe` constructed with all four fields is a valid, non-zero value.
- `recipe.RoutingSpec` is a value type with exported `MinCapability int` and
  `SensitivityHint Sensitivity` fields.
- `recipe.Sensitivity` has at least two values: `SensitivityNone` and `SensitivitySensitive`.
- `r.RoutingSpec.MinCapability == 2` and `r.RoutingSpec.SensitivityHint == SensitivitySensitive`
  after construction (no field silently dropped).
- A zero-value `RoutingSpec{}` is accepted without error (MinCapability=0, SensitivityHint=SensitivityNone).

**Edge cases:**
- `recipe.New(...)` with a nil `GateFactory` returns an error (or panics with a
  named reason) before the `Recipe` value is returned. The test verifies this path.
- The test does NOT call `recipe.SelectRecipe("coding-agent")` — that assertion
  belongs to task 077.

---

### TC-076-02 — internal/recipe is a leaf: no concrete seam package imports, no router/registry imports

- **Requirement:** REQ-076-01, REQ-076-01a
- **Level:** L3 (import-graph)
- **Test file / harness:** `go list -deps ./internal/recipe/...`

**Input:** `go list -deps ./internal/recipe/...`

**Expected output:**
- The output contains `github.com/tkdtaylor/agent-builder/internal/recipe`.
- The output may contain `github.com/tkdtaylor/agent-builder/internal/supervisor`
  (allowed — it holds the `Gate` interface type; it is itself clean against concretes).
- The output does NOT contain any of:
  - `github.com/tkdtaylor/agent-builder/internal/runtime`
  - `github.com/tkdtaylor/agent-builder/internal/tasksource`
  - `github.com/tkdtaylor/agent-builder/internal/executor`
  - `github.com/tkdtaylor/agent-builder/internal/publisher`
  - `github.com/tkdtaylor/agent-builder/internal/vault`
  - `github.com/tkdtaylor/agent-builder/internal/policy`
  - `github.com/tkdtaylor/agent-builder/internal/secrets`
  - `github.com/tkdtaylor/agent-builder/internal/router` (future; must not be imported)
  - `github.com/tkdtaylor/agent-builder/internal/registry` (future; must not be imported)
- `go list` exits 0.

**Rationale for the allowed set:** `internal/supervisor` carries `Gate` as an
interface; it does not import any concrete seam. The `ExecutorFactory` field that
used to reference `supervisor.Executor` is gone — replaced by `RoutingSpec`. The
forbidden set is extended with `internal/router` and `internal/registry` to prevent
the leaf from ever directly depending on the executor-selection layer.

---

### TC-076-03 — Registry errors: empty name, unknown name, duplicate name

- **Requirement:** REQ-076-02, REQ-076-03
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/recipe_test.go`

**Input A:** `recipe.SelectRecipe("")`
**Expected output A:** Returns a non-nil error naming the empty string as invalid.

**Input B:** `recipe.SelectRecipe("does-not-exist")`
**Expected output B:** Returns a non-nil error naming `"does-not-exist"` as unrecognized.

**Input C:** Two calls to `recipe.Register("same-name", ...)` in an `init()`
function or via a test-scoped registration.
**Expected output C:** Panics with a message naming `"same-name"` as already
registered (or the second `Register` call returns a non-nil error — implementation
picks; the test asserts the behavior is deterministic and loud, not last-writer-wins).

**Note:** At this task's completion, no real recipe name is registered. The registry
is empty except for names registered during the test itself. `SelectRecipe("coding-agent")`
is NOT tested here — that assertion lives in task 077.

---

### TC-076-04 — Register + SelectRecipe round-trip with a fake recipe

- **Requirement:** REQ-076-02, REQ-076-03
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/recipe_test.go`

**Input:** In the test, call `recipe.Register("test-fake", fakeRecipeFactory)` where
`fakeRecipeFactory` returns a `Recipe` with all seam fields populated — `GoalSource`,
`RoutingSpec{MinCapability:1}`, `GateFactory`, `ResultSink` — and no `ExecutorFactory`.
Then call `recipe.SelectRecipe("test-fake")`.

**Expected output:**
- `SelectRecipe("test-fake")` returns `(Recipe, nil)`.
- The returned `Recipe` has non-nil `GoalSource`, non-zero `RoutingSpec`, non-nil
  `GateFactory`, non-nil `ResultSink` fields.
- There is NO `ExecutorFactory` field on the returned recipe.
- The recipe name field equals `"test-fake"`.

**Edge case:** Two calls to `SelectRecipe("test-fake")` return independent, non-
shared `Recipe` values (each factory call produces a fresh value).

---

### TC-076-05 — ListRecipes returns the registered set in stable order

- **Requirement:** REQ-076-04
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/recipe_test.go`

**Input:** Register two fake recipes (`"test-alpha"`, `"test-beta"`) in the test.
Call `recipe.ListRecipes()` twice.

**Expected output:**
- The returned slice contains exactly the names registered in this test (or more, if
  a package-level `init` registered others — but NOT `"coding-agent"`, since that
  registration belongs to task 077).
- Two calls return slices in the same order (deterministic; alphabetical or
  registration-order — either is acceptable).

**Important:** If the registry is global and tests run in the same process, test
isolation must be ensured (e.g. a test-only `ResetRegistry()` function for cleanup,
or separate test binaries). The spec test must document which isolation approach is used.

---

## Verification plan

- **Highest level achievable:** L3 — the recipe package has no runtime-observable
  surface of its own. Compile + unit tests + import-graph check are the verification.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/recipe/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/recipe`
- **L3 import-graph check:**
  ```
  go list -deps ./internal/recipe/...
  ```
  Expected: `internal/supervisor` present (allowed); none of `internal/runtime`,
  `internal/tasksource`, `internal/executor`, `internal/publisher`,
  `internal/vault`, `internal/policy`, `internal/secrets` in the output.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Registering the `"coding-agent"` recipe binding real concretes (that is task 077).
- Making `runtime.Run` read from the recipe (task 077).
- Any concrete seam implementation (goal source, gate, etc.) — this task only defines
  the `Recipe` type, the seam interfaces (`GoalSource`, `ResultSink`), the
  `RoutingSpec` and `Sensitivity` value types, and the registry mechanism. There is no
  `ExecutorFactory` — it is replaced by `RoutingSpec`.
- A second real recipe implementation (task 079).
- Runtime assembly-time gate-existence assertion for generated recipes (task 078).
- The registry/router that resolves `RoutingSpec` to a concrete executor (tasks 087,
  092 in the ADR 043 cluster).
- Any harness adapter (Codex, Gemini, local — tasks 089, 090, 091).
