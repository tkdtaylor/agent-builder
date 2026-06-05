# Test Spec 012: Agent loop state machine (pick → attempt → verify → next)

**Linked task:** [`docs/tasks/completed/012-agent-loop.md`](../completed/012-agent-loop.md)
**Written:** 2026-06-04
**Status:** written — ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-004 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003, TC-005 | ✅ |

## Test cases
### TC-001: state transitions pick → attempt → verify → advance
- **Requirement:** REQ-001
- **Input:** fake task source yielding one ready task; fake Executor + fake Gate
- **Expected output:** observed transition sequence is exactly `pick`, `attempt`, `verify`, `advance`; Executor receives the picked task; Gate receives the configured worktree path
- **Assertions:** the test must assert the full transition sequence, the task ID passed to the Executor, the worktree path passed to the Gate, and that the outcome task ID matches the picked task
- **Edge cases:** no ready task is covered by TC-004

### TC-002: Gate pass → done outcome with branch
- **Requirement:** REQ-002
- **Input:** fake Executor returns `Result{Branch:"task/...", OK:true}`; fake Gate returns true
- **Expected output:** `OutcomeDone` carrying the exact branch returned by the Executor
- **Assertions:** outcome kind is done; outcome branch equals the fake Executor branch; outcome verdict is passing; outcome carries no retry count or policy decision
- **Edge cases:** Executor returns OK but Gate disagrees is covered by TC-003

### TC-003: Gate fail → fail outcome, no retry decision in loop
- **Requirement:** REQ-003
- **Input:** fake Gate returns false
- **Expected output:** `OutcomeFail` emitted after `pick`, `attempt`, `verify`; the loop does not append `advance` and does not set a retry count or retry decision
- **Assertions:** outcome kind is fail; failure reason is gate failure; verdict is failing; branch is still the attempted branch for diagnostics; retry decision field is absent from the outcome type or impossible to set
- **Edge cases:** Executor returns error is covered by TC-005

### TC-004: no ready task → idle outcome without attempt
- **Requirement:** REQ-001
- **Input:** fake task source returning no task and no error
- **Expected output:** `OutcomeIdle` with transition sequence `pick` only
- **Assertions:** Executor and Gate call counts remain zero; no branch is recorded
- **Edge cases:** task source error should return an error before Executor or Gate is called

### TC-005: Executor error → fail outcome before verify
- **Requirement:** REQ-003
- **Input:** fake task source yielding one task; fake Executor returning an error
- **Expected output:** `OutcomeFail` emitted after `pick`, `attempt`; Gate is not called; loop makes no retry-count decision
- **Assertions:** outcome kind is fail; failure reason is executor error; error text is preserved for the escalation policy consumer; no branch or retry decision is recorded

## Notes
Framework: Go `testing` (table-driven). Fixtures: fake `Executor` (scripted `Result`/error) + fake `Gate` (scripted bool/error) + fake task source. Assert on the loop's outcome/state type. No real containment, executor, or gate.
