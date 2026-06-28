# Test spec — Task 084: memory-guard adoption (orchestrator goal/fleet state)

**Linked task:** `docs/tasks/backlog/084-memory-guard-adoption.md`
**Written:** 2026-06-27
**Expanded:** 2026-06-28 (full acceptance criteria; stub binary IPC transport; ADR 049)
**Status:** active

## Context

memory-guard provides write-gate + delete-verify for long-lived state stores. This
task adopts it to guard the orchestrator's goal/fleet state — the in-flight plan,
the set of active workers, and their aggregated results — so that state is tamper-
evident and guarded against accidental overwrite or deletion.

**Adoption shape (ADR 049):** memory-guard is `package main` (not importable as a
Go library). agent-builder adopts it via a binary IPC adapter leaf `internal/memoryguard`
mirroring the `internal/audit`/`internal/policy`/`internal/vault` pattern. Transport:
per-op subprocess (JSON request → JSON response on stdout). The adapter maps two verbs:

- `validate_write(entry, identity) → { allow, stored_id, flags }` — the write-gate
- `verify_delete(id) → { confirmed, residue_detected, residue_summary?, deletion_hash }` — the delete-verify

The memory-guarded backend slots behind task 081's `PlanStore` interface using the
`WithPlanStore` option. Tests run against a stub memory-guard binary (a Go test binary
written to a temp dir).

## Requirements coverage

| Req ID     | Description                                                                   | Test cases |
|------------|-------------------------------------------------------------------------------|------------|
| REQ-084-01 | Orchestrator writes goal/fleet state through memory-guard write-gate           | TC-084-01  |
| REQ-084-02 | Delete-verify prevents silent deletion of fleet state                          | TC-084-02  |
| REQ-084-03 | internal/memoryguard is a leaf; orchestrator reaches it only over IPC          | TC-084-03  |
| REQ-084-04 | memory-guard absent → in-memory-only mode with a logged warning                | TC-084-04  |
| REQ-084-05 | Tampered state is detected and the orchestrator halts on tamper                | TC-084-05  |

---

## Test cases

### TC-084-01 — Orchestrator writes a goal through validate_write; stub returns success; handle held

- **Requirement:** REQ-084-01
- **Level:** L2 (unit test with stub memory-guard subprocess)
- **Status:** active

**Setup:**
- Build a stub memory-guard binary into a temp dir that:
  - For `validate_write`: reads `{"op":"validate_write","entry":"…","identity":"…"}` and writes
    `{"allow":true,"stored_id":"stub-id-1","flags":null}` then exits 0.
  - Panics or exits non-zero for unrecognised ops.
- Construct `memoryguard.Client` pointing at the stub binary.
- Construct a `memoryguard.MemoryGuardPlanStore` wrapping the client.
- Call `Put(plan)` with `Plan{Goal: "test-goal", GoalID: "g1", SubGoals: [...]}`

**Expected assertions:**
1. `Put` returns `nil` (no error).
2. The stub binary was invoked once with op `validate_write`.
3. The entry field in the IPC request encodes a non-empty string (the marshalled plan).
4. The returned `stored_id` is held by the store (surfaced as a non-empty ID via `StoredID("g1")`).
5. `Get("g1")` returns the plan and `ok=true`.

**Spy mechanism:** the stub binary writes a JSON receipt line to a temp file before its
response so the test can assert op + entry without parsing stdout capture.

---

### TC-084-02 — Delete-verify: simulated bypass → tamper-detected on subsequent Get

- **Requirement:** REQ-084-02
- **Level:** L2 (unit test with stub binary)
- **Status:** active

**Setup:**
- Build a stub binary that:
  - For `validate_write`: returns `{"allow":true,"stored_id":"stub-id-2","flags":null}`.
  - For `verify_delete`: returns `{"confirmed":false,"residue_detected":true,"residue_summary":"unexpected residue","deletion_hash":"abc123"}`.
- Construct `memoryguard.MemoryGuardPlanStore`.
- Call `Put(plan)` (write succeeds, stored_id held).
- Call `Delete("g1")` — this invokes `verify_delete` with the held stored_id.

**Expected assertions:**
1. `Delete("g1")` returns a non-nil error wrapping `ErrTamperDetected`.
2. The error message contains `"tamper"` (case-insensitive).
3. `Get("g1")` returns `ok=false` (the entry is not in the store even though Delete
   signalled tamper — tamper means we cannot trust the state; we remove it from the
   in-process index and surface the error to the caller).

---

### TC-084-03 — internal/memoryguard is a leaf (fitness check F-012)

- **Requirement:** REQ-084-03
- **Level:** L3 (fitness check)
- **Status:** active

**Input:** `go list -deps ./internal/memoryguard/...`

**Expected assertions:**
1. The dependency list contains no `github.com/tkdtaylor/agent-builder/internal/` path
   other than `internal/memoryguard` itself.
2. `make fitness-memoryguard-isolation` target exists and exits 0.
3. `make fitness` includes `fitness-memoryguard-isolation` as a prerequisite.
4. `docs/spec/fitness-functions.md` has a row for F-012 with `make fitness-memoryguard-isolation` as the check command.

---

