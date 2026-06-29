# Task 123: Wire dispatchPlan → ReevaluateBlockedSpawn on live deny path

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 4 — end-to-end wiring)
**Test spec:** [123-orchestrate-wire-blocked-reevaluation-test-spec.md](../test-specs/123-orchestrate-wire-blocked-reevaluation-test-spec.md)

## Goal

Close ADR 055 seam 4 end-to-end: wire `dispatchPlan` so that every `SubGoalOutcome`
where `outcome.Blocked != nil` invokes `o.ReevaluateBlockedSpawn(goal, *outcome.Blocked,
bound, statusWriter)` after the goroutine join and folds the `loop.ReevaluationOutcome`
(resolved-via-replan or needs-human escalation) into the returned `PlanResult` /
`SubGoalOutcome`. Today the consumer (`ReevaluateBlockedSpawn`) is dead code on the live
path — `dispatchPlan` aggregates outcomes but never inspects `outcome.Blocked`. This task
makes it live.

## Context

Task 121 (ADR 055 seam 4) built `classifyBlockedSpawn` (producer, live) and
`ReevaluateBlockedSpawn` (consumer, unit-tested but unreachable on the live path).
`grep -rn ReevaluateBlockedSpawn internal/ cmd/` finds references ONLY in
`blocked_action.go` and `blocked_action_test.go` — it is never called from
`dispatchPlan`. Task 121 therefore cannot honestly reach L5/L6: its consumer never
fires in production. This task is the missing wire that closes the gap and also
enables task 121's L5/L6 evidence.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-123-01 | `dispatchPlan` iterates over aggregated outcomes after `wg.Wait()` and, for each `outcome.Blocked != nil`, invokes `o.ReevaluateBlockedSpawn`. Reevaluation runs SERIALLY after the join — never inside per-sub-goal goroutines (keeps replanning off the concurrent hot path and avoids planner races). | must have |
| REQ-123-02 | The `loop.ReevaluationOutcome` (resolved or escalated) is folded back into `SubGoalOutcome`: a new exported `ReevaluationOutcome loop.ReevaluationOutcome` field is added to `SubGoalOutcome`. Callers of `RenderPlanResult` see the escalation text or the resolved note in the rendered output. | must have |
| REQ-123-03 | The reevaluation bound comes from a configurable source — a new `reevaluationBound int` field on `Orchestrator` set by a new `WithReevaluationBound(int) Option`, defaulting (when zero / unset) to `AGENT_BUILDER_MAX_ATTEMPTS` (the existing `EnvMaxAttempts` env var read in `internal/runtime/run.go`). The orchestrate CLI path (`internal/cli/orchestrate.go`) reads this env var and passes it via `WithReevaluationBound`. No new env var is introduced unless `AGENT_BUILDER_MAX_ATTEMPTS` is semantically wrong for reevaluation; if a separate knob is needed, use `AGENT_BUILDER_MAX_REEVALUATIONS` (document in `docs/spec/configuration.md`). | must have |
| REQ-123-04 | `Orchestrator` gains a `statusWriter loop.StatusWriter` field set by a new `WithStatusWriter(loop.StatusWriter) Option` (mirroring `WithWorkerSemaphore`). A nil `statusWriter` is a documented no-op: `dispatchPlan` skips the `ReevaluateBlockedSpawn` call rather than panic or return an error. The orchestrate CLI path must **construct** a `loop.StatusWriter` (it does not already hold one — `run.go` is the only existing `tasksource.NewStatusWriter` site) via `tasksource.NewStatusWriter(<base config>.TaskRoot, tasksource.DefaultTaskDirs...)`, whose `*tasksource.StatusWriter` satisfies `loop.StatusWriter`. | must have |

## Pinned design decisions

**Where does the `supervisor.Task` for `ReevaluateBlockedSpawn` come from?**
`dispatchPlan` receives a `Plan` argument. `Plan.GoalID` is the original goal's
`Task.ID` and `Plan.Goal` is the original goal text (`Task.Spec`). Reconstruct the
`supervisor.Task` as `supervisor.Task{ID: plan.GoalID, Spec: plan.Goal}` — these
two fields are everything `ReevaluateBlockedSpawn` uses (it passes `goal.ID` to
`statusWriter.WriteStatus` and passes `goal` to `o.planner.Plan`). Do NOT store the
full original `supervisor.Task` on `Plan` to avoid broadening the struct; the two
fields are sufficient.

**Where does `loop.StatusWriter` come from?**
`Orchestrator` currently has no status-writer field. The loop retry path (tasks 012/013)
has its own `statusWriter` wired inside `runtime.Run` via `NewRetryingLoop`. The
orchestrate path needs a parallel injection. Solution: add `statusWriter loop.StatusWriter`
to the `Orchestrator` struct; supply it via `WithStatusWriter`.

**Correction (verified in source 2026-06-29):** the orchestrate path does **not**
already assemble a status writer — `grep StatusWriter internal/cli/*.go` finds none, and
`tasksource.NewStatusWriter` is constructed **only** in `internal/runtime/run.go` (the
worker/run path), not on the orchestrate path. So the executor must **construct** one on
the orchestrate path: `tasksource.NewStatusWriter(<base config>.TaskRoot,
tasksource.DefaultTaskDirs...)` (the `*tasksource.StatusWriter` it returns satisfies
`loop.StatusWriter` — confirmed: its `WriteStatus(taskID, WritableStatus)
(StatusWriteResult, error)` matches the interface). Root it at the orchestrate base
config's `TaskRoot`, the same value `run.go` uses.

