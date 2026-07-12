# Task 173: back `orchestrator.PlanStore` with the durable, read-gated memory store

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** completed

## Goal

Swap `MemoryGuardPlanStore`'s backing from `memoryguard.MemoryGuardStore[Plan]`
(non-durable, read-ungated) to `memoryguard.DurableStore[Plan]` (task 172), so a
plan awaiting approval survives a restart and `PlanStore.Get` finally calls
`ValidateRead`.

## Context

`internal/memoryguard/store.go:77-84`'s `MemoryGuardStore[P].Get` has documented
its own gap since task 084: "purely in-process (no IPC on the read path in this
task's scope)." Task 172 built the fix (`DurableStore[P]`); this task is the
narrow, mechanical swap that connects it to the one consumer that needs it
today, `orchestrator.PlanStore`. `MemoryGuardPlanStore`'s public surface
(`TryPut`, `Put`, `Get`, `Delete`, `TryDelete`, `StoredID`) keeps its exact
signatures; only the field type and constructor change.

**Reference:**
- `internal/orchestrator/memoryguard.go` (`MemoryGuardPlanStore`, the edit site)
- `internal/memoryguard/durable_store.go` (task 172, consumed unmodified)
- `internal/orchestrator/orchestrator.go:169-188` (`PlanStore`,
  `TamperAwarePlanStore` interfaces, unaffected)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-173-01 | `MemoryGuardPlanStore.inner` becomes `*memoryguard.DurableStore[Plan]`; constructors gain a `dir` parameter. | must have |
| REQ-173-02 | `Get` calls `ValidateRead`; a denial returns `(Plan{}, false)`, never leaks the plan. | must have |
| REQ-173-03 | Write-gate/delete-verify/tamper behavior unchanged. | must have |
| REQ-173-04 | A stored plan survives a restart (cross-construction durability proof). | must have |
| REQ-173-05 | `NewPlanStoreFromEnv` gains a directory config point; unset-memory-guard path unaffected. | must have |
| REQ-173-06 | Pre-existing suites pass unchanged apart from the constructor signature. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/173-planstore-on-persistent-memory-test-spec.md` exists (written first)
- [x] Task 172 merged
- [x] Task 084 merged
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/orchestrator/memoryguard.go`:
   - `MemoryGuardPlanStore.inner` type changes to `*memoryguard.DurableStore[Plan]`.
   - `NewMemoryGuardPlanStore(binPath, identity, dir string) (*MemoryGuardPlanStore, error)`
     (now returns an error, since `DurableStore`'s constructor can fail on a
     malformed on-disk state, propagate it; update the one existing call site,
     `NewPlanStoreFromEnv`, to handle it, fail loud, mirroring this codebase's
     established config-error convention rather than silently degrading).
   - `NewMemoryGuardPlanStoreWithRunner(binPath, identity, dir string, runner
     memoryguard.ExecRunner) (*MemoryGuardPlanStore, error)` symmetric change.
   - `Get(goalID string) (Plan, bool)`: delegate to
     `s.inner.Get(goalID)`, which now returns `(Plan, bool, error)`; on a
     non-nil error (read-gate denial or transport failure), return `(Plan{},
     false)`, matching the void `PlanStore.Get` interface's existing "not
     found" contract (if the executor judges callers need the distinction
     between "not found" and "denied", add an optional `TryGet(goalID string)
     (Plan, bool, error)` alongside, additive, not required by this task's
     acceptance criteria).
   - `TryPut`/`Put`/`Delete`/`TryDelete`/`StoredID` bodies unchanged beyond
     whatever minimal signature adaptation `DurableStore`'s API requires (they
     already delegate to `s.inner`'s equivalent methods).
2. `NewPlanStoreFromEnv(logFn MemoryGuardLogFunc) PlanStore`: when
   `AGENT_BUILDER_MEMORY_GUARD_BIN` is set, also resolve a directory (new env
   var `AGENT_BUILDER_PLAN_STORE_DIR`, or a sensible default derived from an
   existing path convention if unset, executor's choice, document whichever is
   chosen in `docs/spec/configuration.md`), pass it to
   `NewMemoryGuardPlanStore`, and on a construction error, fail loud
   (`NewPlanStoreFromEnv`'s signature may need to become error-returning, or the
   caller in `internal/cli/orchestrate.go` may need updating, propagate the
   change through `assembleOrchestrate` cleanly, matching the SAME
   SEC-003-style fail-fast-before-goal-intake ordering already established
   there for `worker.LoadSigningKey`).
3. When `AGENT_BUILDER_MEMORY_GUARD_BIN` is unset: unchanged, still returns
   `MemoryPlanStore`.
4. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-173-01] TC-173-01: constructor signature updated, compiles, constructs.
- [ ] [REQ-173-02] TC-173-02/03: `Get` fails closed on denial, returns value on allow.
- [ ] [REQ-173-03] TC-173-04: write-gate/delete-verify/tamper behavior unchanged.
- [ ] [REQ-173-04] TC-173-05: cross-restart durability (L5).
- [ ] [REQ-173-05] TC-173-06: `NewPlanStoreFromEnv` unaffected when unset.
- [ ] [REQ-173-06] TC-173-07: `go test -race -count=1 ./internal/orchestrator/... ./internal/memoryguard/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, TC-173-05's two-independently-constructed
  `MemoryGuardPlanStore` cross-restart proof.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... -run TestTC173
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/orchestrator/... -run TestTC173_05
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md`: new `AGENT_BUILDER_PLAN_STORE_DIR` row (or the
  default-derivation note, if the executor chose that path); the existing
  `AGENT_BUILDER_MEMORY_GUARD_BIN` row's description updated to note the plan
  store is now durable, not just write-gated.
- `docs/spec/architecture.md`: the memory-guard adoption row notes durability.

## Out of scope

- Any change to `PlanStore`/`TamperAwarePlanStore`'s interface shape.
- Any change to `Handle`/`Resume`/`dispatchPlan`'s logic.
- General-purpose cross-session goal/skill/context memory beyond `PlanStore`.

## Dependencies

- **Blocks on:** task 172.
- **Blocks:** none.
