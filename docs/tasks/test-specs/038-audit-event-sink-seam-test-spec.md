# Test Spec 038: audit.AuditEvent taxonomy + Sink seam

**Linked task:** [`docs/tasks/backlog/038-audit-event-sink-seam.md`](../backlog/038-audit-event-sink-seam.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-038-01 | TC-038-01, TC-038-02 | ⏳ |
| REQ-038-02 | TC-038-03 | ⏳ |
| REQ-038-03 | TC-038-04, TC-038-05 | ⏳ |
| REQ-038-04 | TC-038-06 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-038-01: action taxonomy is a closed enum covering every emitted lifecycle action

- **Requirement:** REQ-038-01
- **Input:** the exported `AuditAction` constant set declared by `internal/audit`.
- **Expected output:** exactly the actions the run loop emits today are representable as typed constants — `containment`, `pick`, `attempt`, `verify`, `publish`, `escalate`, `finish`. Each constant has a stable string value usable as the NDJSON `action` field. A `verify` event additionally carries a typed `verdict` (e.g. `pass`/`fail`) so the gate result is queryable rather than free-text.
- **Edge cases:** the enum is closed — a `String()`/`Valid()` helper (or equivalent) reports an unknown action value as invalid rather than silently accepting it. No action constant maps to raw stdout/stderr (those stay in the 019 RunRecord, not the audit taxonomy).

### TC-038-02: AuditEvent carries the structured fields an auditor needs

- **Requirement:** REQ-038-01
- **Input:** an `AuditEvent` constructed for each action kind (e.g. a `publish` event with branch/remote, a `verify` event with verdict, a `finish` event with outcome).
- **Expected output:** `AuditEvent` exposes typed fields: the action, a run identifier, a task identifier, an optional verdict (for `verify`), an optional outcome (for `finish`), and an action-detail field for the remaining structured context. Constructing an event does not require stringly-typed maps at the call site.
- **Edge cases:** a zero-value `AuditEvent` (no action set) is detectable as invalid via the same `Valid()` path as TC-038-01.

### TC-038-03: Sink interface defines Append + Seal and nothing executor/LLM/web-shaped

- **Requirement:** REQ-038-02
- **Input:** the `Sink` interface declaration in `internal/audit`.
- **Expected output:** `Sink` is exactly `interface { Append(AuditEvent) error; Seal() error }`. The interface mirrors `sandbox.Runner`'s shape — a single small typed contract the supervisor can depend on without importing a concrete backend.
- **Edge cases:** the interface accepts the typed `AuditEvent`, never an `any`/`map[string]any`; `Seal` returns an error so a flush/close failure is observable, not swallowed.

### TC-038-04: FakeSink records appended events in order

- **Requirement:** REQ-038-03
- **Input:** a `FakeSink`; three `AuditEvent`s appended in sequence (`pick`, `attempt`, `finish`).
- **Expected output:** `FakeSink.Append` returns nil for each; an accessor (e.g. `Events()`) returns the three events in append order with their fields intact. No file is written and no I/O is performed (mirrors `sandbox.FakeRunner`).
- **Edge cases:** `Events()` returns a copy, so a caller mutating the returned slice does not corrupt the fake's internal record.

### TC-038-05: FakeSink records Seal and is usable as a Sink at compile time

- **Requirement:** REQ-038-03
- **Input:** a `*FakeSink` assigned to a `var _ audit.Sink` blank identifier; `Seal()` called after appends.
- **Expected output:** the assignment compiles (FakeSink satisfies `Sink`); after `Seal()`, the fake reports it was sealed (e.g. `Sealed()` true / a recorded seal count), enabling supervisor-side tests to assert the sink was sealed before teardown.
- **Edge cases:** an `Append` after `Seal` is recorded distinctly (returns an error or sets a flag) so a write-after-seal bug in a consumer is catchable.

### TC-038-06: invalid AuditEvent is rejected, not silently accepted

- **Requirement:** REQ-038-04
- **Input:** an `AuditEvent` with an unset/unknown action, or a `verify` event with no verdict, passed through the event validation helper.
- **Expected output:** validation returns a non-nil error naming the offending field; a sink implementation is expected to surface that error from `Append` rather than write a malformed event. (FakeSink may either reject on `Append` or expose the validation helper directly — the helper itself must reject.)
- **Edge cases:** a valid event with an optional detail field absent (e.g. a `pick` event with no extra detail) passes validation.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in existing tests
- [ ] `internal/audit` is a leaf package (no executor/LLM/web imports) — confirmed by inspection here, enforced by F-005 in task 042

## Test framework notes

Framework: Go `testing`, table-driven over the action enum and event validation. No subprocess, no filesystem — this task is pure typed scaffolding (the seam + taxonomy + in-process fake), mirroring how `internal/sandbox`'s `run.go` is unit-tested without containment. The `ChainWriter` (task 039), `Verify` (task 040), and supervisor wiring (task 041) are explicitly out of scope here.
