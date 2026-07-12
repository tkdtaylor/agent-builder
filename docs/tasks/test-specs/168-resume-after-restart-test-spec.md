# Test Spec 168: rehydrate and idempotently resume in-flight runs from RunStore

**Linked task:** [`docs/tasks/backlog/168-resume-after-restart.md`](../backlog/168-resume-after-restart.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Task 167 built `internal/runstore`, a durable journal, but nothing writes to it
or reads from it yet. This task wires `internal/orchestrator` to (a) persist a
`runstore.Record` for every admitted plan and update it per sub-goal dispatch, and
(b) rehydrate and resume in-flight records at startup, idempotently: a sub-goal
already marked `completed` in a prior process's `Record.Attempts` must never be
re-dispatched.

ADR 065 names this task explicitly: "Resume-after-restart rehydration from the
journal with idempotent re-dispatch (task 168)." This is opt-in: an
`Orchestrator` constructed without a `runstore.Store` (the default, via
`WithRunStore` unset) behaves byte-for-byte as before this task, matching every
other optional seam in this codebase (`WithPlanStore`, `WithAuditSink`, etc.).

**Module boundary:** `internal/orchestrator` only. `internal/runstore` (task 167)
gains no new exported surface here beyond what it already has; the orchestrator
is simply a new consumer.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-168-01 | `Orchestrator` gains `WithRunStore(s runstore.Store) Option`. When set, `ConfirmAndPlan` persists `runstore.Record{GoalID, Goal, Plan: <marshaled Plan>, Status: StatusRunning}` to the store immediately after the plan is admitted (spawn-plan allow), before `dispatchPlan` begins. | TC-168-01 |
| REQ-168-02 | `dispatchOne` (or its caller) records an `AttemptState{TaskID: sub.Task.ID, Status: running}` to the store BEFORE dispatching a sub-goal, and updates it to `completed`/`failed` AFTER the dispatch returns, appended onto the goal's `Record.Attempts` via `Save`. | TC-168-02, TC-168-03 |
| REQ-168-03 | A new `RehydrateInFlight(store runstore.Store) ([]runstore.Record, error)` function calls `store.ListInFlight()` (a thin, directly-testable wrapper naming the operation at the orchestrator layer). | TC-168-04 |
| REQ-168-04 | `Orchestrator.ResumeFromRecord(ctx, rec runstore.Record) (PlanResult, error)` re-drives dispatch for a rehydrated record, re-dispatching ONLY sub-goals whose `TaskID` has no `completed` entry in `rec.Attempts` (a sub-goal with a `running` or absent attempt IS re-dispatched, since `running`-and-crashed is indistinguishable from never-started at this scope, matching the idempotency requirement: never DOUBLE-dispatch a COMPLETED attempt, re-dispatching an interrupted one is correct and safe). | TC-168-05, TC-168-06 |
| REQ-168-05 | A simulated crash mid-plan (a fake dispatch func that fails/panics after N of M sub-goal dispatches, recovered via `t.Cleanup`/`recover` in the test, or more simply: construct a SECOND `Orchestrator`+`FileStore` pair after only N of M `Save` calls landed) followed by `RehydrateInFlight` + `ResumeFromRecord` on a fresh `Orchestrator` dispatches ONLY the remaining `M-N` sub-goals (call-count assertion on the fake dispatch func). | TC-168-07 |
| REQ-168-06 | When `WithRunStore` is unset (`nil`), `Orchestrator` behavior (including `Handle`, `ConfirmAndPlan`, `dispatchPlan`) is byte-for-byte unchanged from pre-task, no `runstore` calls of any kind (verified via a spy `Store` that fails the test if invoked). | TC-168-08 |

---

## Pre-implementation checklist

- [x] Task 167 merged (`internal/runstore.Store`/`Record`/`AttemptState` exist)
- [x] Task 081/085/086 merged (orchestrator core, dispatch, concurrent dispatch)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-168-01, plan admission persists a Record

- **Requirement:** REQ-168-01
- **Level:** L2 (unit test, real `runstore.NewFileStore` in a temp dir, real
  `Orchestrator` with a fake `Planner`/`PolicyClient`/`DispatchFunc`, mirroring
  the existing `orchestrator_test.go` fixture pattern)
- **Test file:** `internal/orchestrator/runstore_168_test.go` (new)

**Step:** Construct `Orchestrator` with `WithRunStore(store)` where `store` is a
real `*runstore.FileStore`. Call `Handle` (or `ConfirmAndPlan` directly, matching
the existing test seam conventions in `orchestrator_test.go`) with a goal that
admits (allow decision, non-empty plan).

**Expected output:** `store.Load(goal.ID)` returns a `Record` with
`Status == runstore.StatusRunning`, `Goal == goal.Spec`, and `Plan` unmarshaling
back into the same `orchestrator.Plan` value the `Planner` produced.

---

### TC-168-02, sub-goal dispatch records attempt state before and after

- **Requirement:** REQ-168-02
- **Level:** L2

**Step:** Same setup as TC-168-01, with a plan carrying 2 sub-goals and a fake
`DispatchFunc` that succeeds for both. After `Handle` completes, `store.Load(goal.ID)`.

**Expected output:** `Record.Attempts` has exactly 2 entries (one per sub-goal
`TaskID`), each `Status == runstore.StatusCompleted`, `Attempt == 1`. Ordering
does not matter (concurrent dispatch per ADR 046 §5), but both sub-goal `TaskID`s
must be present exactly once each (no duplicates from the before/after write
pair, the AFTER write updates the SAME logical attempt entry, it does not append
a second one).

---

### TC-168-03, a failed sub-goal dispatch is recorded as failed

- **Requirement:** REQ-168-02
- **Level:** L2

**Step:** Same setup, one sub-goal's fake `DispatchFunc` returns an error.

**Expected output:** the failing sub-goal's `AttemptState.Status ==
runstore.StatusFailed`, `Detail` non-empty (carries the error text or a
classification, executor's choice); the succeeding sub-goal's attempt is still
`StatusCompleted` (independent per-sub-goal state, matching ADR 046 §5's
best-effort concurrent dispatch).

---

### TC-168-04, `RehydrateInFlight` wraps `ListInFlight`

- **Requirement:** REQ-168-03
- **Level:** L2

**Step:** `Save` three records directly via a `runstore.FileStore` (two
`StatusRunning`, one `StatusCompleted`). Call `orchestrator.RehydrateInFlight(store)`.

**Expected output:** returns exactly the 2 non-terminal records, identical to
calling `store.ListInFlight()` directly (this function is a documented, tested
name for the operation at the orchestrator layer, not new logic).

---

### TC-168-05, `ResumeFromRecord` skips completed sub-goals

- **Requirement:** REQ-168-04
- **Level:** L2

**Step:** Construct a `Record` by hand with `Plan` containing 3 sub-goals
(`sub-a`, `sub-b`, `sub-c`) and `Attempts` containing ONE entry:
`{TaskID: "sub-a", Status: StatusCompleted}`. Construct an `Orchestrator` with a
fake `DispatchFunc` that records which `SubGoal.Task.ID`s it was called with.
Call `ResumeFromRecord(ctx, rec)`.

**Expected output:** the fake `DispatchFunc` is called for `sub-b` and `sub-c`
ONLY, never for `sub-a` (the completed one). Call count == 2, not 3.

---

### TC-168-06, `ResumeFromRecord` re-dispatches an interrupted (non-completed) attempt

- **Requirement:** REQ-168-04
- **Level:** L2

**Step:** Same 3-sub-goal plan, `Attempts` containing
`{TaskID: "sub-a", Status: StatusCompleted}` AND
`{TaskID: "sub-b", Status: StatusRunning}` (interrupted mid-dispatch, never
reached a terminal state before the simulated crash). Call `ResumeFromRecord`.

**Expected output:** the fake `DispatchFunc` is called for `sub-b` AND `sub-c`
(both non-completed), never `sub-a`. This proves the idempotency rule is
specifically "never re-run a COMPLETED attempt," not "never re-run any attempt
that was ever touched."

---

### TC-168-07, end-to-end simulated-crash-and-resume (the load-bearing proof)

- **Requirement:** REQ-168-05
- **Level:** L5 (two independently-constructed `Orchestrator`+`FileStore` pairs
  sharing one on-disk directory inside a single Go test binary, mirroring TC-167-11
  and task 162's TC-162-06 pattern)

**Step:** (1) Construct `orch1` with `WithRunStore(store1)` (`store1` backed by a
shared temp dir) and a fake `DispatchFunc` configured to dispatch `sub-a`
successfully then FAIL (simulating a crash) before reaching `sub-b`/`sub-c` of a
3-sub-goal plan. Call `Handle`; expect it to report the partial failure (or, if
`dispatchPlan`'s existing best-effort semantics mean the OTHER sub-goals still
dispatch concurrently, force a serialized/single-worker semaphore in the test
fixture so the "crash before reaching sub-b/c" ordering is deterministic).
(2) Construct a FRESH `store2 := runstore.NewFileStore(sameDir)` and a fresh
`orch2` with `WithRunStore(store2)` and a NEW fake `DispatchFunc` that dispatches
successfully. Call `orchestrator.RehydrateInFlight(store2)`, then
`orch2.ResumeFromRecord(ctx, rec)` for the rehydrated record.

**Expected output:** `orch2`'s fake `DispatchFunc` is called for `sub-b` and
`sub-c` ONLY (never re-dispatches `sub-a`, which `orch1` already completed and
durably recorded before the simulated crash). After `ResumeFromRecord` completes,
`store2.Load(goal.ID).Status == runstore.StatusCompleted` (or the plan's
appropriate terminal status) and `Attempts` has all 3 entries, each completed
exactly once total across BOTH orchestrator instances.

---

### TC-168-08, RunStore unset is byte-for-byte unchanged

- **Requirement:** REQ-168-06
- **Level:** L2 (regression)

**Step:** Construct `Orchestrator` WITHOUT `WithRunStore` (the default). Provide a
spy `runstore.Store` implementation that calls `t.Fatal` on any method
invocation, and confirm it is never even CONSTRUCTED/passed (i.e. the
orchestrator's internal `runStore` field stays nil and every runstore-touching
code path is behind an explicit `if o.runStore != nil` guard). Run the FULL
pre-existing `internal/orchestrator` test suite (`go test ./internal/orchestrator/...`).

**Expected output:** every pre-existing test passes unchanged; no test in the
suite ever touches `internal/runstore` (confirmed by the package's import graph
being unaffected for the no-op path, and by code review of the guard).

---

### TC-168-09, full regression

- **Requirement:** all
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/orchestrator/... ./internal/runstore/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, TC-168-07's two-independently-constructed
  orchestrator-plus-store simulated-crash-and-resume proof.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... -run TestTC168
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/orchestrator/... -run TestTC168_07
  ```
  Expected: only the 2 remaining sub-goals dispatch on the second orchestrator
  instance; zero double-dispatch of the completed one.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- The retry/re-plan budget loop across FULLY failed goals (task 169).
- Approval-pause persistence and resume-on-approval-message (task 170/171).
- Automatic rehydration at CLI/daemon startup (this task provides
  `RehydrateInFlight`/`ResumeFromRecord` as callable functions; task 174 wires
  them into the daemon's actual startup sequence).
- `Compact()` scheduling (task 167's concern, unchanged here).
