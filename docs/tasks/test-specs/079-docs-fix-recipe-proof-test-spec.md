# Test spec — Task 079: Docs-fix recipe (second proof recipe)

**Linked task:** `docs/tasks/backlog/079-docs-fix-recipe-proof.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 041 requires a second, deliberately-trivial recipe to prove the seam is genuine
rather than a coding agent in a costume. If a docs-fix recipe cannot be expressed
without touching `runtime` internals, the seam is wrong and ADR 041 has failed its
own test.

The docs-fix recipe:
- **Goal source:** a list of doc lint findings (or a hardcoded "fix this file" goal)
  — NOT `internal/tasksource` (which reads a roadmap).
- **Executor:** the same harness (Claude CLI or equivalent) with a docs-editing
  system prompt instead of a coding system prompt.
- **Gate:** a non-Go predicate — a markdown linter and/or link-checker plus the
  existing `code-scanner`. This gate must have a different implementation type than
  the production Go gate.
- **Result sink:** the same branch+PR publisher as recipe #1.

The recipe must register via `recipe.SelectRecipe("docs-fix")` and the assembler must
accept it (gate-existence assertion must pass for it).

## Requirements coverage

| Req ID     | Test cases           | Covered? |
|------------|----------------------|----------|
| REQ-079-01 | TC-079-01, TC-079-02 | yes      |
| REQ-079-02 | TC-079-03            | yes      |
| REQ-079-03 | TC-079-04            | yes      |
| REQ-079-04 | TC-079-05            | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-079-01 — SelectRecipe("docs-fix") returns a non-nil Recipe with a non-Go gate

- **Requirement:** REQ-079-01
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/docsfix/docsfix_test.go`

**Input:** `recipe.SelectRecipe("docs-fix")`

**Expected output:**
- Returns `(Recipe, nil)`.
- The returned `Recipe.GateFactory` produces a gate whose type is distinct from
  `internal/gate.ProductionGate` (or whatever the coding-agent gate type is named).
- The recipe name field equals `"docs-fix"`.
- `recipe.ListRecipes()` includes `"docs-fix"`.

---

### TC-079-02 — The docs-fix gate runs a markdown/link check, not Go tooling

- **Requirement:** REQ-079-01
- **Level:** L2 (unit test)
- **Test file:** `internal/recipe/docsfix/docsfix_test.go`

**Input:** Construct the docs-fix gate and call `Verify(ctx, worktreePath)` against
a fixture directory containing:
- A well-formed `*.md` file → expects PASS.
- A `*.md` file with a broken `[link](http://localhost:99999)` or a deliberately
  malformed heading → expects FAIL with a non-empty error describing the doc issue.

**Expected output (well-formed fixture):**
- `Verdict.OK == true`.

**Expected output (malformed fixture):**
- `Verdict.OK == false`.
- `Verdict.Failures` is non-empty and names the doc issue.

**Invariant:** The gate does NOT invoke `go build`, `go test`, or `golangci-lint`.
The test asserts no subprocess named `go` is spawned (stub-subprocess check or
import-graph assertion that the docs gate does not import `internal/gate/gosteps`
or equivalent).

---

### TC-079-03 — The docs-fix recipe shares containment block-wiring with the coding-agent recipe

- **Requirement:** REQ-079-02
- **Level:** L2 / L3 (structural test)
- **Test file / harness:** source inspection + `go list -deps`

**Input:** Inspect the docs-fix recipe definition and the coding-agent recipe
definition.

**Expected output:**
- Both recipes reference the same block-wiring config fields (exec-sandbox backend,
  vault, policy, audit) — neither recipe has its own containment implementation.
- The docs-fix recipe's `BlockWiring` config is either shared by reference with the
  coding-agent recipe's or is identically-typed.
- `go list -deps ./internal/recipe/docsfix/...` does NOT contain
  `github.com/tkdtaylor/agent-builder/internal/sandbox` directly (containment is
  block-wiring, not an IO seam the recipe owns).

---

### TC-079-04 — runtime.Run with recipe="docs-fix" passes the gate-existence assertion

- **Requirement:** REQ-079-03
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_gate_assert_test.go`

**Input:** Call the runtime assembler with `recipe="docs-fix"`.

**Expected output:**
- The gate-existence assertion (task 078) passes — the docs-fix gate is non-nil
  and recognized as a real blocking gate.
- The assembler proceeds to supervisor construction (or a pre-dispatch check returns
  no error).

---

### TC-079-05 — The seam test: adding docs-fix recipe touches zero runtime internals

- **Requirement:** REQ-079-04
- **Level:** L2 (structural test — diff / source inspection)
- **Test file:** code review (documented in verify commit)

**Input:** Review the diff for task 079.

**Expected output:**
- `internal/runtime/run.go` has zero new lines (the assembler is unchanged).
- `internal/supervisor/*.go` has zero new lines.
- New code lives only in `internal/recipe/docsfix/` (or equivalent recipe sub-package)
  and the recipe registry.
- This is the ADR 041 self-test: if `runtime` must change to add the docs-fix recipe,
  the seam is wrong. The verify commit must record this as pass/fail evidence.

---

## Verification plan

- **Highest level achievable:** L3 — the docs-fix recipe's gate has a runtime-
  observable surface (it actually runs a linter); the unit test with a fixture
  directory exercises it end-to-end.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/recipe/... ./internal/runtime/...
  ```
  Expected: `ok` on both packages.
- **Seam self-test (TC-079-05):**
  ```
  git diff HEAD~1 -- internal/runtime/ internal/supervisor/
  ```
  Expected: empty diff (zero lines changed in those directories for this task's
  commit).
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- A live end-to-end docs-fix run against a real target repository (that would
  require a provisioned host; L5/L6 for this task is the unit test of the gate
  against a fixture, not a real agent run).
- Implementing the goal source as a real doc-lint result scanner (a hardcoded "fix
  this file" goal is acceptable for the proof recipe).
- Changing `internal/gate` — the docs-fix gate is a new implementation behind the
  existing `Gate` interface, not a modification of existing gate steps.
