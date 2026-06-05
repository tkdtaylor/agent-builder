# Test Spec 019: Run log collection (audit-trail seam)

**Linked task:** [`docs/tasks/backlog/019-run-log-collection.md`](../backlog/019-run-log-collection.md)
**Written:** 2026-06-04
**Status:** complete — ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001-RunRecord-Wire-Format, TC-005-RunRecord-Outcome-Values | ✅ |
| REQ-002 | TC-002-Stream-Capture | ✅ |
| REQ-003 | TC-003-Persist-After-Teardown, TC-004-No-Post-Teardown-Readback | ✅ |

## Test cases
### TC-001-RunRecord-Wire-Format: RunRecord serializes as NDJSON
- **Requirement:** REQ-001
- **Input:** a completed supervisor run with a configured run-record file path
- **Expected output:** the file is plain UTF-8 NDJSON; every non-empty line is independently parseable JSON and contains a stable `version`, `type`, `run_id`, `timestamp`, and event-specific fields
- **Assertions:** tests parse every line with `encoding/json`, reject array-wrapped or multi-line JSON records, and verify the run starts with a metadata line and ends with a terminal outcome line
- **Edge cases:** an empty-output successful run still produces valid `run_started`, `command`, and `run_finished` records

### TC-002-Stream-Capture: stdout/stderr plus command log captured while run is active
- **Requirement:** REQ-002
- **Input:** fake in-box loop streams known stdout, stderr, and command lines to the supervisor during `RunInside`
- **Expected output:** the run-record file contains stdout, stderr, and command events with exact payload bytes written by the fake loop
- **Assertions:** tests assert at least one `stdout`, one `stderr`, and one `command` event exist, with exact payloads and no stream swaps
- **Edge cases:** interleaved stdout/stderr writes preserve writer order within each stream and are not emitted only after `RunInside` returns

### TC-003-Persist-After-Teardown: record survives containment teardown
- **Requirement:** REQ-003
- **Input:** fixture run followed by box teardown
- **Expected output:** the run-record file exists, is closed, and remains readable after teardown has completed
- **Assertions:** tests read the file after the fake box has marked itself torn down and verify the previously streamed stdout/stderr/command records are present
- **Edge cases:** file is flushed before teardown completes so the record is not trapped in process buffers

### TC-004-No-Post-Teardown-Readback: streamed during run, not copied back from the box
- **Requirement:** REQ-003
- **Input:** fake box that makes post-teardown reads impossible and a fake loop that errors after streaming partial output
- **Expected output:** the run-record still contains the partial stdout/stderr/command records because they were streamed out before teardown
- **Assertions:** tests assert no fake box readback hook is called after teardown and the terminal event records a failed outcome
- **Edge cases:** loop error still yields a flushed partial record plus a terminal outcome event

### TC-005-RunRecord-Outcome-Values: outcome field reserves task 018 timeout state
- **Requirement:** REQ-001
- **Input:** supervisor success and loop-error runs; data-model documentation and Go constants for run-record outcomes
- **Expected output:** terminal records use stable outcome strings and reserve `timed-out` for task 018 without implementing timeout behavior here
- **Assertions:** tests assert success records `completed`, loop errors record `failed`, and the exported outcome vocabulary includes `timed-out`
- **Edge cases:** task 019 does not introduce wall-clock timers, cancellation, or box kill semantics

## Notes
Framework: Go `testing`. Use a fake loop that writes to supervisor-provided stdout/stderr/command writers during `RunInside`, then assert the temp-dir run-record file after simulated teardown. Coordinate the outcome field with task 018's future `timed-out` state, but do not implement timeout behavior in this task.
