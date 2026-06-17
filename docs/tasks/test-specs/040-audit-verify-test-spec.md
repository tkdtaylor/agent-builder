# Test Spec 040: Wire `audit-trail verify` as the block-severity integrity gate

**Linked task:** [`docs/tasks/backlog/040-audit-verify.md`](../backlog/040-audit-verify.md)
**Written:** 2026-06-16
**Status:** ready

> Repurposed under ADR 026 (supersedes ADR 025): tamper detection is the **block's**
> (`audit-trail verify`, RFC 8785, edit/reorder/truncation). This spec covers the
> agent-builder-side `VerifyChain` helper that invokes the block's verifier and maps
> its verdict to a block-severity gate — **not** a reimplemented first-broken-link walker.

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-040-01 | TC-040-01 | ⏳ |
| REQ-040-02 | TC-040-02, TC-040-03 | ⏳ |
| REQ-040-03 | TC-040-04 | ⏳ |
| REQ-040-04 | TC-040-05 | ⏳ |
| REQ-040-05 | TC-040-06 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-040-01: an intact chain verifies valid

- **Requirement:** REQ-040-01
- **Input:** a chain produced by the `BlockSink` (task 039) over N events; `VerifyChain(binPath, logfile)` invoked.
- **Expected output:** a typed result `{ Valid: true, TamperedAt: nil, Message: … }` parsed from the block's `verify` response/exit 0; the record count/seq is carried through when the block provides it.
- **Edge cases:** an empty chain the block treats as valid is reported `Valid == true` (an empty audit log is valid, not corrupt).

### TC-040-02: a tampered chain maps to Valid == false with TamperedAt set

- **Requirement:** REQ-040-02
- **Input:** a `BlockSink`-produced chain with one byte edited on disk; `VerifyChain` invoked (the block's `verify` does the detection).
- **Expected output:** `{ Valid: false, TamperedAt: &seq }` parsed from the block's `tamper_detected_at` / exit 1; the helper preserves the seq the block localized.
- **Edge cases:** the detection itself (which tamper classes — edit-in-place, reorder, truncation) is the block's, asserted via fixtures the block produces; this task asserts the agent-builder side faithfully carries the verdict.

### TC-040-03: Valid == false is a block-severity gate failure

- **Requirement:** REQ-040-02
- **Input:** the `VerifyChain` result from TC-040-02 fed to the gate mapping.
- **Expected output:** a tampered chain produces a **block-severity** (non-zero / gate-fail) outcome that names the `tamper_detected_at` seq — consistent with ADR 026 decision 3 and F-002 (no skip path). A `Valid == true` result passes the gate.
- **Edge cases:** the gate must not down-rank a tamper to a warning; block severity is the contract.

### TC-040-04: missing binary or unreadable logfile is a hard error, distinct from Valid == false

- **Requirement:** REQ-040-03
- **Input:** `VerifyChain` with (a) a non-existent/non-executable block binary, (b) an unreadable/non-existent logfile.
- **Expected output:** a non-nil named error in both cases — explicitly distinct from a clean `Valid == false`. An unavailable verifier is never reported as "valid".
- **Edge cases:** the error message distinguishes "cannot verify" (infra) from "verified and tampered" (integrity) so an operator/gate does not conflate them.

### TC-040-05: VerifyChain reaches the block over os/exec only — internal/audit stays a leaf

- **Requirement:** REQ-040-04
- **Input:** `go list -deps ./internal/audit/...`.
- **Expected output:** no `audit-trail` block package, no executor/LLM/web package in the dependency list; `VerifyChain` uses `os/exec` (+ `encoding/json` to parse the response) only. (Enforced by F-005 in task 042.)
- **Edge cases:** `VerifyChain` does not read or mutate the logfile itself — the block reads it; the helper only invokes the block and parses the verdict.

### TC-040-06 (L5): real-block round trip — intact valid, tampered invalid

- **Requirement:** REQ-040-05 (integration)
- **Input:** produce a real chain via `BlockSink`; run `VerifyChain` (asserts `Valid == true`); tamper a byte on disk; run `VerifyChain` again.
- **Expected output:** intact ⇒ `Valid == true`; tampered ⇒ `Valid == false` with `TamperedAt` populated by the block. (CI-without-binary fallback: a recorded-exec stub returning the block's documented valid/tampered JSON + exit codes; the real-binary path is opt-in and the L5 evidence states which ran.)
- **Edge cases:** the tamper is applied to the on-disk file the block reads, exercising the block's real detection — not a stubbed verdict, when the real-binary path runs.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in existing tests
- [ ] L5 harness (produce → verify valid → tamper → verify invalid via the block) passes, or records the recorded-exec fallback + the opt-in real-binary command

## Test framework notes

Framework: Go `testing` with an injectable exec seam for unit tests and an opt-in real-`audit-trail` path for L5. The adversarial tamper detection (first-broken-link, edit/reorder/truncation) is the **block's**, already tested in the audit-trail repo — this task asserts the agent-builder side invokes it, maps `valid == false` (+ `tamper_detected_at`) to a block-severity gate, and fails loud (not "valid") when the verifier is unavailable. Defense against a privileged full-file rewrite is the block's signed-checkpoint / Rekor-anchor surface, deferred per ADR 026.
