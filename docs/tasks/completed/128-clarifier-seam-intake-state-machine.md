# Task 128: Clarifier seam + HeuristicClarifier v1 + intake state machine

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** completed
**ADR:** 058 — Conversational human-gated orchestrate front door
**Test spec:** [128-clarifier-seam-intake-state-machine-test-spec.md](../test-specs/128-clarifier-seam-intake-state-machine-test-spec.md)

## Goal

Implement the core conversational intake: the `Clarifier` seam + `HeuristicClarifier`
v1, split `Handle` into `BeginGoal` (sets `StateClarifying`, runs clarifier, reports
questions) + `ConfirmAndPlan` (the verbatim body of the original `Handle` from
`planner.Plan` onward, EXTRACTED not rewritten), and add the Clarifying-linger loop
to the goal actor. Include the `AGENT_BUILDER_INTAKE=auto` non-interactive escape
hatch. Also create `scripts/validate-orchestrate-intake.sh`.

## Context

This is the substantive rework of the ADR 058 series. Tasks 124–127 laid the
protocol/routing groundwork; this task delivers the actual conversational behavior.
The design mandate is **extraction, not rewrite**: `ConfirmAndPlan` is the current
body of `Handle` from `planner.Plan` onward, moved verbatim. The risk is accidentally
regressing the gated paths; extraction keeps that risk minimal.

The `Clarifier` seam mirrors the `Planner` seam in shape:
- `Clarifier` interface: `Clarify(goal supervisor.Task) (Clarification, error)`
- `Clarification` struct: `Ready bool`, `Questions []string`
- `HeuristicClarifier` v1 (no LLM): deterministic, unit-testable. Heuristics:
  - Empty / very-short spec → ask what to build.
  - No repo/URL in spec → ask which repo.
  - Both present → `Ready = true`.

The `AGENT_BUILDER_INTAKE=auto` escape hatch makes `BeginGoal` bypass the
Clarifying linger loop — `ConfirmAndPlan` is called immediately, no stdin wait.
This is essential for CI/L5 harnesses that pipe scripted goals and cannot interactively
reply to clarifying questions.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-128-01 | `Clarifier` interface + `Clarification` type in `internal/orchestrator/clarifier.go` (new file). `HeuristicClarifier` implements `Clarifier` with the three-heuristic v1 logic (empty spec, no repo, both present). | must have |
| REQ-128-02 | `Handle` is split into `BeginGoal(ctx, goal)` + `ConfirmAndPlan(ctx, goal)` by extraction. `BeginGoal` sets `StateClarifying`, calls `Clarifier.Clarify`, reports questions or a "ready — reply `confirm`" prompt, and returns. `ConfirmAndPlan` is the verbatim body of the original `Handle` from `planner.Plan` onward. | must have |
| REQ-128-03 | The Clarifying-linger loop in `goal_actor.go` mirrors the AwaitingApproval drain: `MsgInfo` → fold into goal text (existing `EnqueueInfo`/`FoldGoalText`) + re-clarify; `MsgConfirm` → call `orch.ConfirmAndPlan`; `MsgCancel` → existing teardown; `ctx.Done` → sweep + exit. | must have |
| REQ-128-04 | The actor does NOT start planning until `MsgConfirm` is received (linger loop enforces this). The actor lingers in `StateClarifying` draining the mailbox, folding info, and re-clarifying as needed. | must have |
| REQ-128-05 | TC-128-07 (L5 conversation assertion): a scripted conversation `"fix bug"` → question → `"info goal-1 in github.com/…"` → re-clarify → `"confirm goal-1"` → approval pause produces hard-asserted Reporter lines (clarifying question + approval solicitation). The StructuredPlanner and real heuristic clarifier are used; the policy stub returns `DecisionRequireApproval`. | must have |
| REQ-128-06 | `AGENT_BUILDER_INTAKE=auto` (read via the injected `getenv` seam): `BeginGoal` skips the clarifier call and calls `ConfirmAndPlan` immediately. Registry state skips `StateClarifying`. | must have |

## Design mandate — extraction, not rewrite

**This is load-bearing.** `ConfirmAndPlan` must be the current `Handle` body from
`planner.Plan` onward, moved byte-for-byte (comments and all) into the new function.
Do NOT restructure, simplify, or refactor it. Any behavioral change to the
post-planning path is out of scope and a regression risk. The test spec's TC-128-04
specifically asserts that `ConfirmAndPlan` produces the identical observable outcome
to calling the original `Handle` on the plan-onward path.

If the extraction reveals any code smell or opportunity for improvement, note it in a
comment and leave it for a follow-up task — do not fix it in place.

## Acceptance criteria

1. `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/... ./tests/orchestrate/...`
   passes; all eight TCs non-vacuous (hard assertions, not smoke tests).
