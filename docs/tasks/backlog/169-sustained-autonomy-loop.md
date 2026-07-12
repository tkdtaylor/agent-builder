# Task 169: bounded re-plan loop with RunStore-persisted attempt budget and escalation

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add `Orchestrator.RunToCompletion(ctx, goal, maxAttempts int) (PlanResult,
error)`: a bounded goal-level loop that folds a terminal sub-goal failure back
into the goal text and re-plans, up to a RunStore-persisted attempt budget, and
escalates over the `Reporter` seam on exhaustion.

## Context

ADR 065 names this task explicitly as part of the arc built on the run journal.
Today the orchestrator dispatches a plan once and reports whatever happened,
`Handle → dispatchPlan → report → stop`; nothing retries or re-plans at the goal
level when a sub-goal is terminally gate-exhausted. This closes the "sustained
autonomy" gap `docs/plans/roadmap.md`'s Forward arc item 5 (heartbeat/daemon) and
`AGENTS.md`'s "Known missing" list both point at.

**Reference:**
- `docs/architecture/decisions/065-durable-execution-thin-run-journal-temporal-rejected.md`
- `internal/orchestrator/orchestrator.go:752-767` (`FoldGoalText`, the exact
  helper task 115 built for folding queued info into a goal, reused unmodified
  here for folding failure detail instead)
- `internal/loop/retry_policy.go:205-219` (`markNeedsHuman`, the sub-goal-level
  terminal-failure shape this task's fold logic reads from)
- `internal/loop/format_failure.go` (`FormatFailure`, the existing
  failure-to-text convention this task's fold text should reuse/mirror rather
  than inventing a new format)
- Task 167/168 (`runstore`, `WithRunStore`, per-goal `Record` persistence, the
  mechanism this task's attempt counter rides on)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-169-01 | `AGENT_BUILDER_GOAL_MAX_ATTEMPTS` (default `3`) read by `orchestrate` CLI assembly. | must have |
| REQ-169-02 | Immediate success needs no re-plan (single `Handle` call). | must have |
| REQ-169-03 | A terminal failure folds detail into the goal text via `FoldGoalText` and re-plans. | must have |
| REQ-169-04 | Attempt counter persists via `runstore.Record`, surviving a crash mid-loop. | must have |
| REQ-169-05 | Exhaustion escalates once over `Reporter`, naming the goal and attempt count. | must have |
| REQ-169-06 | RunStore unset: loop mechanics still function (no cross-process durability). | must have |
| REQ-169-07 | Pre-existing orchestrator suites unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/169-sustained-autonomy-loop-test-spec.md` exists (written first)
- [x] Task 167/168 merged
- [x] Task 115 merged (`FoldGoalText`)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/cli/orchestrate.go`'s `assembleOrchestrate`: read
   `AGENT_BUILDER_GOAL_MAX_ATTEMPTS` (int, default `3`; a non-integer value is
   `errUsageConfig`, mirroring existing malformed-env-value handling elsewhere in
   this file), thread it into whatever `orchestrateConfig` field the goal actor
   passes to `RunToCompletion` (or its caller).
2. `internal/orchestrator/orchestrator.go`, new method:
   ```go
   var ErrGoalAttemptsExhausted = errors.New("orchestrator: goal attempts exhausted")

   func (o *Orchestrator) RunToCompletion(ctx context.Context, goal supervisor.Task, maxAttempts int) (PlanResult, error) {
       attempt := o.loadAttemptCount(goal.ID) // 0 if no RunStore or no prior record
       currentGoal := goal
       var last PlanResult
       for attempt < maxAttempts {
           attempt++
           o.saveAttemptCount(goal.ID, attempt) // best-effort persist, before dispatch
           result, err := o.Handle(ctx, currentGoal)
           last = result
           if err != nil {
               return result, err // a hard error (not a plan-level failure) is not retried by this loop
           }
           if !result.HasTerminalFailure() { // new helper, see step 3
               return result, nil
           }
           currentGoal.Spec = FoldGoalText(goal.Spec, result.FailureDetails()) // new helper, see step 3
       }
       o.escalateExhausted(goal.ID, attempt) // Reporter.Report, once
       return last, fmt.Errorf("%w: goal %q after %d attempts", ErrGoalAttemptsExhausted, goal.ID, attempt)
   }
   ```
3. Add `PlanResult.HasTerminalFailure() bool` and `PlanResult.FailureDetails()
   []string` helper methods (or equivalent, executor's naming choice) that
   inspect the existing per-sub-goal outcome shape `dispatchPlan` already
   populates, reusing `internal/loop.FormatFailure`'s text convention for each
   detail string so the folded goal text reads consistently with the sub-goal
   level's own failure formatting.
4. Attempt-count persistence: reuse the `runstore.Record` written by task 168's
   `ConfirmAndPlan`/`dispatchOne` wiring, add an `Attempt int` field to
   `runstore.Record` if task 167/168 did not already carry one suitable for this
   purpose (if it did, reuse it, do not add a redundant field), read/write it via
   `o.runStore.Load`/`Save` guarded by `o.runStore != nil` exactly like task
   168's other optional writes.
5. `escalateExhausted`: call `o.reporter.Report(...)` (the existing
   `supervisor.Reporter` seam `Orchestrator` already holds) once, with a message
   containing the goal ID, the attempt count, and the literal word
   `"exhausted"`.
6. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-169-01] TC-169-01: env var default/override/invalid-value handling.
- [ ] [REQ-169-02] TC-169-02: immediate success, one `Handle` call.
- [ ] [REQ-169-03] TC-169-03: failure folds detail and re-plans.
- [ ] [REQ-169-04] TC-169-04: attempt counter survives a crash (L5).
- [ ] [REQ-169-05] TC-169-05: exhaustion escalates exactly once.
- [ ] [REQ-169-06] TC-169-06: RunStore unset, mechanics unaffected.
- [ ] [REQ-169-07] TC-169-07: `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, TC-169-04's two-independently-constructed
  orchestrator-plus-store crash-mid-loop proof.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... -run TestTC169
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/orchestrator/... -run TestTC169_04
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md`: new `AGENT_BUILDER_GOAL_MAX_ATTEMPTS` row.
- `docs/spec/interfaces.md`: `RunToCompletion`/`ErrGoalAttemptsExhausted` added to
  the Tier-1 orchestrator seam documentation.
- `docs/spec/behaviors.md`: new behavior entry describing the fold-and-replan
  loop and escalation-on-exhaustion.

## Out of scope

- Sub-goal-level retry policy (`internal/loop.RetryPolicy`), unrelated,
  unmodified.
- Approval-pause interaction semantics (task 170/171 settles whether a pause
  consumes an attempt).
- Wiring `RunToCompletion` into the daemon's inbound-message loop (task 174/175).

## Dependencies

- **Blocks on:** task 167, 168, 115 (all already merged or in this batch).
- **Blocks:** task 175 (`scheduled-goals` dispatches through this path).