**Open design point for the live (L5/L6) path, not L2/L3:** `tasksource.StatusWriter.
WriteStatus` rewrites the **Status: line of a matching task file**. On the orchestrate
path the goal is free text and may have no backing task file, in which case the live
needs-human write would error ("task not found"). The unit tests (TC-001/TC-005) use an
in-memory `memWriter` spy, so L2/L3 are unaffected — but the executor must note this and
either (a) confirm a task file exists for the dispatched sub-goal's ID, or (b) flag the
escalation-sink-on-free-text-goal question as a follow-up rather than silently shipping a
writer that errors at runtime. Do not paper over it.

**What is the reevaluation bound?**
Mirror the existing gate-failure retry bound. `AGENT_BUILDER_MAX_ATTEMPTS` is read in
`internal/runtime/run.go:EnvMaxAttempts`. Evaluate whether the same constant is
semantically appropriate for orchestrate-path reevaluation (it bounds replan attempts
before escalation, which is the same class of "how many times before giving up to a
human" as gate-failure retries). If it is appropriate, read it in `orchestrate.go` and
pass it via `WithReevaluationBound`. If a distinct name is warranted, define
`EnvMaxReevaluations = "AGENT_BUILDER_MAX_REEVALUATIONS"` in `orchestrate.go` and
document it in `docs/spec/configuration.md`. Either way, do NOT hardcode a magic
constant inside `dispatchPlan`.

**Concurrency/ordering:**
Reevaluation runs AFTER `wg.Wait()` completes, iterating serially over aggregated
outcomes. It must NOT be moved into the per-sub-goal goroutines. The rationale: the
`planner` inside `ReevaluateBlockedSpawn` is shared (single `o.planner` field), and
replanning it from N concurrent goroutines creates a potential race. Serial
post-join reevaluation avoids this while keeping the common (no-block) path cost-free.

**Does reevaluation re-dispatch?**
No. Task 123's scope is wiring + outcome-folding + escalation surfacing only. After
`ReevaluateBlockedSpawn` returns `ReevaluationResolved`, `dispatchPlan` records the
resolved outcome but does NOT re-dispatch the sub-goal in the same plan cycle. Full
re-dispatch on a resolved replan (a bounded retry loop at the dispatchPlan level) is a
follow-up task. The resolved outcome is surfaced to the caller (and eventually to the
reporter) so the operator can see that a replan succeeded — but the re-dispatched work
is left for a subsequent goal submission or a follow-up task to automate. State this
explicitly in a code comment at the reevaluation call site.

## Acceptance criteria

1. `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes;
   all 5 TC in the test spec are exercised with hard assertions (no smoke tests).
2. TC-001 (`TestDispatchPlanCallsReevaluateOnBlockedOutcome`) — `memWriter.writes`
   contains `"goal-1:needs-human"` after `dispatchPlan` runs with a deny policy and
   a still-blocked replan. This is the load-bearing assertion that the dead-code gap
   is closed.
3. TC-002 (`TestDispatchPlanNilStatusWriterSkipsReevaluation`) — `dispatchPlan` with
   no `WithStatusWriter` does not panic and does not error; `outcome.Blocked` is still
   present in the returned `PlanResult`.
4. TC-003/TC-004 — `SubGoalOutcome.ReevaluationOutcome` carries the escalated or
   resolved outcome respectively; never-self-grant invariant preserved (TC-004
   asserts `"coding-agent"` absent from `ReevaluationOutcome.AllowedResources`).
5. TC-005 — `WithStatusWriter` option wires the field; CLI assembles a non-nil writer.
6. `SubGoalOutcome` struct change reflected in `docs/spec/data-model.md`.
7. Reevaluation bound documented in `docs/spec/configuration.md` (whether reusing
   `AGENT_BUILDER_MAX_ATTEMPTS` or introducing `AGENT_BUILDER_MAX_REEVALUATIONS`).
8. `make check` passes (lint + build + fitness green).
9. `git status` clean on commit.

## Verification plan

**L2/L3 (achievable now):**
`go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` — all 5 TCs
non-vacuous. The load-bearing evidence: `memWriter.writes == ["goal-1:needs-human"]`
after `dispatchPlan` with a denyingPolicy and fixedPlanner (TC-001 / TC-003). This
asserts the producer→consumer trace is live end-to-end through `dispatchPlan`.

`make check` — lint + build + all fitness functions green.

**L5/L6 (via end-to-end orchestrate run, after tasks 118/119/120/121/122/123):**
Run `agent-builder orchestrate` against a goal whose plan needs a deployment-denied
recipe. Observe in logs: `Worker spawn denied` → reevaluation triggered → `needs-human`
written to the task file. Quote the log output and the task-file `needs-human` line.
This simultaneously closes task 121's own L5/L6 gap.

## Out of scope

- Re-dispatch after a resolved replan (follow-up task — see pinned decision above).
- Changes to the loop/retry gate-failure path (tasks 012/013/107/108 — unrelated).
- Changes to `ReevaluateBlockedSpawn` itself (task 121 work — do not modify the
  consumer; only wire the call site in `dispatchPlan`).
- New policy-engine block features (ADR 055 notes a request-scoped auth protocol as
  a follow-up to the policy block itself — out of scope here).

## Dependencies

- Task 121 (blocked-action feedback machinery — the consumer being wired here).
- Task 118 (`Plan.AllowedResources()` — used by the re-derived plan inside `ReevaluateBlockedSpawn`).
- Task 122 (plan-scoped allow set — needed for the end-to-end L5/L6 run to dispatch at all).
- Tasks 119/120 (route-to-worker + propagate-result — preconditions for end-to-end).

All dependencies are in `completed/` as of task 122 merge.
