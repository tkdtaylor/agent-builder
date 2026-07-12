# Test Spec 169: bounded re-plan loop with RunStore-persisted attempt budget and escalation

**Linked task:** [`docs/tasks/backlog/169-sustained-autonomy-loop.md`](../backlog/169-sustained-autonomy-loop.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Today, when a sub-goal's dispatch terminally fails (its own `runtime.Run`
attempts are exhausted, `internal/loop/retry_policy.go:205-219`'s
`markNeedsHuman`), the orchestrator's `dispatchPlan` records the failure in the
aggregated `PlanResult` and `Handle` reports it once; nothing re-plans or retries
at the GOAL level. ADR 065 names this task explicitly: "The sustained-autonomy
loop, approval pause/resume, daemon mode, and schedules all build on that journal
(tasks 169-171, 174-175)."

This task adds a bounded, RunStore-persisted, goal-level retry loop:
`orchestrator.RunToCompletion(ctx, goal supervisor.Task, maxAttempts int)
(PlanResult, error)` wraps `Handle` in a loop that, on a terminal sub-goal
failure, folds the failure detail into the goal text (reusing the EXISTING
`FoldGoalText` helper from task 115, `internal/orchestrator/orchestrator.go:752-767`,
originally built for pending-info amendments) and re-plans via the same
`Planner` seam, up to `maxAttempts` GOAL-LEVEL attempts (distinct from, and one
layer above, each sub-goal's own `runtime.Run` retry budget). On exhaustion, it
escalates over the `supervisor.Reporter` seam with a message naming the attempt
count, matching the existing escalation-message shape
`internal/loop/format_failure.go` already establishes for sub-goal-level
escalation.

The attempt counter is persisted via `runstore.Record` (task 167/168) so the
budget survives a crash mid-loop: a re-plan attempt already recorded is never
silently "forgotten" and re-attempted past the configured budget after a
restart.

**Module boundary:** `internal/orchestrator` only. Depends on task 167
(`runstore`) and task 168 (`WithRunStore`, the `Record` write/read discipline)
being present; this task adds no new package.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-169-01 | New env-configurable default, `AGENT_BUILDER_GOAL_MAX_ATTEMPTS` (int, default `3`), read by the `orchestrate` CLI assembly and threaded into `RunToCompletion`'s `maxAttempts` parameter. | TC-169-01 |
| REQ-169-02 | `RunToCompletion` calls `Handle` once; on a fully-successful `PlanResult` (no sub-goal terminal failures), it returns immediately without re-planning, attempt count `1`. | TC-169-02 |
| REQ-169-03 | On a `PlanResult` containing at least one terminal sub-goal failure, `RunToCompletion` folds the failure detail(s) into the goal text via `FoldGoalText` (reusing the existing helper unmodified) and calls `Handle` again with the folded goal, incrementing the attempt counter. | TC-169-03 |
| REQ-169-04 | The attempt counter is persisted to the goal's `runstore.Record` (a new field or reuse of an existing counter-shaped field, executor's choice, documented) after each attempt, so a fresh `RunToCompletion` call sharing the same `RunStore` and `GoalID` resumes counting from where a prior, crashed invocation left off rather than resetting to 1. | TC-169-04 |
| REQ-169-05 | On reaching `maxAttempts` with the goal still failing, `RunToCompletion` calls `supervisor.Reporter.Report` (or the orchestrator's existing reporting seam) with a message containing the goal ID, the attempt count, and the word `"exhausted"`, and returns the last `PlanResult` plus a non-nil sentinel-wrapped error (`ErrGoalAttemptsExhausted`). | TC-169-05 |
| REQ-169-06 | When `RunStore` is unset, `RunToCompletion` still functions (in-memory attempt counting only, no crash-survival), behavior of the loop mechanics unaffected, only the durability guarantee is absent. | TC-169-06 |
| REQ-169-07 | Pre-existing `internal/orchestrator` suites (`Handle`, `ConfirmAndPlan`, `dispatchPlan`) are unaffected, `RunToCompletion` is purely additive, calling the existing `Handle` unmodified. | TC-169-07 |

---

## Pre-implementation checklist

- [x] Task 167/168 merged (`runstore`, `WithRunStore`, per-attempt persistence)
- [x] Task 115 merged (`FoldGoalText` exists)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-169-01, env var wiring

- **Requirement:** REQ-169-01
- **Level:** L2 (unit test)
- **Test file:** `internal/cli/orchestrate_169_test.go` (new) or extend
  `internal/cli/orchestrate_test.go`

**Step:** Assemble `orchestrateConfig` with `AGENT_BUILDER_GOAL_MAX_ATTEMPTS`
unset, then set to `"5"`, then set to an invalid value (`"abc"`).

**Expected output:** unset -> default `3`; `"5"` -> `5`; `"abc"` -> a fail-fast
usage-config error (`errUsageConfig`, matching this codebase's established
malformed-integer-env convention).

---

### TC-169-02, immediate success needs no re-plan

- **Requirement:** REQ-169-02
- **Level:** L2

**Step:** `RunToCompletion(ctx, goal, 3)` with a fake `Planner`/`DispatchFunc`
that succeeds on the first call. Track how many times the fake `Planner` is
invoked.

**Expected output:** `Planner` invoked exactly once; returned `PlanResult`
reports full success; no `Reporter.Report` escalation call.

---

### TC-169-03, a terminal failure triggers a folded re-plan

- **Requirement:** REQ-169-03
- **Level:** L2

**Step:** Fake `Planner` returns a 1-sub-goal plan; fake `DispatchFunc` fails
(terminal) on attempt 1's sub-goal, succeeds on attempt 2's. `RunToCompletion(ctx,
goal, 3)`.

**Expected output:** `Planner` invoked twice; the SECOND invocation's received
goal text is verifiably different from the first (contains the folded failure
detail, assert via `strings.Contains` on a distinguishing substring the fake
dispatch failure carried, e.g. `"gate failure: lint"`); final `PlanResult`
reports success; attempt count `2`.

---

### TC-169-04, attempt counter survives a crash (persisted via RunStore)

- **Requirement:** REQ-169-04
- **Level:** L5 (two independently-constructed `Orchestrator`+`FileStore` pairs
  sharing one on-disk directory, mirroring TC-168-07)

**Step:** (1) `orch1` with `WithRunStore(store1)`, fake dispatch always fails
(terminal). Call `RunToCompletion(ctx, goal, 3)` but interrupt it (via a fake
`Planner` that panics/returns a sentinel "process died" error) after exactly 2
attempts have been recorded. (2) Construct `store2 := runstore.NewFileStore(sameDir)`,
`orch2` with `WithRunStore(store2)` and a fake dispatch that now succeeds. Call
`RunToCompletion(ctx, goal, 3)` again on `orch2`.

**Expected output:** `orch2`'s call succeeds on its FIRST internal `Handle`
attempt (attempt 3 overall, since 2 were already recorded by `orch1`) and
`orch2`'s fake `Planner` is invoked only ONCE (not reset to a fresh 3-attempt
budget), proving the counter is read from `store2`'s rehydrated `Record`, not
reset to zero by the new process. If `orch2`'s single remaining attempt (attempt
3) had ALSO failed, `RunToCompletion` would immediately report exhaustion
(0 further attempts left), a second assertion the test should also cover as a
sub-case.

