# Test Spec 173: back `orchestrator.PlanStore` with the durable, read-gated memory store

**Linked task:** [`docs/tasks/backlog/173-planstore-on-persistent-memory.md`](../backlog/173-planstore-on-persistent-memory.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Task 172 built `DurableStore[P]`. This task swaps `MemoryGuardPlanStore`
(`internal/orchestrator/memoryguard.go`, task 084) from wrapping the
non-durable, read-ungated `memoryguard.MemoryGuardStore[Plan]` to wrapping the
new `memoryguard.DurableStore[Plan]`, so a plan awaiting approval (or any
in-flight plan) survives a process restart, and `PlanStore.Get` finally calls
`ValidateRead`, closing the gap `internal/memoryguard/store.go:77-84` has
documented since task 084.

**This is a narrow, mechanical swap**, not a redesign: `MemoryGuardPlanStore`'s
public methods (`TryPut`, `Put`, `Get`, `Delete`, `TryDelete`, `StoredID`) keep
their EXACT signatures (it still implements `orchestrator.PlanStore`/
`TamperAwarePlanStore` unmodified); only the field type and constructor change.

**Module boundary:** `internal/orchestrator` only (`memoryguard.go`). No change
to `internal/memoryguard` (task 172's `DurableStore[P]` is consumed, not
modified) or to any other orchestrator file (the `PlanStore` interface, `Handle`,
`Resume`, `dispatchPlan`, etc. are all unaffected, they already work against the
`PlanStore` interface, not `MemoryGuardPlanStore`'s concrete type).

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-173-01 | `MemoryGuardPlanStore.inner` changes from `*memoryguard.MemoryGuardStore[Plan]` to `*memoryguard.DurableStore[Plan]`, requiring a `dir string` (or path derived from an env var) at construction; `NewMemoryGuardPlanStore`/`NewMemoryGuardPlanStoreWithRunner` gain the needed parameter, existing call sites updated. | TC-173-01 |
| REQ-173-02 | `Get` now calls `ValidateRead` (via `DurableStore.Get`); a `ErrReadGateDenied` from memory-guard causes `Get` to return `(Plan{}, false)` (the void `PlanStore.Get` signature has no error return, so a denial is treated as "not found", matching the interface's existing contract, the tamper/denial detail is available via a new error-returning `TryGet` if the executor judges callers need it, optional). | TC-173-02, TC-173-03 |
| REQ-173-03 | `TryPut`/`Put`/`Delete`/`TryDelete`/`StoredID` behavior (write-gate, delete-verify, tamper detection) is unchanged, all pre-existing `internal/orchestrator` tests referencing `MemoryGuardPlanStore` pass unmodified except for the constructor's new parameter. | TC-173-04 |
| REQ-173-04 | A plan `Put` by one `Orchestrator`/`MemoryGuardPlanStore` instance is visible to a FRESH, independently-constructed `MemoryGuardPlanStore` sharing the same durable directory (the cross-restart proof, `PlanStore` state now survives a crash, not just memory-guard's write-gate). | TC-173-05 |
| REQ-173-05 | `NewPlanStoreFromEnv` gains a new required-when-memory-guard-is-configured directory parameter (e.g. `AGENT_BUILDER_PLAN_STORE_DIR`, default a sensible path under the existing `TaskRoot`/temp convention if unset, executor's choice, documented) and continues to degrade gracefully to `MemoryPlanStore` when `AGENT_BUILDER_MEMORY_GUARD_BIN` is unset, unaffected. | TC-173-06 |
| REQ-173-06 | Pre-existing `internal/orchestrator` suites pass unchanged apart from the constructor signature update. | TC-173-07 |

---

## Pre-implementation checklist

- [x] Task 172 merged (`memoryguard.DurableStore[P]` exists)
- [x] Task 084 merged (`MemoryGuardPlanStore`, `orchestrator.PlanStore`,
  `TamperAwarePlanStore` all exist)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-173-01, constructor signature updated, existing call sites compile

- **Requirement:** REQ-173-01
- **Level:** L2 (compile-time + basic construction)
- **Test file:** `internal/orchestrator/memoryguard_test.go` (extend)

**Step:** `NewMemoryGuardPlanStore(binPath, identity, t.TempDir())` (new
signature). `NewMemoryGuardPlanStoreWithRunner(binPath, identity, t.TempDir(),
runner)`.

**Expected output:** both construct successfully; `var _ PlanStore =
(*MemoryGuardPlanStore)(nil)` and `var _ TamperAwarePlanStore =
(*MemoryGuardPlanStore)(nil)` still compile (unchanged interface conformance).

---

### TC-173-02, `Get` fails closed on a read-gate denial

- **Requirement:** REQ-173-02
- **Level:** L2

**Step:** `TryPut(Plan{GoalID: "g1"})` (write-gate allowed via stub). Stub the
underlying `ValidateRead` to deny. `Get("g1")`.

**Expected output:** `(Plan{}, false)`, matching the `PlanStore.Get` interface's
existing "not found" contract, NOT the plan value (fail closed, the load-bearing
assertion this task exists to prove: a denied read never leaks the plan).

---

### TC-173-03, `Get` returns the plan on allow

- **Requirement:** REQ-173-02
- **Level:** L2

**Step:** `TryPut(Plan{GoalID: "g1"})`, allow `ValidateRead`, `Get("g1")`.

**Expected output:** `(Plan{GoalID: "g1"}, true)`.

---

### TC-173-04, write-gate/delete-verify/tamper behavior unchanged

- **Requirement:** REQ-173-03
- **Level:** L2 (regression, re-runs the pre-existing task-084 test scenarios
  against the new constructor)

**Step:** Re-run `TestMemoryGuardPlanStore*`'s existing write-gate-denial,
delete-verify-tamper, and `StoredID` assertions, updated only to call the new
constructor signature.

**Expected output:** byte-identical pass/fail outcomes to before this task.

---

### TC-173-05, cross-restart durability for a stored plan

- **Requirement:** REQ-173-04
- **Level:** L5 (two independently-constructed `MemoryGuardPlanStore` sharing
  one durable directory, mirroring TC-172-08's pattern one layer up)

**Step:** `store1 := NewMemoryGuardPlanStore(bin, id, dir)`, `TryPut(Plan{GoalID:
"g1", SubGoals: [...]})` (allowed). Construct `store2 := NewMemoryGuardPlanStore(bin,
id, dir)` fresh. `Get("g1")` (allow `ValidateRead`).

**Expected output:** `store2.Get("g1")` returns the same `Plan` value `store1`
stored, field-for-field, proving `PlanStore` state now survives a restart, not
just a write-gate check within one process's lifetime.

---

### TC-173-06, `NewPlanStoreFromEnv` unaffected when memory-guard is unset

- **Requirement:** REQ-173-05
- **Level:** L2 (regression)

**Step:** Call `NewPlanStoreFromEnv(logFn)` with `AGENT_BUILDER_MEMORY_GUARD_BIN`
unset.

**Expected output:** returns `*MemoryPlanStore` (unchanged), the structured
warning log fires exactly as before this task, no `internal/memoryguard`
construction attempted at all.

---

### TC-173-07, full regression

- **Requirement:** REQ-173-06
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/orchestrator/... ./internal/memoryguard/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

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

## Out of scope

- Any change to `orchestrator.PlanStore`/`TamperAwarePlanStore`'s interface
  shape.
- Any change to `Handle`/`Resume`/`dispatchPlan`'s logic (they already work
  against the `PlanStore` interface unmodified).
- General-purpose goal/skill/context memory beyond the `PlanStore` use case
  (this task swaps ONE consumer's backend; a broader "orchestrator remembers
  past goals across sessions" feature is a follow-on, not required here).
