# Test Spec 039: audit.ChainWriter

**Linked task:** [`docs/tasks/backlog/039-audit-chain-writer.md`](../backlog/039-audit-chain-writer.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-039-01 | TC-039-01 | ⏳ |
| REQ-039-02 | TC-039-02, TC-039-03 | ⏳ |
| REQ-039-03 | TC-039-04 | ⏳ |
| REQ-039-04 | TC-039-05 | ⏳ |
| REQ-039-05 | TC-039-06 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-039-01: ChainWriter satisfies audit.Sink at compile time

- **Requirement:** REQ-039-01
- **Input:** a `*audit.ChainWriter` value assigned to a `var _ audit.Sink` blank identifier.
- **Expected output:** compiles without a type assertion; `ChainWriter` implements `Append(AuditEvent) error` and `Seal() error`.
- **Edge cases:** the writer accepts an `io.Writer` (or a path) at construction so tests can target an in-memory buffer with no real file.

### TC-039-02: first record's prev_hash is the genesis sentinel; each record's hash chains the previous

- **Requirement:** REQ-039-02
- **Input:** three `AuditEvent`s appended in order to a `ChainWriter` over a buffer.
- **Expected output:** the output is NDJSON, one JSON object per line. The first record's `prev_hash` is the fixed genesis value (e.g. 64 zero hex chars or a documented constant). Each subsequent record's `prev_hash` equals the previous record's `hash`. Each record's `hash` is the SHA-256 (hex) of the previous record's **canonical bytes** as defined in TC-039-03.
- **Edge cases:** appending zero events then `Seal` produces an empty (zero-line) chain that TC-039-06 / task 040 treats as a valid empty chain.

### TC-039-03: canonical encoding is deterministic and independent of field order

- **Requirement:** REQ-039-02
- **Input:** the same logical `AuditEvent` appended in two separate writer runs; and two events whose Go struct fields are populated in different source order.
- **Expected output:** the canonical bytes hashed into the chain are byte-identical across runs for the same logical event — keys are emitted in a fixed (e.g. lexicographic) order, no insignificant whitespace, a fixed timestamp format. Re-running the writer over identical input yields a byte-identical file.
- **Edge cases:** the `hash`/`prev_hash` fields themselves are excluded from the canonical bytes that get hashed (a record cannot hash itself); only the event payload + chain linkage parent is hashed in the documented way.

### TC-039-04: a written chain round-trips — every line parses and the recomputed chain matches

- **Requirement:** REQ-039-03
- **Input:** a `ChainWriter` writes N events to a temp file; the file is read back line by line and each record's `hash` is recomputed from its predecessor's canonical bytes.
- **Expected output:** every line is valid JSON carrying `prev_hash` + `hash` plus the typed event fields; the recomputed hashes match the stored hashes for all N records (the chain the writer produced is internally consistent).
- **Edge cases:** a single-event chain links to the genesis sentinel and verifies.

### TC-039-05: Append after Seal fails; Seal is idempotent-safe

- **Requirement:** REQ-039-04
- **Input:** a `ChainWriter`; `Append` then `Seal` then `Append`.
- **Expected output:** the post-Seal `Append` returns a non-nil error (the chain is closed); `Seal` flushes/closes the underlying writer and surfaces any flush error. A second `Seal` does not panic and does not corrupt the file.
- **Edge cases:** an `Append` whose `AuditEvent` fails validation (per task 038) returns the validation error and writes nothing — the chain is not advanced by a rejected event.

### TC-039-06: fixture chain bytes are stable (golden test)

- **Requirement:** REQ-039-05
- **Input:** a fixed sequence of `AuditEvent`s with fixed (injected) timestamps written through the `ChainWriter`.
- **Expected output:** the produced bytes equal a checked-in golden fixture; the genesis `prev_hash` and the final `hash` match documented expected hex values. This pins the canonical-encoding + hashing contract so a future refactor cannot silently change the on-disk format.
- **Edge cases:** timestamp must be injectable (clock seam) so the golden test is deterministic; a non-injected wall clock would make the fixture non-reproducible.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in existing tests
- [ ] L5 harness (write-then-read a real chain file) passes

## Test framework notes

Framework: Go `testing` with golden fixtures and an injectable clock. The hash chain uses `crypto/sha256` over canonical JSON bytes; canonical encoding must be hand-controlled (sorted keys, fixed timestamp format) rather than relying on `encoding/json` map ordering by accident. The verifier that *detects tampering* is task 040 — this task only proves the writer produces a self-consistent, deterministic chain.
