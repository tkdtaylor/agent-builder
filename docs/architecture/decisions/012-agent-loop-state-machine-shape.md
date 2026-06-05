# ADR 012: Agent loop state machine shape

**Date:** 2026-06-05
**Status:** Accepted

## Context

The agent loop is the inside-the-box cycle that consumes the roadmap task
source, asks an Executor to attempt one task, and runs the verification gate
against the target worktree. The loop must expose enough state for tests,
future logging, and future escalation policy code without absorbing those
policies itself.

Task 012 needs only the happy-path state machine plus a policy-free fail
outcome. Retry counts, mandatory stop conditions, containment dispatch, and real
executor wiring are later tasks.

## Decision

The loop is a single-cycle state machine with explicit states:

1. `pick` asks the task source for the next ready task.
2. `attempt` runs the Executor for that task.
3. `verify` runs the Gate against the configured worktree path.
4. `advance` is recorded only after the Gate passes.

The public result of one cycle is an `Outcome`. It carries the task, the
observed transition trace, and exactly one kind:

- `idle` when no task is ready.
- `done` when Executor returns a branch and the Gate passes. The branch is
  preserved in the outcome.
- `fail` when the Executor errors, the Executor reports an unsuccessful attempt,
  or the Gate fails. The outcome records the failure reason, optional branch
  diagnostics, and the Gate verdict when one exists.

The fail outcome intentionally has no retry count, retry decision, or escalation
target. The loop reports what happened; the escalation policy consumes that
outcome and decides what to do next.

## Consequences

- Tests can assert the state sequence without relying on logs or side effects.
- The Gate remains the machine-checkable definition of done; Executor self-
  assessment alone never completes a task.
- Policy code can distinguish executor errors from gate failures without the
  loop making a retry decision.
- Later dispatch and containment work can wrap this cycle without changing the
  state or outcome contract.