### TC-084-04 — AGENT_BUILDER_MEMORY_GUARD_BIN unset → in-memory-only mode + warning + e2e unchanged

- **Requirement:** REQ-084-04
- **Level:** L2 (unit test) + L2 e2e regression guard
- **Status:** active

**Sub-test A — structured warning log:**
- Call `memoryguard.NewPlanStoreFromEnv(logFunc)` with `AGENT_BUILDER_MEMORY_GUARD_BIN`
  unset (empty env).
- **Assertions:**
  1. Returns a non-nil `PlanStore` (the `MemoryPlanStore` v1).
  2. `logFunc` was called exactly once with a structured warning containing BOTH:
     - the literal string `"AGENT_BUILDER_MEMORY_GUARD_BIN"` (naming the missing config)
     - the literal string `"memory-guard"` or `"memoryguard"` (naming the disabled component)
  3. The returned store satisfies the full `orchestrator.PlanStore` interface (Put/Get/Delete
     work without error in the caller's process — no subprocess involved).

**Sub-test B — e2e regression guard:**
- `TestPhase0EndToEndAcceptance` (in `tests/e2e/`) must pass UNCHANGED when
  `AGENT_BUILDER_MEMORY_GUARD_BIN` is absent from the test environment.
- This is asserted by running `go test -count=1 ./tests/e2e/` without setting
  `AGENT_BUILDER_MEMORY_GUARD_BIN` — the test framework must not inject it.
- The orchestrator's live path uses `MemoryPlanStore` in this case (no IPC, no panic).

**Live-path producer-consumer trace (required):**
- `runtime.ConfigFromEnv` or the CLI wiring must read `AGENT_BUILDER_MEMORY_GUARD_BIN`
  and either construct a `MemoryGuardPlanStore` (set) or call `memoryguard.NewPlanStoreFromEnv`
  (unset) before constructing the `Orchestrator`.
- The `Orchestrator.store` field is the result (MemoryPlanStore or MemoryGuardPlanStore).
- Trace: `runtime.ConfigFromEnv` → env read → store constructed → `orchestrator.New(…, WithPlanStore(store))`.

---

### TC-084-05 — Tamper-detected → orchestrator halts plan; audit FakeSink receives event with tamper_detected=true

- **Requirement:** REQ-084-05
- **Level:** L2 (unit test with stub binary returning tamper, plus audit.FakeSink)
- **Status:** active

**Setup:**
- Build a stub binary that:
  - For `validate_write`: returns `{"allow":true,"stored_id":"stub-id-3","flags":null}`.
  - For `verify_delete`: returns `{"confirmed":false,"residue_detected":true,"residue_summary":"injected","deletion_hash":"deadbeef"}`.
- Construct `memoryguard.MemoryGuardPlanStore` backed by the stub.
- Construct `audit.FakeSink`.
- Construct a `memoryguard.GuardedOrchestrator` (or wire via `orchestrator.New` with
  `WithPlanStore` and `WithAuditSink`) with:
  - policy: `fakePolicy{decision: DecisionRequireApproval}` (so the plan is paused and stored)
  - store: the MemoryGuardPlanStore
  - audit sink: FakeSink
- Call `Handle(ctx, goalTask)` → plan is stored (validate_write fires, succeeds).
- Call `Resume(ctx, Approval{…Approved:true})`:
  - Resume calls `store.Delete(goalID)` internally.
  - `verify_delete` returns tamper signal.
  - `Delete` returns an `ErrTamperDetected`-wrapping error.
  - The orchestrator must detect this, HALT the plan (return error, not dispatch sub-goals),
    and emit an `audit.AuditEvent` through the Sink with `Detail.TamperDetected == true`.

**Expected assertions:**
1. `Resume` returns a non-nil error containing `"tamper"` (case-insensitive).
2. No sub-goal dispatch occurred (dispatchSpy.count() == 0).
3. `audit.FakeSink.Events()` contains at least one event where `Detail.TamperDetected == true`.
4. The tamper event's `Action` is one of the defined `audit.AuditAction` constants
   (the spec must extend `audit.EventDetail` with a `TamperDetected bool` field and
   define an `ActionTamper` constant or reuse `ActionEscalate` — the test asserts the
   exact field value `true`, not just "an event arrived").

**Note on audit.EventDetail extension:** task 084 adds `TamperDetected bool` to
`audit.EventDetail`. The FakeSink.Events() assertion inspects `ev.Detail.TamperDetected`.
The existing `audit.Validate` function must accept events carrying this new field
(it only validates `Action` and `Verdict`; new `EventDetail` fields are always valid).

---

## Verification plan

- **Highest level achievable:** L2 with stub memory-guard binary. L5 requires a live
  memory-guard binary.
- **L2/L3 harness command:**
  ```
  go test -count=1 ./internal/memoryguard/... ./internal/orchestrator/... ./internal/audit/... ./tests/e2e/...
  make fitness-memoryguard-isolation
  make check
  ```
  Expected: `ok`; PASS; `All checks passed.`

## Out of scope

- Orchestrator containment + policy gating (task 085).
- State migration / schema evolution across memory-guard versions.
- The `validate_read` verb (read tamper-check) — deferred.
- Persistent socket `serve` mode — per-op subprocess is used for this low-frequency store.
