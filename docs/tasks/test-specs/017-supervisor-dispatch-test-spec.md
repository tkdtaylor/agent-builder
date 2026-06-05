# Test Spec 017: Supervisor dispatch-one-task lifecycle

**Linked task:** [`docs/tasks/completed/017-supervisor-dispatch.md`](../completed/017-supervisor-dispatch.md)
**Written:** 2026-06-04
**Status:** complete

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002 | ✅ |
| REQ-002 | TC-003, TC-004 | ✅ |
| REQ-003 | TC-005 | ✅ |

## Test cases
### TC-001: happy-path single dispatch
- **Requirement:** REQ-001
- **Input:** one fixture task in the supervisor options, fake box (records lifecycle calls), fake loop (returns success)
- **Expected output:** `Run()` returns nil; fake box and fake loop each record exactly one call; lifecycle calls are `box.create`, `loop.run`, `box.teardown`
- **Edge cases:** one `Run()` dispatches exactly one task and never loops over additional fake-loop responses

### TC-002: ordering is strict
- **Requirement:** REQ-001
- **Input:** fake box + fake loop sharing a call log
- **Expected output:** the loop observes the created box identifier; log entries prove `box.create` precedes `loop.run`, which precedes `box.teardown`
- **Edge cases:** the loop is never started before the box exists; teardown is never attempted before the loop starts

### TC-003: teardown runs on loop error
- **Requirement:** REQ-002
- **Input:** fake loop returns an error
- **Expected output:** teardown still invokes exactly once after the loop call; `Run()` surfaces an error wrapping the fake loop error
- **Edge cases:** loop error is not swallowed and teardown is not double-invoked

### TC-004: teardown runs on panic
- **Requirement:** REQ-002
- **Input:** fake loop panics
- **Expected output:** teardown still invokes exactly once; `Run()` recovers and returns an error that includes the panic value
- **Edge cases:** teardown is not double-invoked on the recover path

### TC-005: no forbidden imports
- **Requirement:** REQ-003
- **Input:** supervisor package import set
- **Expected output:** `make fitness-supervisor-isolation` is green — no executor/LLM/web imports in the supervisor package import graph
- **Edge cases:** the supervisor may consume injected box and loop interfaces, but must not import executor, LLM, or web-fetch packages

## Notes
Framework: Go `testing`. Fake box implements the box interface and records `Create`/`Teardown` invocation order; fake loop implements the in-box-run interface and can be configured to succeed, error, or panic. Assert lifecycle ordering via a recorded call log whose entries include the TC markers above. REQ-003 is verified by the existing F-003 fitness check rather than a duplicate unit test.
