# Test Spec 013: Escalation + retry-N + mandatory stop condition

**Linked task:** [`docs/tasks/completed/013-escalation-retry-policy.md`](../completed/013-escalation-retry-policy.md)
**Written:** 2026-06-04
**Status:** written

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-001A | ✅ |
| REQ-002 | TC-002, TC-002A, TC-003 | ✅ |
| REQ-003 | TC-004, TC-004A | ✅ |

## Acceptance criteria
- [REQ-001] A retry policy accepts a configurable non-negative attempt limit `N`.
- [REQ-001] `N=0` is valid and means escalate immediately without executor or gate attempts.
- [REQ-001] Negative retry counts are rejected at construction/configuration time.
- [REQ-002] When every attempt fails, exactly `N` executor attempts run; no `N+1` attempt is possible.
- [REQ-002] After failed attempts are exhausted, the policy writes `needs-human` through the status-writer seam and returns a terminal escalation/advance outcome.
- [REQ-002] A success before `N` stops retrying, does not mark `needs-human`, and returns a done/advance outcome carrying the successful branch.
- [REQ-002] Bounded failure across multiple tasks terminates cleanly instead of spinning.
- [REQ-003] The escalation hook is invoked after each failed attempt that still has a retry remaining, and the next attempt uses the executor returned by the hook.
- [REQ-003] Bootstrap may use a hook that returns the same executor, while tests can substitute a router-like hook that returns a different executor.

## Test cases
### TC-001: retry count N is configurable and honoured
- **Requirement:** REQ-001
- **Input:** table-driven policy configurations with `MaxAttempts` values `1` and `3`; always-failing Executor and a Gate that would fail if reached.
- **Expected output:** the executor observes exactly `N` calls for each row; the returned outcome is terminal escalation; the fake status writer records one `needs-human` write for the picked task.
- **Assertion markers:** each assertion message references `TC-001`.

### TC-001A: zero and invalid retry counts are explicit
- **Requirement:** REQ-001
- **Input:** one policy with `MaxAttempts=0`, and one policy with `MaxAttempts=-1`.
- **Expected output:** `N=0` performs no executor or gate attempt, invokes no between-attempt hook, writes `needs-human`, and returns a terminal escalation outcome; negative `N` is rejected before a policy can run.
- **Assertion markers:** each assertion message references `TC-001A`.

### TC-002: after N failures → escalate + advance
- **Requirement:** REQ-002
- **Input:** always-failing Executor, `MaxAttempts=3`, a Gate that would fail if reached, fake status writer, and recording escalation hook.
- **Expected output:** after exactly three attempts, the policy returns an escalated/needs-human outcome, records terminal/advance semantics, and writes the picked task ID with `needs-human` once.
- **Assertion markers:** each assertion message references `TC-002`.

### TC-002A: success before N does not escalate
- **Requirement:** REQ-002
- **Input:** first executor attempt fails, escalation hook supplies an executor that succeeds, Gate passes, and `MaxAttempts=3`.
- **Expected output:** exactly two executor attempts run, Gate runs once against the successful branch/worktree path, no status write occurs, and the returned outcome is done/advance with the successful branch.
- **Assertion markers:** each assertion message references `TC-002A`.

### TC-003: loop terminates — no infinite thrash
- **Requirement:** REQ-002
- **Input:** a finite task source with two ready tasks, `MaxAttempts=2`, always-failing Executor, and fake status writer. The harness repeatedly calls the policy runner until it returns idle.
- **Expected output:** total executor calls equal `task count * MaxAttempts`, both tasks are marked `needs-human`, the next call returns idle, and a tripwire fails the test if calls exceed the computed bound.
- **Assertion markers:** each assertion message references `TC-003`.

### TC-004: escalation hook is a substitutable seam
- **Requirement:** REQ-003
- **Input:** policy with a recording hook, `MaxAttempts=3`, and a bootstrap hook that returns the same executor after each failed attempt.
- **Expected output:** hook is invoked exactly twice, once after attempt 1 and once after attempt 2; it is not invoked after the terminal failure; each invocation receives the task, failed attempt number, and failed outcome.
- **Assertion markers:** each assertion message references `TC-004`.

### TC-004A: hook return value controls the next executor
- **Requirement:** REQ-003
- **Input:** first executor fails, hook returns a different executor that succeeds, and Gate passes.
- **Expected output:** the first executor is called once, the replacement executor is called once, the policy does not need to know whether the replacement came from a router or no-op bootstrap hook, and no status write occurs.
- **Assertion markers:** each assertion message references `TC-004A`.

## Notes
Framework: Go `testing` (table-driven). Fixtures: always-failing fake `Executor`, succeeding fake `Executor`, failing/passing fake `Gate`, recording fake escalation hook, fake status writer compatible with the task 011 status-writer seam. Termination is proven by computed attempt bounds and a tripwire counter, not wall-clock sleeps.
