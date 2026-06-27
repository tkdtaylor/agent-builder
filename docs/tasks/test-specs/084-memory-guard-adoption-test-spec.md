# Test spec — Task 084: memory-guard adoption (orchestrator goal/fleet state)

**Linked task:** `docs/tasks/backlog/084-memory-guard-adoption.md`
**Written:** 2026-06-27
**Status:** stub — blocked by task 081 (orchestrator core)

## Context

memory-guard provides write-gate + delete-verify for long-lived state stores. This
task adopts it to guard the orchestrator's goal/fleet state — the in-flight plan,
the set of active workers, and their aggregated results — so that state is tamper-
evident and guarded against accidental overwrite or deletion.

This task is **blocked by task 081** (orchestrator core must own state before there
is state to guard).

**Detailed task shape is deferred** pending orchestrator core delivery. Shape
parameters that are known now:
- `internal/memoryguard` — a new adapter package (mirrors the `internal/vault`,
  `internal/policy` pattern).
- The block lives at `~/Code/Public/memory-guard` (write-gate + delete-verify, v0
  single commit).
- Must add an isolation fitness check mirroring F-005/F-006.
- memory-guard is opt-in via `AGENT_BUILDER_MEMORY_GUARD_BIN`; without it, the
  orchestrator uses in-memory-only state (safe for single-run; no durability guarantee).

## Requirements coverage (preliminary)

| Req ID     | Description                                                                   | Test cases |
|------------|-------------------------------------------------------------------------------|------------|
| REQ-084-01 | Orchestrator writes goal/fleet state through memory-guard write-gate           | TC-084-01  |
| REQ-084-02 | Delete-verify prevents silent deletion of fleet state                          | TC-084-02  |
| REQ-084-03 | internal/memoryguard is a leaf; orchestrator reaches it only over IPC          | TC-084-03  |
| REQ-084-04 | memory-guard absent → in-memory-only mode with a logged warning                | TC-084-04  |
| REQ-084-05 | Tampered state is detected and the orchestrator halts on tamper                | TC-084-05  |

## Pre-implementation checklist

- [ ] Task 081 merged (orchestrator core — state ownership clear)
- [ ] memory-guard block API surveyed (Go library vs binary IPC)
- [ ] ADR for memory-guard adoption written (if scope warrants one)
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-084-01 — Orchestrator writes a goal to memory-guard write-gate

- **Requirement:** REQ-084-01
- **Level:** L2 (unit test with stub memory-guard)
- **Status:** stub

**Input:** Orchestrator receives a new goal and writes it to the state store.

**Expected output:**
- The write goes through the memory-guard write-gate.
- A successful write returns a state handle the orchestrator holds.

---

### TC-084-02 — Delete-verify prevents silent state deletion

- **Requirement:** REQ-084-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** An attempt to delete a fleet-state entry bypassing the delete-verify
guard (direct file delete simulation).

**Expected output:**
- memory-guard detects the bypass; subsequent read returns a tamper-detected error.
- The orchestrator surfaces this as a critical error.

---

### TC-084-03 — internal/memoryguard is a leaf (fitness check)

- **Requirement:** REQ-084-03
- **Level:** L3 (fitness check)
- **Status:** stub

**Input:** `go list -deps ./internal/memoryguard/...`

**Expected output:**
- No `agent-builder/internal/` path other than `internal/memoryguard` itself.
- `make fitness-memoryguard-isolation` target added and wired.

---

### TC-084-04 — memory-guard absent → in-memory-only mode with a warning

- **Requirement:** REQ-084-04
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** `AGENT_BUILDER_MEMORY_GUARD_BIN` unset.

**Expected output:**
- Orchestrator starts successfully in in-memory-only mode.
- A structured warning log entry names the degraded mode.
- The existing Phase-0/Phase-1 e2e tests still pass (opt-in regression guard).

---

### TC-084-05 — Tampered state detected → orchestrator halts

- **Requirement:** REQ-084-05
- **Level:** L2 (unit test with stub memory-guard returning tamper-detected error)
- **Status:** stub

**Input:** memory-guard stub returns a tamper-detected error on a state read.

**Expected output:**
- Orchestrator halts the relevant plan (does not proceed with potentially corrupted
  state).
- Audit event emitted with `tamper_detected=true`.

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 with stub memory-guard. L5 requires a live
  memory-guard binary.
- **L2 harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/memoryguard/...
  ```
  Expected: `ok`.

## Out of scope

- Orchestrator containment + policy gating (task 085).
- State migration / schema evolution across memory-guard versions.
