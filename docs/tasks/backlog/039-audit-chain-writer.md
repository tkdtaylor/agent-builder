# Task 039: audit.ChainWriter

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Implement the production `audit.Sink`: an append-only NDJSON `ChainWriter` where each record carries `prev_hash` + `hash` (SHA-256 over the previous record's canonical bytes), with a deterministic canonical encoding — the tamper-evident on-disk chain format that makes the audit log harder to silently rewrite than git.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/025-audit-trail-v0-hash-chained-reader.md` decision + Consequences — "append-only NDJSON where each record carries the SHA-256 of the previous record's canonical bytes." Tamper-*evident*, not tamper-*proof* (a full-file rewrite by a privileged attacker is the deferred upgrade; v0 detects edit-in-place + truncation, verified in task 040).
- Builds on task 038's `audit.AuditEvent` + `Sink` interface — `ChainWriter` is the production implementation of that seam.
- **Model tier: deep (opus)** — integrity / canonical-bytes correctness is load-bearing; a wrong canonicalization silently weakens tamper-evidence.
- Dependencies: 038.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-039-01 | `ChainWriter` implements `audit.Sink` (`Append`/`Seal`) over an `io.Writer`/path | must have |
| REQ-039-02 | Append-only NDJSON; first record links to a documented genesis sentinel; each record's `hash` = SHA-256(hex) of the previous record's canonical bytes; each `prev_hash` = the previous record's `hash` | must have |
| REQ-039-03 | A written chain round-trips: every line parses and recomputed hashes match stored hashes (internally consistent) | must have |
| REQ-039-04 | `Append` after `Seal` fails; a validation-failing event writes nothing and does not advance the chain; `Seal` flushes/closes and surfaces flush errors | must have |
| REQ-039-05 | Canonical encoding is deterministic (sorted keys, fixed timestamp format via an injectable clock, `hash`/`prev_hash` excluded from the hashed bytes), pinned by a golden fixture | must have |
| REQ-039-06 | `docs/spec/data-model.md` documents the chained AuditEvent NDJSON wire format (fields, genesis sentinel, hashing rule, tamper-evident-not-proof limit) in the same commit | must have |

## Readiness gate

- [x] Test spec `039-audit-chain-writer-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 038 complete

## Acceptance criteria

- [ ] [REQ-039-01] `var _ audit.Sink = (*audit.ChainWriter)(nil)` compiles; the writer is constructed over a buffer or path
- [ ] [REQ-039-02] Output is one JSON object per line; first `prev_hash` is the genesis sentinel; each `hash` is SHA-256 of the predecessor's canonical bytes and each `prev_hash` equals the predecessor's `hash`
- [ ] [REQ-039-03] Reading a written file back and recomputing every hash matches the stored hashes for all records, including a single-event chain
- [ ] [REQ-039-04] Post-`Seal` `Append` errors; a validation-failing event writes nothing; second `Seal` does not panic or corrupt the file
- [ ] [REQ-039-05] Re-running the writer over identical input + injected clock yields byte-identical output equal to a checked-in golden fixture; the hashed bytes exclude `hash`/`prev_hash`
- [ ] [REQ-039-06] `docs/spec/data-model.md` records the chained NDJSON format and the tamper-evident (not -proof) limit in the feat commit

## Verification plan

- **Highest level achievable:** L5 — a harness writes a chain to a real file, reads it back, and asserts every recomputed hash matches the stored chain (the runtime artifact is the chain file).
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./internal/audit/... -run 'TestChainWriter|TestChainRoundTrip|TestChainGolden'
  ```
  Expected final assertion: a written-then-read chain file's recomputed hashes match the stored hashes for all N records (`TC-039-04`), and the golden fixture bytes match (`TC-039-06`).
- **Level 6 — Operator observation (if applicable):** optional — `cat` a produced chain file and confirm each `prev_hash` equals the prior line's `hash`. Not required for ✅ given L5 covers the runtime artifact.
- **Cross-module state risk:** names the on-disk `prev_hash`/`hash` chain fields and the genesis sentinel — task 040 (`Verify`) is the consumer and must agree on the exact canonical-bytes + genesis definition. A golden fixture pins the contract between writer and verifier.
- **Runtime-visible surface:** file output — the append-only NDJSON chain file on disk. The executor must produce a chain file and quote sample lines.

## Out of scope

- The tamper-detection reader (`audit.Verify`, task 040) — this task only proves the writer is self-consistent and deterministic.
- Supervisor wiring (task 041) and the fitness check (task 042).
- Cryptographic signing / external anchoring — deferred per ADR 025 Consequences (the defense against a full-file rewrite).
- **Egress-attempt audit events** — deferred and spike-gated per ADR 025 decision 2.

## Notes

- Canonical encoding is the load-bearing detail: sort keys, fix the timestamp format, inject the clock for golden tests, and exclude the `hash`/`prev_hash` fields from the bytes that get hashed (a record cannot hash itself).
- Append-only: the writer never rewrites an earlier line; `Seal` is the terminal flush/close.
- Update `docs/spec/data-model.md` in the same commit (chained NDJSON format + tamper-evident-not-proof limit, per ADR 025). Do not edit spec during backlog authoring.
