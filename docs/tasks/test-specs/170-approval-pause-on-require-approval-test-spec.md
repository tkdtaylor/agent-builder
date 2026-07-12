# Test Spec 170: pause a sub-goal dispatch on `require_approval`, recorded in RunStore

**Linked task:** [`docs/tasks/backlog/170-approval-pause-on-require-approval.md`](../backlog/170-approval-pause-on-require-approval.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

**This is a distinct layer from the orchestrator's existing plan-level approval.**
`Orchestrator.pauseForApproval`/`Resume`/`Approval` (`internal/orchestrator/orchestrator.go:590-665`)
already pause-and-resume a WHOLE PLAN before any sub-goal dispatch begins, gated
on the `spawn-plan` decision. That flow is complete and unaffected by this task.
This task targets a DIFFERENT, currently-terminal decision point: the
PER-SUB-GOAL `run-task` gate INSIDE `runtime.Run` (task 073's `decideGate`,
`internal/runtime/run.go:1055-1064`), which today, on `require_approval`, writes
a `needs-human` task-status file and returns `nil` from `Run`, a dead end. The
orchestrator's `dispatchOne` currently cannot even distinguish this outcome from
an ordinary successful dispatch at the `error` level (`Run` returns `nil` either
way, per REQ-073-01's documented contract, which this task does NOT change).

ADR 065 names this task explicitly: "approval pause/resume ... build on that
journal (tasks 169-171)." This task adds the PAUSE half; task 171 adds the
routing/resume half.

**The mechanism:** `runtime.Config` gains an optional hook,
`OnRequireApproval func(task supervisor.Task, reason string)`, fired from `Run`'s
existing `require_approval` branch. The orchestrator's `dispatchOne` supplies
this hook (only when a RunStore is configured) so it can persist a
`runstore.PendingApproval` onto the goal's `Record` and mark the record
`StatusAwaitingApproval`, without changing `runtime.Run`'s existing return
contract (an additive hook, matching this codebase's established pattern, e.g.
`WithDispatchFunc`).

**Module boundary:** `internal/runtime` (the new hook field + one call site) and
`internal/orchestrator` (supplying the hook, persisting the pending approval, and
`dispatchPlan`'s "don't start not-yet-dispatched sub-goals once a pause is
recorded" semantics). Two modules.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-170-01 | `runtime.Config` gains `OnRequireApproval func(task supervisor.Task, reason string)`; `Run`'s `require_approval` branch (`run.go:855-856`, immediately before the existing `fmt.Fprintf`/`return nil`) calls it when non-nil, with the task and `outcome.reason`. | TC-170-01, TC-170-02 |
| REQ-170-02 | `dispatchOne` supplies this hook when `o.runStore != nil`; firing it persists `runstore.PendingApproval{TaskID: sub.Task.ID, Reason: reason, RequestedAt: now}` onto the goal's `Record.Pending` and sets `Record.Status = runstore.StatusAwaitingApproval`. | TC-170-03, TC-170-04 |
| REQ-170-03 | Once a pending approval is recorded for a plan, `dispatchPlan` does not START any further not-yet-dispatched sub-goals for that plan; already in-flight sub-goals complete normally (best-effort, matching ADR 046 §5's existing concurrent-dispatch semantics, unmodified). | TC-170-05 |
| REQ-170-04 | When `o.runStore` is nil, the hook is never supplied (the orchestrator's per-dispatch `runtime.Config` has `OnRequireApproval == nil`), and `runtime.Run`'s behavior for a caller that never sets the hook (e.g. the single-task `run` subcommand, `internal/cli/cli.go`'s `runLoop`) is byte-for-byte unchanged. | TC-170-06 |
| REQ-170-05 | REQ-073-01 (`runtime.Run` returns `nil` on `require_approval`, task marked `needs-human`, box never starts) is completely unmodified; this task adds a hook call, it does not alter the routing/return contract. | TC-170-07 |

---

## Pre-implementation checklist

- [x] Task 073 merged (`decideGate`'s `require_approval` branch exists)
- [x] Task 167/168 merged (`runstore.Store`/`Record`/`PendingApproval`, `WithRunStore`)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-170-01, `OnRequireApproval` fires with the task and reason

- **Requirement:** REQ-170-01
- **Level:** L2 (unit test, mirrors `run_policy_audit_test.go`'s
  gateOutcome-shape-pinning approach, since `Run`/`decideGate` dial a live
  socket; the direct hook-firing contract is pinned here, the L5 e2e proof in
  TC-170-02 exercises the real daemon path)
- **Test file:** `internal/runtime/run_170_test.go` (new)

**Step:** Construct `Config{PolicyBin: <fake daemon>, ..., OnRequireApproval:
func(t supervisor.Task, reason string) { captured = ... }}` and drive a real
`require_approval`-responding fake policy-engine through `Run` (reusing
`tests/e2e/policy_gate_e2e_test.go`'s harness) OR, if the executor judges a pure
`internal/runtime` unit test insufficient to reach the live call site, promote
this to L5 directly (acceptable, matches this task's L5-primary verification
plan below).

**Expected output:** the hook is called exactly once, `t.ID` matches the
dispatched task, `reason` contains `"approval"` (matching the existing
`outcome.reason` wording task 073 already produces, unmodified).

---

### TC-170-02, end-to-end: hook fires through the real daemon path

- **Requirement:** REQ-170-01
- **Level:** L5 (extends `tests/e2e/policy_gate_e2e_test.go`'s fake-policy-engine
  harness)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go` (extend)

**Step:** This task's env-level e2e harness (`runAgentBuilder`, a subprocess) has
no way to observe an in-process Go closure firing; instead, add an L5 test at the
`internal/runtime` package level (NOT the `tests/e2e` subprocess harness) that
starts a REAL fake policy-engine daemon (reusing `buildFakePolicyEngine`'s binary
if that helper is exported/liftable, or a package-local equivalent) and calls
`runtime.Run` in-process with `OnRequireApproval` set, asserting the hook fires.
If reusing the subprocess-level `tests/e2e` binary is simpler for the executor,
an alternative acceptable proof is: add a test-only sentinel line to stdout when
the hook fires (behind a build-tag or test-only config field) and assert on it
via `runAgentBuilder`'s captured stdout, executor's choice, document whichever
approach is taken in the task's `Verification plan`.

**Expected output:** the hook demonstrably fires against a REAL running fake
policy-engine daemon, not just a hand-constructed `gateOutcome`, proving REQ-170-01
end-to-end.

---

### TC-170-03, dispatchOne persists a PendingApproval

- **Requirement:** REQ-170-02
- **Level:** L2 (unit test, real `Orchestrator` + real `runstore.FileStore`, fake
  `DispatchFunc` that INVOKES the supplied `runtime.Config.OnRequireApproval`
  hook itself, simulating what `runtime.Run` would do, matching this
  codebase's established `WithDispatchFunc` test-seam pattern for orchestrator-level
  tests, `internal/orchestrator/orchestrator_test.go`)
- **Test file:** `internal/orchestrator/approval_pause_170_test.go` (new)

**Step:** Construct `Orchestrator` with `WithRunStore(store)` and a fake
`DispatchFunc` that, for one specific sub-goal, calls the `runtime.Config` it
receives (task 170's design requires `DispatchFunc`'s signature or the config it
is handed to expose `OnRequireApproval`, if the current `DispatchFunc` signature
does not carry the full `runtime.Config`, extend it minimally, documented in the
task file's implementation outline) with `OnRequireApproval(sub.Task, "policy:
requires human approval")`. Call `Handle`.

**Expected output:** `store.Load(goal.ID).Pending` contains exactly one
`PendingApproval{TaskID: <the sub-goal's ID>, Reason: "policy: requires human
approval"}`; `store.Load(goal.ID).Status == runstore.StatusAwaitingApproval`.

---

### TC-170-04, RequestedAt is set and monotonic

- **Requirement:** REQ-170-02
- **Level:** L2

**Step:** Same setup as TC-170-03, capture `time.Now()` immediately before and
after `Handle` returns.

**Expected output:** `PendingApproval.RequestedAt` falls between the two
captured timestamps (inclusive), proving it is set at hook-fire time, not left
zero-valued.

---

### TC-170-05, a pause halts further NOT-YET-STARTED dispatch

- **Requirement:** REQ-170-03
- **Level:** L2 (deterministic ordering forced via a single-worker semaphore in
  the test fixture, or via a fake `DispatchFunc` with an internal ordering gate,
  mirroring TC-168-07's determinism technique)

**Step:** A 3-sub-goal plan; sub-goal 1 dispatches and requires approval
(pauses); sub-goals 2 and 3 have a fake `DispatchFunc` that would `t.Fatal` if
ever invoked. Call `Handle`.

**Expected output:** sub-goals 2 and 3's `DispatchFunc` is never invoked; the
overall `PlanResult`/reported outcome for the goal reflects an awaiting-approval
state (not a hard failure, not a silent success), matching the existing
plan-level-pause reporting shape `pauseForApproval` already establishes at
`orchestrator.go:590-618` for consistency (reuse its rendering convention where
reasonable).

---

### TC-170-06, RunStore unset: hook never supplied, byte-for-byte unchanged

- **Requirement:** REQ-170-04
- **Level:** L2 (regression)

**Step:** Construct `Orchestrator` WITHOUT `WithRunStore`. Dispatch a sub-goal
that would require approval. Run the pre-existing `internal/runtime` suite
(`go test ./internal/runtime/...`) with no `OnRequireApproval` set anywhere
(the single-task `run` subcommand's existing call sites, unmodified).

**Expected output:** `internal/runtime`'s pre-existing require_approval tests
(TC-073-01/02 and this task's TC-170-01) all pass unchanged; a `runtime.Config`
with a zero-value (nil) `OnRequireApproval` never panics on the require_approval
path (a nil-func-call guard, `if config.OnRequireApproval != nil { ... }`).

---

### TC-170-07, REQ-073-01 completely unmodified

- **Requirement:** REQ-170-05
- **Level:** L2/L5 (regression)

**Step:** Re-run `TestPolicyGateRequireApprovalDistinctFromDeny` and
`TestRequireApprovalStatusReason` unmodified.

**Expected output:** byte-identical pass.

---

### TC-170-08, full regression

- **Requirement:** all
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/runtime/... ./internal/orchestrator/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, TC-170-02's real-daemon hook-firing proof
  plus TC-170-05's deterministic pause-halts-further-dispatch proof.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/orchestrator/... -run TestTC170
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/runtime/... -run TestTC170_02
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Routing the pending approval over any channel (Telegram, `examples/agent-cli`)
  or resuming a paused sub-goal on an operator reply (task 171).
- Timeout-based auto-escalation of an unresolved pending approval (task 171).
- Any change to the existing plan-level `pauseForApproval`/`Resume`/`Approval`
  flow (unrelated, unmodified layer).
