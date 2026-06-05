# Test Spec 019: Run log collection (audit-trail seam)

**Linked task:** [`docs/tasks/backlog/019-run-log-collection.md`](../backlog/019-run-log-collection.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003, TC-004 | ❌ |

## Test cases
### TC-001: RunRecord wire format
- **Requirement:** REQ-001
- **Input:** a completed run
- **Expected output:** record serializes to plain-text / NDJSON; each line is independently parseable
- **Edge cases:** empty output run still produces a valid record

### TC-002: stdout/stderr + command log captured
- **Requirement:** REQ-002
- **Input:** fake box emits known stdout, stderr, and command lines during the run
- **Expected output:** all three streams present in the captured record
- **Edge cases:** interleaved stdout/stderr ordering is preserved per stream

### TC-003: record persists after teardown
- **Requirement:** REQ-003
- **Input:** fixture run followed by box teardown
- **Expected output:** run-record file exists and is readable after teardown
- **Edge cases:** file is flushed/closed before teardown completes (streamed, not buffered)

### TC-004: streamed during run, not read back from box
- **Requirement:** REQ-003
- **Input:** fake box that is destroyed at teardown (no post-teardown read possible)
- **Expected output:** record still complete because output was streamed out during the run
- **Edge cases:** partial run (loop errors mid-way) still yields the partial record

## Notes
Framework: Go `testing`. Fake box exposes stdout/stderr pipes the supervisor streams to a temp-dir run-record file; assert file contents after a simulated teardown. Coordinate the outcome field with 018's timed-out state. Use `t.TempDir()` for the durable record location.
