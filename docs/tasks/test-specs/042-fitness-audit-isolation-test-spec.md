# Test Spec 042: fitness-audit-isolation (F-005)

**Linked task:** [`docs/tasks/backlog/042-fitness-audit-isolation.md`](../backlog/042-fitness-audit-isolation.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-042-01 | TC-042-01 | ⏳ |
| REQ-042-02 | TC-042-02 | ⏳ |
| REQ-042-03 | TC-042-03 | ⏳ |
| REQ-042-04 | TC-042-04 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-042-01: fitness-audit-isolation passes on the clean tree (internal/audit is a leaf)

- **Requirement:** REQ-042-01
- **Input:** `make fitness-audit-isolation` on the current tree after task 041 lands.
- **Expected output:** the target exits zero and prints a PASS line, e.g. `PASS fitness-audit-isolation: internal/audit import graph contains no executor/LLM/web-fetch or audit-trail-block packages and the supervisor's audit dependency drags none in.`
- **Edge cases:** the check covers `go list -deps ./internal/audit/...` (the leaf itself) and asserts no path segment named `executor`/`executors`/`llm`/`llms`/`web`/`webfetch`/`web-fetch`, mirroring the `fitness-supervisor-isolation` token set, plus an `audit-trail` block-package token (the block must be reached over `os/exec`, never imported as a Go module — ADR 026 Option A over C).

### TC-042-02: the check also guards the supervisor's transitive graph post-wiring

- **Requirement:** REQ-042-02
- **Input:** `make fitness-audit-isolation` inspecting `go list -deps ./internal/supervisor/...` as well.
- **Expected output:** the target asserts that wiring `internal/audit` into the supervisor (task 041) did NOT drag an executor/LLM/web package into the supervisor's transitive import graph — i.e. the audit dependency stayed a leaf through the supervisor seam. PASS prints both the audit-leaf and supervisor-graph confirmations.
- **Edge cases:** this overlaps `fitness-supervisor-isolation` deliberately — F-003 guards the supervisor; F-005 guards `internal/audit` itself plus the audit-via-supervisor path, so a regression that only `internal/audit` introduces is caught even if F-003's exact token logic changes.

### TC-042-03: a forbidden import in internal/audit makes the check fail (negative)

- **Requirement:** REQ-042-03
- **Input:** a temporary/fixture build of `internal/audit` (or a test harness) where an executor/LLM/web package is imported into the audit graph.
- **Expected output:** `make fitness-audit-isolation` exits non-zero and prints a FAIL line naming the forbidden package path; the violation is the import chain into the offending package.
- **Edge cases:** the negative is demonstrated without permanently adding a bad import to the tree — e.g. a scripted check against a synthetic import list, or a documented manual demonstration recorded in the verify evidence (consistent with how F-001/F-002/F-003 negatives were demonstrated).

### TC-042-04: fitness-audit-isolation is wired into the umbrella fitness target and the spec

- **Requirement:** REQ-042-04
- **Input:** the `fitness` umbrella target's prerequisites and `docs/spec/fitness-functions.md`.
- **Expected output:** `fitness-audit-isolation` is a prerequisite of `make fitness` (so it runs in `make check`); `docs/spec/fitness-functions.md` has an F-005 row (rule, asserts, threshold `0 violations`, check command `make fitness-audit-isolation`, severity `block`, a one-line *why*) and a source-of-truth link to ADR 026. `make fitness` prints the F-005 PASS line among the others.
- **Edge cases:** the `.PHONY` list and the `fitness:` prerequisite list both include the new target so it is discoverable and runnable standalone.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in the existing fitness targets (F-001..F-004 still pass)
- [ ] L6 operator-observed PASS line recorded

## Test framework notes

Framework: shell + `go list` in the Makefile, mirroring `fitness-supervisor-isolation` (line ~111) and `fitness-no-srt` (line ~123). Reuse the same `awk`/`grep` token set for executor/LLM/web path segments. The negative case (TC-042-03) follows the established pattern of demonstrating the FAIL path against a synthetic import list rather than committing a bad import.
