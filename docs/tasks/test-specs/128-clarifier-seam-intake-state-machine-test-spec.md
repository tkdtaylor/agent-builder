# Test Spec 128: Clarifier seam + HeuristicClarifier v1 + intake state machine

**Linked task:** [`docs/tasks/backlog/128-clarifier-seam-intake-state-machine.md`](../backlog/128-clarifier-seam-intake-state-machine.md)
**Written:** 2026-06-29
**ADR:** 056 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-128-01 | TC-128-01, TC-128-02 | ✅ |
| REQ-128-02 | TC-128-03, TC-128-04 | ✅ |
| REQ-128-03 | TC-128-05         | ✅ |
| REQ-128-04 | TC-128-06         | ✅ |
| REQ-128-05 | TC-128-07         | ✅ |
| REQ-128-06 | TC-128-08         | ✅ |

## Test locations

- `internal/orchestrator/clarifier_test.go` — TC-128-01, TC-128-02 (Clarifier seam + HeuristicClarifier)
- `internal/orchestrator/orchestrator_test.go` — TC-128-03, TC-128-04, TC-128-05 (intake state machine on orchestrator)
- `internal/cli/goal_actor_test.go` — TC-128-06 (linger loop behaviour)
- `tests/orchestrate/intake_test.go` (new integration test file) — TC-128-07 (L5 conversation assertion), TC-128-08 (AGENT_BUILDER_INTAKE=auto escape hatch)

Test function names:
- **TC-128-01:** `TestHeuristicClarifierReadyForSpecificGoal`
- **TC-128-02:** `TestHeuristicClarifierQuestionsForVagueGoal`
- **TC-128-03:** `TestBeginGoalSetsClarifyingState`
- **TC-128-04:** `TestConfirmAndPlanInvokesExistingHandleBody`
- **TC-128-05:** `TestMsgInfoFoldedDuringClarifying`
- **TC-128-06:** `TestClarifyingLingerLoopDrainsAndExitsOnConfirm`
- **TC-128-07:** `TestL5ConversationalIntakeVagueGoalToDispatch`
- **TC-128-08:** `TestIntakeAutoEscapeHatchSkipsClarification`

## Unit under test

Three new or modified pieces:

1. **`internal/orchestrator/clarifier.go`** (new file) — `Clarifier` interface +
   `Clarification` result type + `HeuristicClarifier` v1 implementation.
2. **`internal/orchestrator/orchestrator.go`** — `Handle` is split (by EXTRACTION):
   - `BeginGoal(ctx, goal)` — sets `StateClarifying`, calls `Clarifier.Clarify(goal)`,
     reports questions or a "ready — reply `confirm`" prompt; does NOT plan.
   - `ConfirmAndPlan(ctx, goal)` — the verbatim body of the original `Handle` from
     `planner.Plan` onward (moved, not rewritten).
3. **`internal/cli/goal_actor.go`** — the actor's linger loop (currently
   `AwaitingApproval`-only) gains a `Clarifying` linger arm: while the goal is
   `StateClarifying`, drain the mailbox; on `MsgInfo` fold into goal text and
   re-clarify; on `MsgConfirm` call `orch.ConfirmAndPlan`; on `MsgCancel` teardown;
   on `ctx.Done` sweep and exit. The `AGENT_BUILDER_INTAKE=auto` env var wires into
   `BeginGoal` so non-interactive harnesses bypass the clarification pause.

## Test cases

### TC-128-01: HeuristicClarifier returns Ready=true for a sufficiently specific goal

- **Requirement:** REQ-128-01
- **Setup:** construct a `HeuristicClarifier`. Call `Clarify` with a goal that names
  a repo and has more than a trivially short spec — e.g.
  `supervisor.Task{ID: "goal-1", Spec: "add a retry backoff to the exec-sandbox in github.com/tkdtaylor/exec-sandbox"}`.
- **Expected:**
  - `Clarification.Ready == true`
  - `Clarification.Questions` is empty (no questions for a ready goal)
  - `err == nil`

### TC-128-02: HeuristicClarifier returns questions for a vague or repo-less goal

- **Requirement:** REQ-128-01
- **Sub-case A — empty spec:**
  - Input: `supervisor.Task{ID: "goal-2", Spec: ""}` or `Spec: "   "`.
  - Expected: `Ready == false`, `len(Questions) >= 1`, `err == nil`.
  - Assert the question text contains `"build"` or similar (what to build).
- **Sub-case B — very short spec without a repo:**
  - Input: `supervisor.Task{ID: "goal-3", Spec: "fix bug"}` (no URL, no repo name).
  - Expected: `Ready == false`, `len(Questions) >= 1`, `err == nil`.
  - Assert the question text contains `"repo"` or `"repository"` (which repo).
- **Sub-case C — spec with repo but no meaningful action:**
  - Input: `supervisor.Task{ID: "goal-4", Spec: "github.com/tkdtaylor/exec-sandbox"}` (repo only, no action).
  - Expected: `Ready == false`, `len(Questions) >= 1`, `err == nil`.
  These three sub-cases confirm the three heuristic triggers from the plan spec.

