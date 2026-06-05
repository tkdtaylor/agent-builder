# Test Spec 012: Agent loop state machine (pick → attempt → verify → next)

**Linked task:** [`docs/tasks/backlog/012-agent-loop.md`](../backlog/012-agent-loop.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003 | ❌ |

## Test cases
### TC-001: state transitions pick → attempt → verify → advance
- **Requirement:** REQ-001
- **Input:** fake task source yielding one ready task; fake Executor + fake Gate
- **Expected output:** observed transition sequence matches pick → attempt → verify → advance
- **Edge cases:** no ready task (loop idles/terminates); task source exhausted

### TC-002: Gate pass → done outcome with branch
- **Requirement:** REQ-002
- **Input:** fake Executor returns `Result{Branch:"task/...", OK:true}`; fake Gate returns true
- **Expected output:** done outcome carrying the branch
- **Edge cases:** Executor returns OK but Gate disagrees → fail path (see TC-003)

### TC-003: Gate fail → fail outcome, no retry decision in loop
- **Requirement:** REQ-003
- **Input:** fake Gate returns false
- **Expected output:** fail outcome emitted; loop makes no retry-count decision; cycle suspended for policy
- **Edge cases:** Executor returns error vs Gate returns false (both surface as fail outcome distinctly)

## Notes
Framework: Go `testing` (table-driven). Fixtures: fake `Executor` (scripted `Result`/error) + fake `Gate` (scripted bool/error) + fake task source. Assert on the loop's outcome/state type. No real containment, executor, or gate.
