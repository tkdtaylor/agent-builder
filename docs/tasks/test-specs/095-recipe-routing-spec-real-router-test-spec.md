# Test spec — Task 095: Recipe RoutingSpec wired to the real router

**Linked task:** `docs/tasks/backlog/095-recipe-routing-spec-real-router.md`
**Written:** 2026-06-27
**Status:** ready

## Context

Task 077 introduced a `stubResolveExecutor` function in `internal/runtime` that maps
any recipe's `RoutingSpec` unconditionally to `executor.NewClaudeCLI(...)`. This task
replaces that stub with the real registry+router from tasks 087–093.

The load-bearing requirement is **zero-drift for the coding-agent recipe**: after this
task, `recipe.SelectRecipe("coding-agent")` still routes to the Claude executor (or
whichever entry wins the router's capability/cost selection with the coding-agent's
`RoutingSpec`), and all existing e2e tests pass without modification.

The stub's comment `// stubResolver — replaced by registry+router in task 095` marks
the replacement site.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-095-01 | TC-095-01, TC-095-02           | yes      |
| REQ-095-02 | TC-095-03                      | yes      |
| REQ-095-03 | TC-095-04, TC-095-05           | yes      |
| REQ-095-04 | TC-095-06                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-095-01 — Stub resolver is replaced: runtime uses registry+router

- **Requirement:** REQ-095-01
- **Level:** L2 (structural test — source inspection)
- **Test file / harness:** source code inspection (recorded in verify commit)

**Input:** Review `internal/runtime/` post-task.

**Expected output:**
- The `stubResolveExecutor` function (or its equivalent from task 077) no longer
  exists in `internal/runtime`.
- `internal/runtime` constructs a `router.Router` from `registry.LoadFromEnv()` and
  passes it to the supervisor (or wraps it as a `supervisor.Executor`).
- F-003 remains intact: `internal/supervisor` does not import `internal/router` or
  `internal/registry`.

---

### TC-095-02 — Coding-agent recipe routes to Claude executor (zero-drift check)

- **Requirement:** REQ-095-01
- **Level:** L5 (end-to-end acceptance)
- **Test file / harness:** `tests/e2e` + `go test`

**Input:**
```
go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
```

**Expected output:**
- Both tests pass without modification.
- The run record still shows `containment=exec-sandbox` (or equivalent) and the
  task is marked done.
- The coding-agent recipe's `RoutingSpec` resolves to the Claude executor via the
  real router (because Claude is the only configured entry in the CI env, where no
  `AGENT_BUILDER_REGISTRY_CODEX_*` or `AGENT_BUILDER_REGISTRY_GEMINI_*` vars are set).

**Rationale:** The zero-drift check is the primary acceptance gate. If the CI env
only has the Claude entry enabled, the router will select Claude regardless — same
behavior as the stub. The behavioral change only manifests when multiple entries are
configured.

---

### TC-095-03 — Multi-entry routing: cheapest eligible entry selected

- **Requirement:** REQ-095-02
- **Level:** L2 (unit test with fake registry)
- **Test file:** `internal/runtime/runtime_test.go` or `tests/cli/run_wiring_test.go`

**Input:** Construct a runtime with a fake registry containing:
- `{ID:"local", CapabilityTier:1, CostWeight:1, Availability:Available}`
- `{ID:"claude-oauth", CapabilityTier:3, CostWeight:10, Availability:Available}`

Call `runtime.Run` with the coding-agent recipe (`RoutingSpec{MinCapability:1}`).

**Expected output:**
- The router selects `"local"` (cheapest eligible).
- The fake local executor (stub subprocess) is invoked, not the Claude executor.
- The run record records which entry was selected.

---

### TC-095-04 — ErrNoEligibleExecutor surfaces before dispatch

- **Requirement:** REQ-095-03
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_test.go`

**Input:** Construct a runtime with an empty registry (no entries configured).
Call `runtime.Run` with the coding-agent recipe.

**Expected output:**
- `runtime.Run` returns a non-nil error containing `"no eligible executor"` (or
  equivalent) before any sandbox creation.
- No audit events are emitted.

---

### TC-095-05 — Unknown recipe name still errors before dispatch (regression)

- **Requirement:** REQ-095-03
- **Level:** L2 (unit test — regression from task 077)
- **Test file:** `tests/cli/run_wiring_test.go`

**Input:** `AGENT_BUILDER_RECIPE=does-not-exist` → `runtime.Run`.

**Expected output:**
- Returns error naming the unknown recipe before any sandbox creation.
- No audit events emitted.
- Behavior unchanged from task 077.

---

### TC-095-06 — F-003 preserved after stub replacement

- **Requirement:** REQ-095-04
- **Level:** L3 (fitness check)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after the stub is replaced.

**Expected output:**
- `PASS fitness-supervisor-isolation: …` (exit 0).
- `go list -deps ./internal/supervisor/...` does NOT contain `internal/router` or
  `internal/registry`.

---

## Verification plan

- **Highest level achievable:** L5 — existing e2e harness (TC-095-02) proves zero-drift.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/runtime/... ./tests/cli/...
  ```
  Expected: `ok` on all packages; zero-drift confirmed.
- **L5 harness command:**
  ```
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  ```
  Expected: both pass, no test files modified.
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
  Expected: `PASS fitness-supervisor-isolation: …`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Multi-entry live routing with real Codex or Gemini CLIs (L6, operator-run, requires
  provisioned API keys and CLIs).
- Persistent quota state across real dispatches (task 093 covers this; this task
  wires the router, which inherits the persistence from 093).
