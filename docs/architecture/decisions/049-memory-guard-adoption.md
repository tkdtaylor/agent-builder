# ADR 049 — memory-guard adoption: binary IPC adapter leaf guarding the orchestrator's PlanStore

**Status:** Accepted (2026-06-28) — design-only. Resolves OQ-6 (memory-guard: Go
library vs binary IPC) and scopes task 084 so its stub test spec can be expanded and
implemented. No code/spec/diagram change lands with this ADR.
**Date:** 2026-06-28
**Extends:** ADR 042 (memory-guard guards the orchestrator's long-lived goal/fleet
state), ADR 046 §3 + task 081 (plan state held behind a swappable `PlanStore` interface,
in-memory v1, "task 084 swaps the backend"), and the block-adapter pattern of ADR 026
(`internal/audit` wraps the `audit-trail` binary over IPC; the block is not importable).
**Motivated by:** task 084 (`docs/tasks/backlog/084-memory-guard-adoption.md`) readiness
gate: "memory-guard block API surveyed; adoption ADR written if warranted."

## Context — block survey (2026-06-28)

Surveyed `~/Code/Public/memory-guard` (`module github.com/tkdtaylor/memory-guard`, Go
1.26). Findings:

1. **`package main` — not importable as a Go library.** Like agent-mesh (ADR 045) and
   audit-trail (ADR 026), the block is a standalone binary, not a package agent-builder
   can `import`.
2. **It exposes a JSON IPC contract**, both as a `serve` unix-socket daemon and one-shot
   CLI verbs:
   - `validate_write(entry, identity) -> { allow, stored_id, flags }` — the **write-gate**
     (fail-closed on suspected poisoning/injection; PII redaction).
   - `validate_read(query, identity) -> { allow, content_redacted, flags }`.
   - `verify_delete(id) -> { confirmed, residue_detected, residue_summary?, deletion_hash }`
     — the **post-deletion verification** (the "delete-verify" REQ-084-02 needs).
   - `ping`.
   IPC framing: `{"op":"validate_write","entry":"…"}` etc. over the socket.
3. Its `## Scope` explicitly owns the memory write/read/delete boundary and disclaims
   prompt/tool-call guarding (armor), secret brokering (vault), and authorization
   (policy-engine) — so agent-builder composes it over its published contract, not by
   absorbing it.

## Decision

### 1. Adopt via a **binary IPC adapter leaf `internal/memoryguard`** — not a library import

Mirror `internal/audit`/`internal/policy`/`internal/vault`: a thin leaf adapter that
speaks memory-guard's JSON IPC contract to the configured binary. The leaf maps the two
verbs task 084 needs — `validate_write` (write-gate) and `verify_delete` (delete-verify)
— to typed Go calls, and stays a leaf: its only `agent-builder/internal/` imports are the
ones a leaf adapter legitimately needs (no executor/runtime/orchestrator/LLM/web). A
fitness check asserts the isolation (REQ-084-03), mirroring F-005/F-007/F-011.

**Transport:** follow the audit-trail adapter precedent — the exact transport (a one-shot
subprocess per op vs a persistent `serve`-socket connection) is an implementation detail
the executor confirms against the binary; the orchestrator's goal/fleet store is
low-frequency, so per-op subprocess is acceptable, but the socket `serve` protocol (which
returns the structured JSON contract responses) is the cleaner fit and is preferred if it
is straightforward to drive. Either way the adapter consumes the **published JSON
contract**, version-tolerantly.

### 2. The memory-guarded backend slots behind task 081's `PlanStore` seam

Task 081 deliberately put plan/fleet state behind a `PlanStore` interface with an
in-memory `MemoryPlanStore` v1 and a `WithPlanStore` option "so task 084 can swap the
backend." Task 084 provides a `memoryguard`-backed `PlanStore`:
- **Writes** (orchestrator persisting a goal/plan/fleet update) go through
  `validate_write` — the write-gate. A write the guard rejects (`allow=false`) is a
  fail-closed error, not a silent drop.
- **Deletes** go through `verify_delete` — a delete that reports `confirmed=false` or
  `residue_detected=true` is a **tamper signal**.

The orchestrator does not learn a new concept; it keeps calling `PlanStore`, and the
backend is swapped at construction.

### 3. Degraded mode when unconfigured (REQ-084-04)

When `AGENT_BUILDER_MEMORY_GUARD_BIN` is unset (or the binary is absent), the orchestrator
**degrades gracefully to the in-memory `MemoryPlanStore` (task 081) with a structured
warning log** — it does not fail to start. This keeps existing e2e tests
(`TestPhase0EndToEndAcceptance`) passing unchanged with no memory-guard binary in CI. This
mirrors ADR 033/045's "in-memory v1, durable backend a named follow-on, fail-soft when the
block is absent" posture. The warning names the missing config so the operator knows
durability/guarding is off.

### 4. Tamper-detected → halt the plan + audit (REQ-084-05)

When the guard reports tamper (a `verify_delete` residue, or a read that the guard flags
as tampered), the orchestrator **halts the in-flight plan** and emits an `audit.AuditEvent`
carrying `tamper_detected=true`. A guarded store that detects tampering must stop the
fleet, not continue on poisoned state — this is the whole point of adopting the guard.

## Consequences

- **Design-only.** No code/spec/diagram lands with this ADR; task 084 makes them.
- **Task 084 unblocked.** TCs expand concretely against a **stub memory-guard binary**
  (the task already specifies "memory-guard stub returns tamper-detected"): TC-084-01
  write→`validate_write`; TC-084-02 delete-bypass→tamper on next read; TC-084-03 leaf
  fitness (`make fitness-memoryguard-isolation`, next free F-number); TC-084-04 unset→
  in-memory + warning + e2e unchanged; TC-084-05 tamper→halt + audit `tamper_detected=true`.
- **No new block coupling beyond the contract.** Like every other block adapter,
  `internal/memoryguard` depends only on the JSON IPC contract; a memory-guard version
  bump that preserves the contract needs no agent-builder change.
- **Feeds task 085.** The fleet audit chain (085) records the `tamper_detected` events;
  the guarded PlanStore is part of the "orchestrator is itself contained/gated/audited"
  story.
- **agent-builder never edits memory-guard.** It runs the shipped binary; the block stays
  a sibling composed over its contract (ADR 040/042).
- **Invariants hold:** the guard is fail-closed on writes; degraded mode is explicit and
  logged, never silent; tamper halts rather than continues; the adapter is a leaf off the
  supervisor/orchestrator import graphs.