### TC-128-03: BeginGoal sets StateClarifying and reports questions via Reporter

- **Requirement:** REQ-128-02
- **Setup:** construct an `Orchestrator` with a `HeuristicClarifier` (or a stub
  clarifier that always returns `Ready=false, Questions=["What repo?"]`) and a
  `FakeReporter`. Call `o.BeginGoal(ctx, supervisor.Task{ID: "goal-1", Spec: "fix bug"})`.
- **Expected:**
  - The orchestrator's registry state for `"goal-1"` is `StateClarifying`.
  - `reporter.Reported()` has exactly ONE entry containing `"What repo?"` (the
    clarifying question text). Assert `strings.Contains(reported[0], "What repo?")`.
  - `BeginGoal` returns without error (it is non-blocking — it sets state and reports
    questions, then returns; the actor's linger loop drives the rest).
  - `BeginGoal` does NOT call `planner.Plan` (assert the fake planner's call count
    is 0 after `BeginGoal`).

### TC-128-04: ConfirmAndPlan invokes the existing Handle body (the plan-onward path)

- **Requirement:** REQ-128-02
- **Setup:** construct an `Orchestrator` with a stub planner and a stub policy
  (`DecisionRequireApproval`) and a `FakeReporter`. Call
  `o.ConfirmAndPlan(ctx, supervisor.Task{ID: "goal-1", Spec: "coding-agent: fix bug in github.com/tkdtaylor/exec-sandbox"})`.
- **Expected:**
  - `planner.Plan` is called exactly once (the existing plan-onward body runs).
  - The reporter receives the approval solicitation text (the approval gate fires,
    consistent with the pre-existing `Handle` path behavior with `DecisionRequireApproval`).
  - `ConfirmAndPlan` does not return an error on the approval-pause path.
  - Assert the registry state transitions to `StateAwaitingApproval` after
    `ConfirmAndPlan` (the plan is held, not dispatched, because policy requires approval).
  This is the "extraction not rewrite" assertion: the identical outcome to calling
  the original `Handle` directly (before the split).

### TC-128-05: MsgInfo during Clarifying is folded and re-clarification is solicited

- **Requirement:** REQ-128-03
- **Setup:** construct an `Orchestrator` with a stub clarifier that returns
  `Ready=false, Questions=["What repo?"]` on the first call, then `Ready=true` on
  the second call (simulating the user's info resolving the ambiguity). Use a
  `FakeReporter`.
  Call `o.BeginGoal(ctx, goal_without_repo)` — first clarification fires.
  Then call `o.EnqueueInfo("goal-1", "github.com/tkdtaylor/exec-sandbox")` to fold
  the info, and simulate the linger loop calling re-clarify (or call the internal
  re-clarify path directly).
- **Expected:**
  - After the first `BeginGoal`: `reporter.Reported()` has the first question.
  - After the info fold + re-clarification: `reporter.Reported()` has a second entry
    indicating the goal is now ready (e.g. "goal is clear — reply `confirm` when ready"
    or equivalent). Assert `len(reporter.Reported()) == 2`.
  - The registry state for `"goal-1"` remains `StateClarifying` until `MsgConfirm`
    is received.

### TC-128-06: Clarifying linger loop drains the mailbox and exits on MsgConfirm

- **Requirement:** REQ-128-04
- **Setup:** construct a `goalActor` in test mode with its mailbox pre-seeded with:
  `[MsgInfo("goal-1", "extra info"), MsgConfirm("goal-1")]`.
  Wire a stub `BeginGoal` (already set `StateClarifying`) and a spy `ConfirmAndPlan`.
- **Expected:**
  - The linger loop processes `MsgInfo` first: the info is folded (the spy records
    the fold call).
  - The linger loop then processes `MsgConfirm`: the spy `ConfirmAndPlan` is called
    exactly once with the augmented goal text (original + the info).
  - After `ConfirmAndPlan` returns, the linger loop exits (no more drain).
  - The loop does NOT stall waiting for more messages after `MsgConfirm`.
  - Assert `ConfirmAndPlan` was called exactly once (not zero, not more than once).

### TC-128-07: L5 conversation — vague goal → question → info → confirm → approval pause → Reporter assertions (HARD)

- **Requirement:** REQ-128-05
- **Setup:** construct a full `orchestrateConfig` with:
  - `HeuristicClarifier` (real, not stubbed — uses env default)
  - `StructuredPlanner` (real)
  - A real or stubbed policy returning `DecisionRequireApproval`
  - A `FakeReporter` collecting all reported strings
  - `AGENT_BUILDER_INTAKE` NOT set to `auto` (interactive mode — the linger loop runs)
  Drive a scripted conversation using a pre-seeded `envMessageSource` with the following
  line sequence:
  ```
  fix bug
  info goal-1 in github.com/tkdtaylor/exec-sandbox
  confirm goal-1
  ```
  Run `runControlLoop` (or `assembleOrchestrate` + loop equivalent) until the source
  is exhausted and the actor joins.
- **Expected (HARD assertions — not smoke tests):**
  - **Reporter line 1 (clarifying question):** `reporter.Reported()[0]` contains at
    least one of `"repo"` / `"repository"` / `"what"` — the `HeuristicClarifier`'s
    question for a goal without a repo.
  - **Reporter line 2 (approval solicitation):** `reporter.Reported()` contains a
    string matching `"Approve?"` or `"approve"` and includes the plan text. Assert
    `len(reporter.Reported()) >= 2`.
  - **Registry state after `confirm goal-1`:** the goal transitions from
    `StateClarifying` → `StateAwaitingApproval` (the plan is held for approval).
  - **Planner called exactly once:** the stub/real planner's call count is 1 (planning
    happens after confirm, not during clarification).
  - **No dispatch:** `DispatchFunc` is NOT called (approval is pending, so workers
    are not started). Assert via a spy `DispatchFunc` that records calls.
  This is the load-bearing L5 assertion of the full conversational intake. It uses
  the real `HeuristicClarifier` (deterministic, no LLM), real `StructuredPlanner`,
  and real control-loop routing. The three Reporter lines it asserts are:
  (1) the clarifying question, (2) the approval solicitation. A third
  `RenderPlanResult` reporter line appears only AFTER approval is granted (not
  tested here since no `Resume` call is made — that is the end-to-end L6 path).

### TC-128-08: AGENT_BUILDER_INTAKE=auto skips clarification and proceeds directly to plan

- **Requirement:** REQ-128-06
- **Setup:** set `AGENT_BUILDER_INTAKE=auto` (via the test's `getenv` stub). Construct
  an `Orchestrator` with the same `HeuristicClarifier` and a spy `ConfirmAndPlan`.
  Call `o.BeginGoal(ctx, supervisor.Task{ID: "goal-1", Spec: "fix bug"})`.
- **Expected:**
  - The clarifier is NOT called (no question is generated in auto mode).
  - `ConfirmAndPlan` is called immediately (without waiting for a `MsgConfirm`).
  - `reporter.Reported()` contains NO clarifying question — the auto path bypasses
    the Clarifying linger loop entirely.
  - Registry state transitions directly from `StateQueued` → `StateDispatching` (or
    `StateAwaitingApproval` if policy requires it) — it never passes through
    `StateClarifying` in auto mode.
  This is the escape hatch that prevents CI/L5 harnesses from deadlocking on
  stdin-wait while the clarification linger loop runs.

## Post-implementation verification

- [ ] `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/... ./tests/orchestrate/...`
  passes with all eight TCs non-vacuous (hard assertions, not smoke tests)
- [ ] `make check` passes (lint + build + all fitness functions green)
- [ ] `docs/spec/behaviors.md` updated: the goal lifecycle section gains the
  `Clarifying` state and the `BeginGoal → Clarifying → ConfirmAndPlan → AwaitingApproval`
  flow in the same commit
- [ ] `docs/spec/interfaces.md` updated: `Clarifier` interface + `Clarification` type
  + `HeuristicClarifier` concrete documented; `BeginGoal` and `ConfirmAndPlan` appear
  in the Tier-1 orchestrator surface; `AGENT_BUILDER_INTAKE` documented
- [ ] `docs/architecture/diagrams.md` updated: the orchestrate front-door flow gains
  the `Clarifying` box between `intake` and `plan` in the same commit
- [ ] The "extraction not rewrite" invariant is confirmed: TC-128-04 proves
  `ConfirmAndPlan` produces the same observable outcome as the original `Handle` on
  the plan-onward path. Any divergence from the original behavior is a regression, not
  an improvement.
- [ ] `scripts/validate-orchestrate-intake.sh` created: pipes the scripted conversation
  (TC-128-07's input) to `agent-builder orchestrate` and greps for the three
  Reporter-line assertions. This is the L5 validation harness command.

## Test framework notes

- Go `testing`. TC-128-07 and TC-128-08 live in `tests/orchestrate/intake_test.go`
  (an integration test using the real control-loop plumbing but fake policy/reporter).
- The `FakeReporter` from task 098 is reused throughout.
- TC-128-04's "extraction not rewrite" proof: run the ORIGINAL `Handle` (pre-split)
  on a copy of the code in a test fixture, then run `ConfirmAndPlan` on the same
  input, and assert `reporter.Reported()` is identical in both. If the original
  `Handle` is no longer available (it is replaced), assert TC-128-04's expected
  outcomes match the known pre-split behavior documented in task 081's tests.
- `AGENT_BUILDER_INTAKE`: read via the injected `getenv` (the same pattern as
  `EnvPlanner` / `EnvInbound` / `EnvMaxWorkers`). The constant name is
  `EnvIntake = "AGENT_BUILDER_INTAKE"`; value `"auto"` triggers the bypass.
- This is the largest task in the series. Its test spec deliberately requires a full
  L5 conversation assertion (TC-128-07) with hard Reporter-line assertions, not a
  smoke test. The complexity is the design's risk surface — the extraction + linger
  loop + heuristic clarifier all intersect here.
- L5 achieved via `scripts/validate-orchestrate-intake.sh` with StructuredPlanner +
  real policy-engine binary. L6 is the Telegram round-trip (operator-observed).
