# Task 040: audit.Verify

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Implement `audit.Verify`: a chain reader/verifier that walks a `ChainWriter`-produced chain file and reports the **first** broken link, detecting edit-in-place, reorder, and truncation — the read side that makes the tamper-evidence actually checkable.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/025-audit-trail-v0-hash-chained-reader.md` — "a companion `audit.Verify` walks the chain and reports the first broken link." Decision 3 makes `Verify` over a produced run's chain a **block-severity** check. The v0 boundary (Consequences) is edit-in-place + truncation detection, not defense against a privileged full-file rewrite.
- Consumes the on-disk format and genesis sentinel fixed by task 039 (golden fixture is the shared contract).
- **Model tier: deep (opus)** — the adversarial tamper-case test design is the value of this task; the three tamper classes must each be localized to the first break.
- Dependencies: 039.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-040-01 | `audit.Verify(path)` returns a report with `OK`, record count, first/last hash; an intact chain (including empty and single-record) verifies OK against the genesis sentinel | must have |
| REQ-040-02 | Edit-in-place is detected and localized to the first broken link (including an edit to the final record) | must have |
| REQ-040-03 | Record reorder is detected and localized to the first broken link | must have |
| REQ-040-04 | Truncation (middle-deletion and tail-truncation) is detected, or — where v0 provably cannot detect unanchored tail truncation — the report states that limit explicitly rather than falsely reporting OK | must have |
| REQ-040-05 | Malformed lines are reported (not panicked); `Verify` is a pure read-only function with no executor/LLM/web imports | must have |
| REQ-040-06 | `docs/spec/data-model.md` / `docs/spec/behaviors.md` documents the verification report shape and the detection boundary (which tamper classes v0 catches) in the same commit | must have |

## Readiness gate

- [x] Test spec `040-audit-verify-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 039 complete

## Acceptance criteria

- [ ] [REQ-040-01] An intact, empty, and single-record chain each verify `OK == true`; a single-record chain with a non-genesis `prev_hash` is reported broken at index 0
- [ ] [REQ-040-02] An in-place edit to record `k` is reported `OK == false` with the first broken link localized; editing the last record is caught as a self-hash mismatch
- [ ] [REQ-040-03] Swapping two adjacent records is reported `OK == false` at the first position whose `prev_hash` no longer matches its predecessor
- [ ] [REQ-040-04] Middle-deletion is reported broken at the join; tail-truncation is either reported broken (with a sealed-length anchor) or the unanchored-tail limit is explicitly documented and asserted
- [ ] [REQ-040-05] A malformed/non-JSON or hash-field-missing line yields a finding naming the index without a panic; the file is unchanged after `Verify`; `internal/audit` stays a leaf
- [ ] [REQ-040-06] The verification report shape and detection boundary are documented in spec in the feat commit

## Verification plan

- **Highest level achievable:** L5 — a harness writes a real chain, tampers the bytes, and asserts `Verify` localizes the first broken link for each tamper class.
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./internal/audit/... -run 'TestVerify'
  ```
  Expected final assertion: an intact chain reports `OK == true`; edit-in-place, reorder, and truncation fixtures each report `OK == false` with the first-broken-link index asserted.
- **Level 6 — Operator observation (if applicable):** optional — run a small verify entry point over a tampered file and observe the "first broken link at record N" line. Not required for ✅ given L5.
- **Cross-module state risk:** consumes the `prev_hash`/`hash`/genesis-sentinel contract produced by task 039 — the verifier must recompute canonical bytes identically to the writer. The task-039 golden fixture is the producer-consumer pin; a divergence here surfaces as a good chain falsely reported broken.
- **Runtime-visible surface:** the verification report (struct + any printed summary). The executor must run the harness and quote the OK/broken-link assertions.

## Out of scope

- Wiring `Verify` into the supervisor run or a CLI subcommand (task 041 wires the writer; a `verify` CLI/check entry point can follow). This task delivers the library function + adversarial tests.
- Defense against a privileged full-file rewrite (signing / external anchoring) — deferred per ADR 025 Consequences.
- The fitness isolation check (task 042).
- **Egress-attempt audit events** — deferred and spike-gated per ADR 025 decision 2.

## Notes

- Report the **first** broken link and stop escalating — a single edit must not produce N downstream "broken" findings that drown the real one.
- Build tamper fixtures programmatically from a known-good task-039 chain so they track the writer's format.
- Be explicit about the tail-truncation boundary: if v0 cannot detect pure tail truncation of an unanchored chain, the spec and the report must say so (ADR 025 says no one should over-trust v0).
- Update `docs/spec/` (data-model / behaviors) in the same commit. Do not edit spec during backlog authoring.
