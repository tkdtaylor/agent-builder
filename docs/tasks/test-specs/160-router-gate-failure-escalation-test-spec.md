# Test Spec 160: wire `Router.OnGateFailure` into the live retry/escalation hook

**Linked task:** [`docs/tasks/backlog/160-router-gate-failure-escalation.md`](../backlog/160-router-gate-failure-escalation.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

`resolveExecutor` (`internal/runtime/run.go:445-462`) builds a FRESH `router.New(catalog)`
on every call, resolves ONE executor up front via `r.Select(...)`, and then discards
the router entirely — it is never referenced again. `Run` then constructs the retry
policy with the constant `agentloop.BootstrapEscalationHook`
(`internal/runtime/run.go:616`, `internal/loop/retry_policy.go:38-41`):

```go
func BootstrapEscalationHook(request EscalationRequest) (supervisor.Executor, error) {
    return request.CurrentExecutor, nil   // unchanged — no escalation at all
}
```

So every gate-failing attempt inside `RetryingLoop.RunOnce`'s bounded retry loop
(`internal/loop/retry_policy.go:140-186`) retries with the IDENTICAL executor,
forever, up to `MaxAttempts`, then escalates to `needs-human` — never climbing the
capability ladder `router.Router.OnGateFailure`/`Select` already implement correctly
(`internal/router/router.go:195-238`, task 093): `OnGateFailure(entryID)` marks an
entry as tried-and-failed for the CURRENT dispatch, and the next `Select` call
naturally returns the next-cheapest eligible entry that has NOT yet gate-failed —
climbing the quality ladder exactly as ADR 043 designs it. `RecordDispatch`,
`OnQuotaExhausted`, `OnRateLimit`, `SaveState`, `LoadState` all have zero non-test call
sites anywhere in the codebase (confirmed by `grep`) — this task wires the FIRST of
those five, the quality-axis escalation hook, which is the highest-severity gap (a
gate-failing task NEVER improves its odds across retries today).

**The fix:** `resolveExecutor` retains the `*router.Router` it constructs (instead of
discarding it) and returns it alongside the executor/entry. `Run` builds a REAL
escalation hook closure — replacing `agentloop.BootstrapEscalationHook` on the
production `run`/`orchestrate` dispatch path — that, on each gate failure: calls
`router.OnGateFailure(currentEntryID)`, calls `router.Select(spec)` again, and if a
next entry is found, constructs its concrete executor via `buildExecutorForEntry` and
returns it (updating the closure's tracked `currentEntryID`); if `Select` returns
`router.ErrNoEligibleExecutor` (every eligible entry already gate-failed), the hook
returns the SAME (already-exhausted-of-alternatives) `CurrentExecutor` unchanged —
graceful degradation to the existing `BootstrapEscalationHook` behavior for the
remaining attempts, rather than aborting `RunOnce` with a hard, unhandled error.
`BootstrapEscalationHook` itself is untouched and remains exported/independently
testable (e.g. for callers who want no router-driven escalation).

**Module boundaries touched:** `internal/runtime` (`resolveExecutor`, `Run` — the
closure lives here, where `buildExecutorForEntry` and the `*router.Router` both
already live) and `internal/loop` (no interface change — `EscalationHook`'s existing
signature already carries everything the closure needs via `EscalationRequest` plus
its own captured state; this task adds NO new field to `EscalationRequest`).

This task's scope deliberately EXCLUDES `RecordDispatch`/`OnQuotaExhausted`/
`OnRateLimit`/`SaveState`/`LoadState` — those are tasks 161/162.

---

## Requirements coverage

| Req ID     | Description                                                                                                                 | Test cases            |
|------------|------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-160-01 | `resolveExecutor` returns the `*router.Router` it constructs (alongside the existing executor/entry/error), instead of discarding it | TC-160-01               |
| REQ-160-02 | `Run` builds a router-backed `EscalationHook` closure and wires it in place of `agentloop.BootstrapEscalationHook` on the live `run`/`orchestrate` dispatch path | TC-160-02               |
| REQ-160-03 | A gate-failing attempt calls `router.OnGateFailure(currentEntryID)` and the closure's NEXT return value is the concrete executor for the next-cheapest eligible entry the router's `Select` returns — proven via a multi-entry catalog where a cheap entry deterministically gate-fails and a stronger entry deterministically passes | TC-160-03               |
| REQ-160-04 | When every eligible entry has already gate-failed (`router.ErrNoEligibleExecutor`), the hook returns the CURRENT executor unchanged (graceful degradation) rather than propagating a hard error out of `RetryingLoop.RunOnce` | TC-160-04               |
| REQ-160-05 | The router's per-dispatch `escalated` set is scoped to ONE task's retry loop — a second, independent task dispatch (a fresh `resolveExecutor` call within the same process) starts with a clean escalation slate, not carrying over the previous task's exhausted entries | TC-160-05               |
| REQ-160-06 | `agentloop.BootstrapEscalationHook` remains exported and independently testable, unchanged in behavior, for any caller that does not want router-driven escalation | TC-160-06               |
| REQ-160-07 | `make fitness-orchestrator-no-executor` and `make fitness-llm-planner-no-executor` remain PASS — this task's changes stay inside `internal/runtime` + `internal/loop` and introduce no new import edge from `internal/orchestrator`/`internal/orchestrator/planner` into `internal/executor` | TC-160-07               |
| REQ-160-08 | Pre-existing `internal/runtime`, `internal/loop`, `tests/loop` suites continue to pass unchanged in behavior for the single-entry (zero-registry, synthetic default Claude entry) case — the common single-provider deployment sees NO behavior change (there is nothing to escalate TO) | TC-160-08               |

---

## Pre-implementation checklist

- [x] Task 093 merged (`Router.OnGateFailure`/`Select`/escalation-set semantics already exist and are unit-tested)
- [x] Task 155 merged (`Executor.Run(ctx, ...)`/`InBoxLoop.RunInside(ctx, ...)` plumbing
  landed first — this task's closure calls `buildExecutorForEntry`, unaffected by the
  signature change, but lands after to avoid `internal/runtime/run.go` merge conflicts)
- [ ] `make check` green before branching

---

## Test cases

### TC-160-01 — `resolveExecutor` returns the constructed Router

- **Requirement:** REQ-160-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go` or a new `run_160_test.go`

**Step:** Call `resolveExecutor(spec, config)` (extended return signature) against a
multi-entry fake catalog (via the existing `buildCatalog` test-injection seam).

**Expected output:** The returned `*router.Router` is non-nil and, when `Select`
is called on it directly by the test, returns the SAME entry `resolveExecutor` itself
selected — proving it is the actual router instance used for the initial selection,
not a fresh throwaway.

---

### TC-160-02 — `Run` wires the router-backed hook, not `BootstrapEscalationHook`

- **Requirement:** REQ-160-02
- **Level:** L2 (unit test, black-box via observable escalation behavior)
- **Test file:** `internal/runtime/run_160_test.go`

**Setup:** A two-entry fake catalog: a cheap entry whose fake executor always
gate-fails (verdict `OK: false`), and a more-expensive, higher-capability entry whose
fake executor always succeeds. `MaxAttempts >= 2`.

**Step:** Run the full `Run(ctx, config, stdout)` path (or the smallest slice that
exercises `resolveExecutor` → retry-policy construction → `RetryingLoop.RunOnce`) against
a task that reaches this catalog.

**Expected output:** Attempt 1 uses the cheap entry and gate-fails; attempt 2 uses the
STRONGER entry and succeeds — observable via the fake gate/executor's recorded call
log or the run's final outcome (`RetryOutcomeDone` with the stronger entry's
executor). This is IMPOSSIBLE under the pre-task `BootstrapEscalationHook` (attempt 2
would retry the SAME cheap, still-failing entry and exhaust `MaxAttempts` into
`needs-human`) — the test is written to fail against the old wiring.

---

### TC-160-03 — Gate failure calls `OnGateFailure` and climbs to the next eligible entry

- **Requirement:** REQ-160-03
- **Level:** L2 (unit test, closer to the router/closure boundary)
- **Test file:** `internal/runtime/run_160_test.go`

**Setup:** A three-entry fake catalog at increasing `CapabilityTier`/`CostWeight`:
cheap (fails), mid (fails), strong (succeeds). `MaxAttempts = 3`.

**Step:** Drive the retry loop to completion.

**Expected output:** The executor identity used on each of the 3 attempts
corresponds to cheap → mid → strong, in that order (the router's cost-ordered
`Select` after each `OnGateFailure` climbs monotonically) — confirmed via each fake
executor's own call counter (cheap called exactly once, mid called exactly once,
strong called exactly once) and the final outcome being `RetryOutcomeDone` on
attempt 3.

---

### TC-160-04 — Exhausted escalation degrades gracefully, does not abort `RunOnce`

- **Requirement:** REQ-160-04
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_160_test.go` or `tests/loop/retry_policy_test.go`

**Setup:** A single-entry (or all-entries-fail) fake catalog where EVERY eligible
entry's fake executor always gate-fails. `MaxAttempts = 3`.

**Step:** Drive the retry loop to completion.

**Expected output:** `RunOnce` returns `RetryOutcomeEscalated` (needs-human,
`markNeedsHuman` called) after exactly 3 attempts — NOT a hard, unhandled error out of
`RunOnce` from the escalation hook itself. All 3 attempts used the SAME (only
available) executor once the router's eligible set is exhausted, exactly matching
`BootstrapEscalationHook`'s pre-task behavior for this degenerate case.

---

### TC-160-05 — Escalation state is scoped per-task, not leaked across dispatches

- **Requirement:** REQ-160-05
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_160_test.go`

**Setup:** A two-entry catalog (cheap fails, strong succeeds). Call `resolveExecutor`
(or the full `Run` path) TWICE in sequence, each simulating an independent task
dispatch (as `Run` does — one `Run` call per task, called `buildCatalog` fresh each
time per the existing per-call construction).

**Step:** For the SECOND call, drive its own retry loop to completion.

**Expected output:** The second dispatch's first attempt uses the CHEAP entry again
(not skipping it as "already escalated past" from the first dispatch) — proving each
`resolveExecutor` call gets a fresh `Router`/escalation set, matching the existing
`router.Router.escalated` map's documented per-dispatch (not per-process) lifetime.
This confirms REQ-160-01's returned router is genuinely scoped to ONE `resolveExecutor`
call, not accidentally shared/cached across calls (a concern this task must NOT
introduce while fixing the ladder-climbing gap — task 162, not this task, is where a
DELIBERATE cross-dispatch persistence mechanism is introduced, via `SaveState`/`LoadState`,
not via reusing the same in-memory `*Router`).

---

### TC-160-06 — `BootstrapEscalationHook` is unchanged and still independently usable

- **Requirement:** REQ-160-06
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/loop/retry_policy_test.go` (existing)

**Step:** Call `agentloop.BootstrapEscalationHook(request)` directly, as the existing
pre-task tests already do.

**Expected output:** Returns `request.CurrentExecutor, nil` unchanged — identical to
pre-task behavior. `NewRetryingLoop`/`RetryingLoop.RunOnce` still accept it as a valid
`EscalationHook` for any caller that constructs a loop directly (bypassing
`internal/runtime`'s router-backed wiring).

---

### TC-160-07 — Fitness boundaries remain green

- **Requirement:** REQ-160-07
- **Level:** L3

**Step:**
```
make fitness-orchestrator-no-executor
make fitness-llm-planner-no-executor
```

**Expected output:** Both `PASS ...` lines, unchanged text — this task's closure lives
entirely inside `internal/runtime` (which already imports `internal/executor` and
`internal/router`), introducing no new import edge from `internal/orchestrator` or
`internal/orchestrator/planner`.

---

### TC-160-08 — Zero-registry (synthetic default Claude entry) path is unaffected

- **Requirement:** REQ-160-08
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/runtime/run_test.go` (existing zero-registry tests)

**Step:** Run the existing zero-`AGENT_BUILDER_REGISTRY_*`-env test(s) that exercise
the synthetic `defaultClaudeEntry` single-provider path.

**Expected output:** Unchanged behavior — a single-entry catalog has nothing to
escalate TO, so the router-backed hook behaves identically to
`BootstrapEscalationHook` for this (the most common, single-provider) deployment
shape. No new required env var, no new failure mode.

---

## Verification plan

- **Highest level achievable:** L5 — a fake multi-entry-catalog harness spanning the
  real `Run`/`resolveExecutor`/retry-policy/`RetryingLoop` production chain proves
  gate-failure escalation is genuinely live (climbing cheap→mid→strong across real
  retry attempts), not merely unit-tested in isolation. No live Claude/Codex/Ollama
  subprocess is required to prove this task's REQs.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/loop/... ./tests/loop/...
  ```
  Expected: all packages `ok`; TC-160-01..06/08 pass.
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-llm-planner-no-executor
  ```
  Expected: both PASS, unchanged text.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- `RecordDispatch` (usage/budget accounting) — task 161.
- `OnQuotaExhausted`/`OnRateLimit` (availability-axis fallback) — no executor currently
  surfaces a machine-parseable rate-limit/quota signal distinct from a generic error;
  flagged as future work, not this batch (see task 161's Out of scope for the
  explicit note).
- `SaveState`/`LoadState` (cross-process persistence) — task 162.
- Any change to `Executor.Run`'s signature (tasks 155-156, already merged/sequenced
  before this task).
