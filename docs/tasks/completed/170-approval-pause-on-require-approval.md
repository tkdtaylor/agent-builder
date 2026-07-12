# Task 170: pause a sub-goal dispatch on `require_approval`, recorded in RunStore

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** completed

## Goal

Add an optional `runtime.Config.OnRequireApproval` hook fired from `Run`'s
existing `require_approval` branch, and wire the orchestrator's `dispatchOne` to
supply it (when a RunStore is configured) so a sub-goal that hits
`require_approval` persists a `runstore.PendingApproval` and pauses further
dispatch for that plan, instead of only writing a task-status file no orchestrator
code observes.

## Context

**This is a distinct layer from the orchestrator's existing plan-level
approval.** `Orchestrator.pauseForApproval`/`Resume`/`Approval`
(`internal/orchestrator/orchestrator.go:590-665`) already pause-and-resume a
WHOLE PLAN before any sub-goal dispatch begins, gated on `spawn-plan`. That flow
is unaffected. This task targets the PER-SUB-GOAL `run-task` gate inside
`runtime.Run` (task 073's `decideGate`), which today, on `require_approval`,
writes a `needs-human` task-status file and returns `nil`, a dead end the
orchestrator cannot currently distinguish from a successful dispatch.

ADR 065 names this as part of the "approval pause/resume" arc (tasks 169-171)
built on the run journal.

**Reference:**
- `internal/runtime/run.go:846-856` (`Run`'s `require_approval` halt, the hook
  call site)
- `internal/runtime/run.go:1055-1064` (`decideGate`'s `require_approval` branch,
  unmodified, its `reason` string is what the hook receives)
- `internal/orchestrator/orchestrator.go:957-1049` (`dispatchOne`,
  `dispatchWithSemaphore`, the wiring site for supplying the hook)
- `internal/orchestrator/orchestrator.go:590-618` (`pauseForApproval`, the
  existing plan-level pause, a rendering/reporting convention to mirror where
  reasonable, NOT a code path this task modifies)
- Task 167/168 (`runstore.PendingApproval`, `Record.Pending`,
  `Record.Status`, `WithRunStore`, all consumed unmodified)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-170-01 | `runtime.Config.OnRequireApproval` hook, fired from `Run`'s existing `require_approval` branch. | must have |
| REQ-170-02 | `dispatchOne` supplies the hook (when RunStore configured); firing persists a `PendingApproval` and sets `Record.Status = StatusAwaitingApproval`. | must have |
| REQ-170-03 | A recorded pause halts further not-yet-started sub-goal dispatch for that plan; in-flight sub-goals complete. | must have |
| REQ-170-04 | RunStore unset: hook never supplied, byte-for-byte unchanged for existing callers. | must have |
| REQ-170-05 | REQ-073-01's existing routing/return contract is completely unmodified. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/170-approval-pause-on-require-approval-test-spec.md` exists (written first)
- [x] Task 073 merged
- [x] Task 167/168 merged
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/runtime/run.go`, `Config` struct (`run.go:102-149`): add
   `OnRequireApproval func(task supervisor.Task, reason string)` (unexported
   field name at executor's discretion, e.g. `onRequireApproval` if it should
   only be settable via a functional option rather than direct struct literal
   assignment elsewhere in the package, match whatever convention
   `DispatchedTask` already uses one field above it).
2. In `Run`, immediately before the existing `require_approval` halt block
   (`run.go:855-856`, the `fmt.Fprintf`/`return nil`), add:
   ```go
   if config.OnRequireApproval != nil {
       config.OnRequireApproval(task, outcome.reason)
   }
   ```
   Do not touch anything else in this block; `Run`'s return value stays `nil`,
   unchanged (REQ-170-05, REQ-073-01 preserved verbatim).
3. `internal/orchestrator/orchestrator.go`, `dispatchOne` (or wherever it
   constructs the per-sub-goal `runtime.Config`, likely via `o.baseConfig` cloned
   per dispatch, `orchestrator.go:242-250`): when `o.runStore != nil`, set
   ```go
   cfg.OnRequireApproval = func(t supervisor.Task, reason string) {
       o.recordPendingApproval(plan.GoalID, t.ID, reason)
   }
   ```
4. `recordPendingApproval(goalID, taskID, reason string)`: `Load` the goal's
   `Record` from `o.runStore`, append
   `runstore.PendingApproval{TaskID: taskID, Reason: reason, RequestedAt:
   time.Now()}` to `Record.Pending`, set `Record.Status =
   runstore.StatusAwaitingApproval`, `Save`. Guard with the same
   cross-goroutine synchronization discipline `orchestrator.go:842-844`
   documents for the audit chain / PlanStore (a plan's dispatch runs
   sub-goals concurrently per ADR 046 §5, so this write must be safe against
   concurrent calls, either via `o.runStore`'s own internal locking, task 167's
   `FileStore` already has a `sync.Mutex`, which is sufficient, or an
   additional orchestrator-side lock if the read-modify-write of `Record.Pending`
   needs stronger atomicity than `Save`'s own internal locking alone provides;
   document whichever is chosen).
5. `dispatchPlan`: before starting each not-yet-dispatched sub-goal's goroutine,
   check (via `o.runStore`, when configured) whether the plan's `Record.Status`
   has already become `StatusAwaitingApproval` (set by an earlier sub-goal's
   concurrent dispatch); if so, skip starting that sub-goal (best-effort:
   already-started goroutines are NOT cancelled, matching ADR 046 §5's existing
   best-effort semantics, unmodified).
6. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-170-01] TC-170-01/02: hook fires with task+reason, proven at L2 and L5.
- [ ] [REQ-170-02] TC-170-03/04: PendingApproval persisted, RequestedAt set.
- [ ] [REQ-170-03] TC-170-05: pause halts further not-yet-started dispatch.
- [ ] [REQ-170-04] TC-170-06: RunStore unset, hook never supplied, unchanged.
- [ ] [REQ-170-05] TC-170-07: REQ-073-01 regression unmodified.
- [ ] TC-170-08: `go test -race -count=1 ./internal/runtime/... ./internal/orchestrator/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, a real fake-policy-engine daemon firing the
  hook through the genuine `decideGate`/`Run` call chain, plus a deterministic
  pause-halts-dispatch proof.
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

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md`: `runtime.Config.OnRequireApproval` documented
  alongside the existing policy-gate seam entry.
- `docs/spec/behaviors.md`: new behavior entry distinguishing plan-level pause
  (existing) from sub-goal-level dispatch pause (this task).

## Out of scope

- Routing the pending approval over any channel or resuming on an operator reply
  (task 171).
- Timeout-based auto-escalation of an unresolved pending approval (task 171).
- Any change to the existing plan-level `pauseForApproval`/`Resume`/`Approval` flow.

## Dependencies

- **Blocks on:** task 073 (already merged), task 167/168.
- **Blocks:** task 171.
