# Test Spec 040: audit.Verify

**Linked task:** [`docs/tasks/backlog/040-audit-verify.md`](../backlog/040-audit-verify.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-040-01 | TC-040-01, TC-040-02 | ⏳ |
| REQ-040-02 | TC-040-03 | ⏳ |
| REQ-040-03 | TC-040-04 | ⏳ |
| REQ-040-04 | TC-040-05 | ⏳ |
| REQ-040-05 | TC-040-06, TC-040-07 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-040-01: an intact chain verifies OK

- **Requirement:** REQ-040-01
- **Input:** a chain file produced by `audit.ChainWriter` (task 039) over N events, untouched.
- **Expected output:** `audit.Verify(path)` returns a report with `OK == true`, no broken-link index, and a record count of N. The report names the first and last `hash` so an operator can compare against an external anchor later.
- **Edge cases:** an empty chain (zero records) verifies OK with count 0 — an empty audit log is valid, not corrupt.

### TC-040-02: a single-record chain verifies OK against the genesis sentinel

- **Requirement:** REQ-040-01
- **Input:** a one-event chain whose `prev_hash` is the genesis sentinel.
- **Expected output:** `OK == true`, count 1; the genesis linkage is accepted.
- **Edge cases:** a one-record chain whose `prev_hash` is NOT the genesis sentinel is reported broken at record 0 (a forged first link is caught).

### TC-040-03: edit-in-place is detected at the edited record

- **Requirement:** REQ-040-02
- **Input:** an intact N-record chain; one field in record `k` (0 < k < N) is modified in place without recomputing downstream hashes (a clean post-hoc edit).
- **Expected output:** `Verify` returns `OK == false` and identifies the **first** broken link. Because record `k`'s canonical bytes changed, record `k+1`'s stored `prev_hash` no longer matches the recomputed hash of `k` — the first broken link is at index `k+1` (the report states the index and the mismatch kind). The walk stops reporting at the first break, not every downstream record.
- **Edge cases:** editing the **last** record (no successor to break) is caught as a self-hash mismatch on that record itself, not missed.

### TC-040-04: reorder is detected

- **Requirement:** REQ-040-03
- **Input:** an intact chain with records `k` and `k+1` swapped.
- **Expected output:** `OK == false`; the first broken link is reported at the first position where the stored `prev_hash` no longer matches the actual predecessor's hash.
- **Edge cases:** swapping two records with coincidentally similar payloads still breaks the chain because the hashes differ.

### TC-040-05: truncation is detected

- **Requirement:** REQ-040-04
- **Input:** (a) a chain with its final M records removed (tail truncation); (b) a chain with a middle record deleted (line removed).
- **Expected output:** for (b), a middle deletion breaks the `prev_hash` linkage at the join and is reported `OK == false` at that index. For (a), tail truncation is reported per the documented policy: either `OK == false` with an explicit "chain truncated / shorter than sealed length" finding when a sealed length is recorded, or — if v0 cannot detect pure tail truncation of an unanchored chain — the report explicitly states the unanchored-tail-truncation limit rather than falsely reporting OK. The task's chosen policy must be documented and asserted, not left ambiguous.
- **Edge cases:** truncating to zero records must not be confused with a legitimately empty chain if a sealed-length anchor exists.

### TC-040-06: a malformed (non-JSON / missing hash field) line is reported, not panicked

- **Requirement:** REQ-040-05
- **Input:** a chain file where one line is not valid JSON, or is valid JSON missing the `hash`/`prev_hash` fields.
- **Expected output:** `Verify` returns `OK == false` with a finding naming the line index and the parse/field error; it does not panic and does not return a nil report.
- **Edge cases:** a trailing blank line is tolerated (skipped), consistent with NDJSON conventions; a blank line in the middle is reported.

### TC-040-07: Verify is a pure reader — no mutation, no executor/LLM/web dependency

- **Requirement:** REQ-040-05
- **Input:** `Verify` invoked over a fixture file; file contents and modification time inspected before and after.
- **Expected output:** the file is unchanged after `Verify` (read-only). `Verify` lives in `internal/audit` with no imports outside the standard library + task 038/039 types (no executor/LLM/web) — confirmed here, enforced by F-005 in task 042.
- **Edge cases:** a non-existent path returns a clear error, not a panic.

## Post-implementation verification

- [ ] All test cases above pass (including all three tamper classes: edit-in-place, reorder, truncation)
- [ ] No regressions in existing tests
- [ ] L5 harness over a written-then-tampered chain reports the first broken link

## Test framework notes

Framework: Go `testing` with adversarial fixtures. The tamper cases are the value of this task — generate an intact chain with the task-039 writer, then mutate the bytes (edit-in-place, swap two lines, drop a line, truncate the tail, corrupt a line) and assert `Verify` localizes the **first** break. Build the tamper fixtures programmatically from a known-good chain so they stay in sync with the writer's format rather than being hand-pasted hex.
