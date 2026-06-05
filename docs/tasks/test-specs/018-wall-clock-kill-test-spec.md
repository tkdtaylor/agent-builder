# Test Spec 018: Wall-clock timeout / runaway kill

**Linked task:** [`docs/tasks/backlog/018-wall-clock-kill.md`](../backlog/018-wall-clock-kill.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003, TC-004 | ❌ |

## Test cases
### TC-001: timeout is configurable
- **Requirement:** REQ-001
- **Input:** supervisor configured with a short timeout (e.g. 50ms)
- **Expected output:** the configured value drives the deadline; no hard-coded constant
- **Edge cases:** zero / unset timeout handling (disabled or default — per design)

### TC-002: runaway run is killed and box torn down
- **Requirement:** REQ-002
- **Input:** fake loop blocks/sleeps past the configured timeout
- **Expected output:** box kill invoked; teardown runs (017 guarantee preserved)
- **Edge cases:** teardown not double-invoked; kill is idempotent

### TC-003: outcome marked timed-out
- **Requirement:** REQ-003
- **Input:** run that exceeds the timeout
- **Expected output:** run outcome == timed-out
- **Edge cases:** N/A

### TC-004: timed-out distinct from gate-fail and success
- **Requirement:** REQ-003
- **Input:** three runs — success, gate-fail (loop error), timeout
- **Expected output:** three distinct outcome values
- **Edge cases:** a fast-failing loop is NOT reported as timed-out

## Notes
Framework: Go `testing`. Fake loop honours context cancellation or sleeps a controllable duration; use a short configured timeout to keep tests fast. Assert box kill + teardown ordering and the distinct timed-out outcome. Coordinate the outcome value with 019's `RunRecord`.