---

### TC-169-05, exhaustion escalates over the Reporter

- **Requirement:** REQ-169-05
- **Level:** L2

**Step:** Fake `Planner`/`DispatchFunc` always terminally fails. `RunToCompletion(ctx,
goal, 2)` with a fake `supervisor.Reporter` that records `Report` calls.

**Expected output:** exactly 2 `Handle` attempts made; `Reporter.Report` called
exactly once at the end (not once per attempt) with a message containing the
goal ID, `"2"` (the attempt count), and `"exhausted"`; returned `error` satisfies
`errors.Is(err, orchestrator.ErrGoalAttemptsExhausted)`.

---

### TC-169-06, RunStore unset still functions (no durability, but correct loop mechanics)

- **Requirement:** REQ-169-06
- **Level:** L2 (regression-shaped)

**Step:** Re-run TC-169-02/03/05's exact scenarios WITHOUT `WithRunStore`.

**Expected output:** identical pass/fail outcomes and attempt counts to the
RunStore-configured runs (only the cross-process durability guarantee differs,
untestable without RunStore, and out of scope for this sub-case).

---

### TC-169-07, full regression

- **Requirement:** REQ-169-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, TC-169-04's two-independently-constructed
  orchestrator-plus-store crash-mid-loop proof.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... -run TestTC169
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/orchestrator/... -run TestTC169_04
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Sub-goal-level retry policy (`internal/loop.RetryPolicy`), unmodified,
  unrelated layer.
- Approval-pause interaction with the re-plan loop (task 170/171); a goal paused
  on `require_approval` mid-loop is out of scope for this task's exhaustion
  semantics (a follow-on question: does an approval pause consume an attempt?
  Task 170/171's own spec settles that when it lands).
- Wiring `RunToCompletion` into the daemon's inbound-message loop (task 174/175).
