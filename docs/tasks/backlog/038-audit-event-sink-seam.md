# Task 038: audit.AuditEvent taxonomy + Sink seam

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Stand up a new leaf package `internal/audit` owning a typed closed-enum `AuditEvent` action taxonomy, the `audit.Sink` seam interface, and an in-process `FakeSink` — the typed, compile-checked, fakeable contract the supervisor and later blocks depend on, with no writer and no supervisor change yet.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/025-audit-trail-v0-hash-chained-reader.md` (Option B, accepted) — the `internal/audit` package owns `AuditEvent`, `Sink`, `FakeSink`; this task is the taxonomy + seam slice only.
- Seam pattern to mirror: `docs/architecture/decisions/020-exec-sandbox-adapter-seam.md` and `internal/sandbox/run.go` (typed interface in a small package, in-process deterministic fake, supervisor depends only on the interface).
- The action vocabulary is the action-class lifecycle events the run loop already emits as `command` lines (`internal/runtime/run.go`): `containment=podman`, `pick task`, `attempt`, `verify`, `publish branch`, `escalated`, `finish … outcome=…`. Raw stdout/stderr stay in the unchanged 019 RunRecord, not in this taxonomy.
- **Model tier: balanced (sonnet)** — typed Go scaffolding behind a strict commit gate.
- Dependencies: none.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-038-01 | A typed, closed-enum `AuditEvent` action taxonomy covering containment, pick, attempt, verify (+verdict), publish, escalate, finish (+outcome), with structured typed fields — no `map[string]any` at the call site | must have |
| REQ-038-02 | A `Sink interface { Append(AuditEvent) error; Seal() error }` in `internal/audit`, mirroring the `sandbox.Runner` seam shape | must have |
| REQ-038-03 | An in-process `FakeSink` that records appended events and Seal, performs no I/O, and satisfies `Sink` at compile time (mirrors `sandbox.FakeRunner`) | must have |
| REQ-038-04 | Event validation rejects unset/unknown actions and missing required sub-fields (e.g. `verify` without a verdict) rather than silently accepting them | must have |
| REQ-038-05 | `docs/spec/data-model.md` gains the `AuditEvent` schema and `docs/spec/architecture.md` gains the `internal/audit` component row, in the same commit as the code | must have |

## Readiness gate

- [x] Test spec `038-audit-event-sink-seam-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Any blocking tasks are complete (none)

## Acceptance criteria

- [ ] [REQ-038-01] `internal/audit` declares a closed `AuditAction` enum with exactly {containment, pick, attempt, verify, publish, escalate, finish}; `AuditEvent` carries typed fields including an optional verdict (verify) and outcome (finish); no raw stdout/stderr action exists
- [ ] [REQ-038-02] `Sink` is `interface { Append(AuditEvent) error; Seal() error }`, accepting the typed `AuditEvent` and never `any`/`map`
- [ ] [REQ-038-03] `FakeSink` records appended events in order, records Seal, returns copies from accessors, performs zero I/O, and a `var _ audit.Sink = (*FakeSink)(nil)` assertion compiles
- [ ] [REQ-038-04] An event-validation helper returns a non-nil error naming the offending field for an unset/unknown action or a `verify` event missing its verdict
- [ ] [REQ-038-05] `docs/spec/data-model.md` documents the `AuditEvent` schema and `docs/spec/architecture.md` lists the `internal/audit` component, both in the feat commit

## Verification plan

- **Highest level achievable:** L2 only — internal typed scaffolding, unit-test-covered. This task adds no runtime-observable surface (no file is written, no CLI flag, no supervisor change yet); the writer is task 039 and the wiring is task 041. ✅ at L2 is appropriate here provided the row's `Verified by` says "unit-test-only; no runtime surface."
- **Level 5 — Validation harness command (if applicable):** N/A — no live runtime path until `ChainWriter` (039) and supervisor wiring (041) land.
- **Level 6 — Operator observation (if applicable):** N/A.
- **Level 2 evidence expected:**
  ```
  go test -count=1 ./internal/audit/...
  ```
  Expected final assertion: `ok github.com/tkdtaylor/agent-builder/internal/audit`
- **Cross-module state risk:** introduces the `AuditEvent` / `Sink` contract that tasks 039–041 consume — the producer (supervisor wiring, task 041) and the consumers (ChainWriter 039, Verify 040) must agree on the typed event shape this task fixes.
- **Runtime-visible surface:** none in this task.

## Out of scope

- The `ChainWriter` production sink (task 039), `Verify` reader (task 040), supervisor wiring (task 041), the fitness isolation check (task 042).
- Any supervisor or `internal/runtime` change — this task does not touch the write path.
- **Egress-attempt audit events** — deferred and spike-gated per ADR 025 decision 2; a conditional Phase 2 follow-up pending a proxy-exposure spike, not part of v0.

## Notes

- Keep the package a strict leaf — no executor/LLM/web imports — so task 042's F-005 fitness check stays trivially green. This mirrors the `sandbox` discipline.
- The taxonomy is the type the `Sink` accepts; getting the field set right here is what lets 039/041 stay mechanical.
- Update spec in the same commit (new `AuditEvent` data-model entry, new architecture component row). Do not edit spec during backlog authoring.
