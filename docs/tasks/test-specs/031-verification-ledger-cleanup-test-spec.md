# Test Spec 031: verification ledger cleanup

**Linked task:** [`docs/tasks/backlog/031-verification-ledger-cleanup.md`](../backlog/031-verification-ledger-cleanup.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004 | ✅ |

## Test cases
### TC-001: remaining yellow tasks receive assertion-by-assertion verdicts
- **Requirement:** REQ-001
- **Input:** task files and test specs for tasks 001, 002, 026, and 027.
- **Expected output:** each task has a recorded spec-verifier APPROVE or a named missing assertion/blocker in `coverage-tracker.md`.
- **Edge cases:** a passing test command without assertion evidence remains 🟡.

### TC-002: runtime-gated tasks are not overpromoted
- **Requirement:** REQ-002
- **Input:** task 030 evidence and tracker rows for 014, 015, 016, and 021.
- **Expected output:** rows with missing runtime evidence remain 🟡 and name the unavailable probe; rows with observed L6 evidence may be promoted.
- **Edge cases:** static parser or unit evidence alone is insufficient for runtime security claims.

### TC-003: spec files describe current state
- **Requirement:** REQ-003
- **Input:** `docs/spec/SPEC.md` and relevant sub-spec files.
- **Expected output:** spec text no longer labels the whole project "pre-implementation" if components are implemented; any remaining target/future work is moved to tasks or roadmap language.
- **Edge cases:** ADR history is not rewritten; only current-state spec text changes.

### TC-004: task ledger consistency check passes
- **Requirement:** REQ-004
- **Input:** all task files under `docs/tasks/{backlog,active,completed}` and `coverage-tracker.md`.
- **Expected output:** every task has one file, one paired test spec, one tracker row, and compatible status/location/tracker state.
- **Edge cases:** backlog tasks may be ❌; completed tasks may be 🟡 if code is merged but verification is pending.

## Notes
Framework: shell or Go doc-consistency harness plus normal `make check`. The spec-verifier step is read-only except for recording its verdict after review.
