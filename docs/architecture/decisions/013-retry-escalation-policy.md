# ADR 013: Retry escalation policy

**Date:** 2026-06-05
**Status:** Accepted

## Context

ADR 012 keeps the inside-the-box loop intentionally policy-free: it can report
that an executor or gate failed, but it does not decide whether to retry,
escalate, or stop. The autonomous-builder design requires a bounded retry-N
policy with a mandatory stop condition so unattended runs cannot thrash
forever. It also names escalation to a stronger executor as a future router
concern, while bootstrap still has only one executor.

Task 013 needs that policy layer without changing the single-cycle loop state
machine and without owning supervisor dispatch lifecycle work.

## Decision

Add a retry policy wrapper around one task cycle.

- `MaxAttempts` is a non-negative integer. Negative values are invalid.
- `MaxAttempts == 0` is valid and means the picked task is immediately marked
  `needs-human` without running an executor or gate.
- For `MaxAttempts > 0`, the policy attempts the picked task at most that many
  times. Executor error, executor incomplete, and gate fail outcomes are retryable
  failures until the bound is exhausted.
- After each failed non-terminal attempt, the policy invokes an escalation hook.
  The hook receives the task, the 1-based failed attempt number, the failed
  outcome, and the current executor. It returns the executor for the next
  attempt.
- Bootstrap uses a hook that returns the same executor. A future router can
  substitute a hook that returns a stronger executor without changing the policy.
- Once failures exhaust the bound, the policy writes `needs-human` through the
  constrained task status-writer seam and returns a terminal escalated outcome.
- A successful executor plus passing gate stops immediately, records advance
  semantics, and performs no status write.

## Consequences

- The loop's `OutcomeFail` remains free of retry counts and escalation targets.
- The mandatory stop condition is testable with fakes by asserting exact attempt
  counts and terminal status writes.
- The status writer remains the only persistent mutation path for escalation
  state.
- The router can be introduced later by replacing the hook implementation rather
  than reshaping the policy or loop contracts.
