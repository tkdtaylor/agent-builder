# Test Spec 013: Escalation + retry-N + mandatory stop condition

**Linked task:** [`docs/tasks/backlog/013-escalation-retry-policy.md`](../backlog/013-escalation-retry-policy.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |

## Test cases
### TC-001: retry count N is configurable and honoured
- **Requirement:** REQ-001
- **Input:** policy configured with N (e.g. 1, 3); always-failing Executor + failing Gate
- **Expected output:** exactly N attempts observed for each configured N
- **Edge cases:** N=0 (escalate immediately, no attempt); N negative/invalid → rejected

### TC-002: after N failures → escalate + advance
- **Requirement:** REQ-002
- **Input:** always-failing Executor, N=3
- **Expected output:** after 3 attempts the task is marked escalated/needs-human and the loop advances
- **Edge cases:** failure on the Nth attempt only; success before N (should not escalate)

### TC-003: loop terminates — no infinite thrash
- **Requirement:** REQ-002
- **Input:** always-failing Executor across the task source
- **Expected output:** loop terminates with bounded total attempts; assertion proves no infinite loop (e.g. capped iteration counter / timeout tripwire)
- **Edge cases:** every task fails → all escalated, loop exits cleanly

### TC-004: escalation hook is a substitutable seam
- **Requirement:** REQ-003
- **Input:** policy with a fake escalation hook (records invocations); single-executor bootstrap config
- **Expected output:** hook invoked between attempts as designed; swapping the hook impl changes behaviour without touching the policy
- **Edge cases:** hook returns same executor (bootstrap no-op) vs a different one (router stand-in)

## Notes
Framework: Go `testing` (table-driven). Fixtures: always-failing fake `Executor`, failing fake `Gate`, recording fake escalation hook, fake status writer (011). Termination proven via bounded counter / `context` timeout, not wall-clock.
