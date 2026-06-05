# Test Spec 018: Wall-clock timeout / runaway kill

**Linked task:** [`docs/tasks/backlog/018-wall-clock-kill.md`](../backlog/018-wall-clock-kill.md)
**Written:** 2026-06-04
**Status:** complete — ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001-Configurable-Timeout, TC-005-Unset-Timeout-No-Kill | ✅ |
| REQ-002 | TC-002-Timeout-Kills-Box-And-Tears-Down, TC-006-Kill-Error-Still-Tears-Down | ✅ |
| REQ-003 | TC-003-RunRecord-Timed-Out, TC-004-Outcome-Distinct | ✅ |

## Test cases
### TC-001-Configurable-Timeout: timeout is configurable
- **Requirement:** REQ-001
- **Input:** supervisor configured with a short timeout (for example 50ms), a fake in-box loop that blocks until released, and a fake containment box that records lifecycle calls.
- **Expected output:** the configured value drives the deadline: the run returns a timeout error after the short configured duration, not after an internal constant.
- **Assertions:** the test measures the elapsed run duration with a loose upper bound, asserts `errors.Is(err, supervisor.ErrRunTimedOut)`, and asserts the fake box observed a kill call for the created handle.
- **Edge cases:** the test must avoid brittle exact timing by using a short deadline and a generous maximum bound.

### TC-002-Timeout-Kills-Box-And-Tears-Down: runaway run is killed and box torn down
- **Requirement:** REQ-002
- **Input:** fake loop blocks past the configured timeout; fake box records `Create`, `Kill`, and `Teardown` calls.
- **Expected output:** timeout invokes `Kill(handle)` and then deterministic `Teardown(handle)` exactly once.
- **Assertions:** call order is `box.create`, `loop.run`, `box.kill`, `box.teardown`; `Kill` and `Teardown` receive the same handle produced by `Create`; teardown count remains exactly one.
- **Edge cases:** teardown still runs if the killed loop unblocks and returns after the kill signal.

### TC-003-RunRecord-Timed-Out: run record marks timed-out
- **Requirement:** REQ-003
- **Input:** supervisor configured with a run-record path and a fake loop that blocks past the timeout.
- **Expected output:** terminal `run_finished` event records `"outcome":"timed-out"` and includes an error string naming the timeout.
- **Assertions:** parse the NDJSON run record, assert the final event is `run_finished`, assert outcome equals `supervisor.RunOutcomeTimedOut`, and assert the timeout record is flushed before teardown.
- **Edge cases:** partial stdout/stderr/command events written before the timeout remain present in the run record.

### TC-004-Outcome-Distinct: timed-out stays distinct from success and loop failure
- **Requirement:** REQ-003
- **Input:** three runs — success, gate-fail (loop error), timeout
- **Expected output:** successful runs record `completed`, loop errors record `failed`, and deadline expiry records `timed-out`.
- **Assertions:** tests compare the exact outcome strings from run-record terminal events, and assert a fast loop error is not wrapped as `ErrRunTimedOut`.
- **Edge cases:** if a loop returns an ordinary error before the timeout, no kill is invoked and teardown still runs once.

### TC-005-Unset-Timeout-No-Kill: unset timeout preserves existing behavior
- **Requirement:** REQ-001
- **Input:** supervisor created without a timeout option, fake loop returns success immediately.
- **Expected output:** the run follows the task 017 lifecycle with no kill and a `completed` outcome.
- **Assertions:** fake box kill calls remain zero; lifecycle order remains `box.create`, `loop.run`, `box.teardown`; run record, when configured, finishes as `completed`.
- **Edge cases:** a nil/zero timeout option must not create a zero-duration immediate timeout.

### TC-006-Kill-Error-Still-Tears-Down: kill failure is surfaced without skipping teardown
- **Requirement:** REQ-002
- **Input:** fake loop blocks past the configured timeout and fake box returns an error from `Kill`.
- **Expected output:** `Run()` returns an error joining `ErrRunTimedOut` and the kill error; teardown still runs exactly once.
- **Assertions:** `errors.Is(err, supervisor.ErrRunTimedOut)` and `errors.Is(err, killErr)` are both true; teardown receives the created handle after the kill attempt.
- **Edge cases:** kill errors do not change the run-record outcome away from `timed-out`.

## Notes
Framework: Go `testing`. Fake loop blocks on channels so timeout tests are deterministic and fast. The existing `RunRecord` outcome vocabulary from task 019 is the source of truth for the `timed-out` string; this task only adds the timeout producer. Supervisor import isolation remains covered by F-003 and `tests/supervisor/imports_test.go`.
