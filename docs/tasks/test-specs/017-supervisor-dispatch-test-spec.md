# Test Spec 017: Supervisor dispatch-one-task lifecycle

**Linked task:** [`docs/tasks/backlog/017-supervisor-dispatch.md`](../backlog/017-supervisor-dispatch.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002 | ❌ |
| REQ-002 | TC-003, TC-004 | ❌ |
| REQ-003 | TC-005 | ❌ |

## Test cases
### TC-001: happy-path single dispatch
- **Requirement:** REQ-001
- **Input:** one fixture task, fake box (records lifecycle calls), fake loop (returns success)
- **Expected output:** `Run()` returns nil; lifecycle calls recorded in order create → run-inside → teardown
- **Edge cases:** exactly one task dispatched per Run (no loop over many)

### TC-002: ordering is strict
- **Requirement:** REQ-001
- **Input:** fake box + fake loop with timestamped lifecycle hooks
- **Expected output:** box-created precedes loop-started precedes box-torn-down
- **Edge cases:** loop is never started before the box exists

### TC-003: teardown runs on loop error
- **Requirement:** REQ-002
- **Input:** fake loop returns an error
- **Expected output:** teardown still invoked exactly once; Run surfaces the error
- **Edge cases:** error is not swallowed

### TC-004: teardown runs on panic
- **Requirement:** REQ-002
- **Input:** fake loop panics
- **Expected output:** teardown still invoked (recover path); panic surfaced as error or re-panic per design
- **Edge cases:** teardown not double-invoked

### TC-005: no forbidden imports
- **Requirement:** REQ-003
- **Input:** supervisor package import set
- **Expected output:** `make fitness` F-003 check green — no executor/LLM/web imports
- **Edge cases:** N/A (fitness-level assertion)

## Notes
Framework: Go `testing`. Fake box implements the box interface and records `Create`/`Teardown` invocation order; fake loop implements the in-box-run interface and can be configured to succeed, error, or panic. Assert lifecycle ordering via a recorded call log. REQ-003 verified by the existing F-003 fitness check rather than a unit test.
