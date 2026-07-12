# Task 168: rehydrate and idempotently resume in-flight runs from RunStore

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Wire `internal/orchestrator` to persist a `runstore.Record` per admitted plan and
per sub-goal attempt, and add `RehydrateInFlight`/`Orchestrator.ResumeFromRecord`
so a fresh process can pick an interrupted goal back up without re-dispatching
work already completed by a prior process.

## Context

ADR 065 names this task explicitly: "Resume-after-restart rehydration from the
journal with idempotent re-dispatch (task 168)." Task 167 built the storage
primitive (`internal/runstore`); nothing writes to or reads from it yet. Today the
orchestrator is, per ADR 065's own framing, "Handle → dispatchPlan → report →
stop" with `orchestrator.MemoryPlanStore` as its only state; a crash mid-goal
loses everything.

This task is opt-in (`WithRunStore`), matching every other optional seam already
in this codebase (`WithPlanStore`, `WithAuditSink`, `WithClarifier`, etc. at
`internal/orchestrator/orchestrator.go:307-406`). When unset, behavior is
byte-for-byte unchanged.

**Reference:**
- `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`
- `internal/orchestrator/orchestrator.go:406-436` (`New`, the `Option` pattern to extend)
- `internal/orchestrator/orchestrator.go:852-1049` (`dispatchPlan`, `dispatchOne`,
  `dispatchWithSemaphore`, the edit sites for per-sub-goal attempt recording)
- `internal/orchestrator/orchestrator.go:529-618` (`ConfirmAndPlan`, the edit site
  for plan-admission recording)
- `internal/runstore` (task 167, the `Store`/`Record`/`AttemptState` types this
  task consumes, unmodified)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-168-01 | `WithRunStore(s runstore.Store) Option`; `ConfirmAndPlan` persists a `Record` on plan admission, before dispatch. | must have |
| REQ-168-02 | Per-sub-goal dispatch records `AttemptState` before (running) and after (completed/failed) dispatch. | must have |
| REQ-168-03 | `RehydrateInFlight(store) ([]runstore.Record, error)` wraps `ListInFlight`. | must have |
| REQ-168-04 | `Orchestrator.ResumeFromRecord(ctx, rec) (PlanResult, error)` re-dispatches only non-completed sub-goals. | must have |
| REQ-168-05 | Simulated crash-and-resume across two independently-constructed orchestrator+store instances re-dispatches only the interrupted remainder. | must have |
| REQ-168-06 | `WithRunStore` unset: behavior byte-for-byte unchanged, zero `runstore` calls. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/168-resume-after-restart-test-spec.md` exists (written first)
- [x] Task 167 merged (`internal/runstore` exists)
- [x] Task 081/085/086 merged (orchestrator core, dispatch)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `Orchestrator` struct (`orchestrator.go:244-`) gains `runStore
   runstore.Store` (nil by default); `WithRunStore(s runstore.Store) Option`
   sets it, mirroring `WithAuditSink`'s shape exactly.
2. `ConfirmAndPlan`: immediately after the spawn-plan `policy.DecisionAllow`
   branch admits the plan (before `dispatchPlan` is called), if
   `o.runStore != nil`, marshal `plan` to JSON and call:
   ```go
   _ = o.runStore.Save(runstore.Record{
       GoalID: plan.GoalID,
       Goal:   plan.Goal,
       Plan:   planJSON,
       Status: runstore.StatusRunning,
   })
   ```
   (Best-effort: a `Save` error is logged, not fatal to the dispatch, matching
   this codebase's existing convention for audit/tamper side-effects, e.g.
   `emitTamperEvent`'s error-swallow at `memoryguard.go:135`. If the executor
   judges a `Save` failure should instead be fail-loud given RunStore's role as
   the crash-recovery source of truth, document that decision explicitly in the
   PR/task-file `Verification plan` update, either choice is acceptable as long
   as it is deliberate and tested.)
3. `dispatchOne` (or a thin wrapper around it called from `dispatchPlan`'s
   per-sub-goal goroutine): before calling the sub-goal's dispatch, if
   `o.runStore != nil`, append/update an `AttemptState{TaskID: sub.Task.ID,
   Attempt: 1, Status: runstore.StatusRunning}` onto the goal's `Record` via
   `Save` (read-modify-write: `Load`, mutate `Attempts`, `Save`, guarded by
   the SAME synchronization `dispatchPlan`'s existing `sync.WaitGroup`/index-write
   discipline already uses for the audit chain and `PlanStore`, per
   `orchestrator.go:842-844`'s documented cross-goroutine mutable-state
   discipline). After the dispatch returns, update that SAME attempt entry to
   `StatusCompleted`/`StatusFailed`.
4. `RehydrateInFlight(store runstore.Store) ([]runstore.Record, error)`: package-level
   function, `return store.ListInFlight()` (named wrapper, not new logic, gives
   callers, task 174's daemon, a single documented entrypoint).
5. `Orchestrator.ResumeFromRecord(ctx context.Context, rec runstore.Record)
   (PlanResult, error)`: unmarshal `rec.Plan` back into an `orchestrator.Plan`,
   build the set of `TaskID`s with `Status == runstore.StatusCompleted` in
   `rec.Attempts`, filter `plan.SubGoals` to exclude those, and call the SAME
   dispatch path `dispatchPlan` already uses for the filtered sub-goal set
   (refactor `dispatchPlan` to accept an explicit sub-goal slice internally if
   needed, so both the normal and resume paths share one implementation).
6. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-168-01] TC-168-01: plan admission persists a Record.
- [ ] [REQ-168-02] TC-168-02/03: attempt state recorded before/after, success and failure.
- [ ] [REQ-168-03] TC-168-04: `RehydrateInFlight` wraps `ListInFlight`.
- [ ] [REQ-168-04] TC-168-05/06: `ResumeFromRecord` skips completed, re-dispatches interrupted.
- [ ] [REQ-168-05] TC-168-07: end-to-end simulated crash-and-resume (L5).
- [ ] [REQ-168-06] TC-168-08: RunStore unset leaves behavior byte-for-byte unchanged.
- [ ] TC-168-09: `go test -race -count=1 ./internal/orchestrator/... ./internal/runstore/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, TC-168-07's two-independently-constructed
  orchestrator-plus-store simulated-crash-and-resume proof, the strongest
  achievable evidence inside a single Go test binary.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... -run TestTC168
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/orchestrator/... -run TestTC168_07
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md`: the Tier-1 orchestrator section gains
  `WithRunStore`/`RehydrateInFlight`/`ResumeFromRecord` to its documented seams.
- `docs/spec/behaviors.md`: new behavior entry describing crash-mid-plan recovery
  semantics (idempotent re-dispatch, never double-dispatches a completed
  attempt).
- `docs/architecture/diagrams.md`: the orchestrator dispatch flow diagram gains
  the optional RunStore write/read points.

## Out of scope

- A retry/re-plan budget loop across fully-failed goals (task 169).
- Approval-pause persistence and resume-on-approval-message (task 170/171).
- Automatic rehydration at CLI/daemon startup (this task exposes the functions;
  task 174 calls them at daemon startup).
- `Compact()` scheduling.

## Dependencies

- **Blocks on:** task 167 (`internal/runstore`).
- **Blocks:** task 169, 170, 174.