2. TC-128-02 (`TestBeginGoalSetsClarifyingState`): reporter receives the clarifying
   question and state transitions to `StateClarifying` — hard assertions.
3. TC-128-04 (`TestConfirmAndPlanInvokesExistingHandleBody`): planner called exactly
   once after `ConfirmAndPlan`; reporter receives approval solicitation — hard
   assertion on call count (not zero) and reporter content.
4. TC-128-07 (`TestL5ConversationalIntakeVagueGoalToDispatch`): the scripted
   conversation produces at least two hard-asserted Reporter lines:
   (a) the clarifying question (contains `"repo"` or `"repository"` or `"what"`);
   (b) the approval solicitation (contains `"Approve?"` or `"approve"`).
   `DispatchFunc` spy records zero calls (plan held). Planner called exactly once.
5. TC-128-08 (`TestIntakeAutoEscapeHatchSkipsClarification`): with
   `AGENT_BUILDER_INTAKE=auto`, `ConfirmAndPlan` is called immediately, clarifier is
   never called, reporter has no clarifying question.
6. `HeuristicClarifier` is deterministic and unit-testable without LLM or IO.
7. `ConfirmAndPlan` byte-identical to the original `Handle` post-plan body (no
   behavioral change — the extraction invariant).
8. `scripts/validate-orchestrate-intake.sh` created and executable.
9. `docs/spec/behaviors.md` updated (Clarifying state + intake flow).
10. `docs/spec/interfaces.md` updated (`Clarifier` interface, `BeginGoal`,
    `ConfirmAndPlan`, `AGENT_BUILDER_INTAKE`).
11. `docs/architecture/diagrams.md` updated (Clarifying box in the front-door flow).
12. `make check` passes.
13. `git status` clean on commit.

## Files changed

- `internal/orchestrator/clarifier.go` (new) — `Clarifier` interface, `Clarification`, `HeuristicClarifier`.
- `internal/orchestrator/orchestrator.go` — split `Handle` into `BeginGoal` + `ConfirmAndPlan`.
- `internal/cli/goal_actor.go` — Clarifying-linger loop.
- `internal/cli/orchestrate.go` — `clarifierFromEnv`, `EnvIntake` constant, `AGENT_BUILDER_INTAKE=auto` wiring.
- `internal/orchestrator/clarifier_test.go` (new) — TC-128-01, TC-128-02.
- `internal/orchestrator/orchestrator_test.go` — TC-128-03, TC-128-04, TC-128-05.
- `internal/cli/goal_actor_test.go` — TC-128-06.
- `tests/orchestrate/intake_test.go` (new) — TC-128-07, TC-128-08.
- `scripts/validate-orchestrate-intake.sh` (new) — L5 validation harness.
- `docs/spec/behaviors.md`, `docs/spec/interfaces.md`, `docs/architecture/diagrams.md`.

## Verification plan

**L2/L3 (achievable in CI):**
`go test -race -count=1 ./internal/orchestrator/... ./internal/cli/... ./tests/orchestrate/...`
— all eight TCs pass with hard assertions (TC-128-07 is the load-bearing L5 test,
run with real StructuredPlanner and HeuristicClarifier, stub policy, FakeReporter).

`make check` — lint + build + all fitness functions green (F-010 / F-014 must remain
satisfied: `clarifier.go` must not import `internal/executor`).

**L5 (scripted):**
`scripts/validate-orchestrate-intake.sh` — pipes the scripted conversation to
`agent-builder orchestrate` and greps for the three Reporter-line assertions
(clarifying question, approval solicitation). Uses real `StructuredPlanner` +
real `HeuristicClarifier` + real policy-engine binary at
`~/Code/Public/policy-engine/policy-engine` with a plan-covering allow set.
`AGENT_BUILDER_INTAKE` is NOT set (interactive). The script drives stdin.

**L6 (operator-observed):**
Telegram round-trip: vague goal → `HeuristicClarifier` asks a question via
`ReplyAdapter.Report` → operator replies with info via Telegram → `go` (reply-to) →
approval pause → operator approves → plan result reported. This proves the
channel-abstract claim. Additionally: a policy-deny scenario so `needs-human` reaches
the channel via `Reporter` for a synthetic goal ID (preview of task 130's L6).

## Dependencies

- Tasks 124–127 (all must be merged before this task begins).
- Task 123 merged to `main` (per plan prerequisite).

## Out of scope

- Approval-default (`AGENT_BUILDER_REQUIRE_APPROVAL`) — task 129.
- Escalation over the channel (`reporterStatusWriter`) — task 130.
- LLM clarifier (`AGENT_BUILDER_CLARIFIER=llm`) — task 131 (deferred).
- Re-dispatch after a resolved replan — deferred follow-up.
