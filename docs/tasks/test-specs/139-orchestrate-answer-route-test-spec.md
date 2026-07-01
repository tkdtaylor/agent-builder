# Test spec — Task 139: orchestrate answer route (single-turn)

**Task:** `docs/tasks/backlog/139-orchestrate-answer-route.md`
**Relates to:** ADR 060, task 138 (GoalAnalyzer), ADR 059 (Completer/Answerer).

## Context

The orchestrator routes a `KindAnswer` goal (task 138 analyzer) to the single-shot `Answerer` and
reports the answer over the channel — no clarifier, planner, policy-spawn gate, or approval (read-only
inference). A `KindCoding` goal falls through to the existing flow unchanged. Multi-turn is a follow-up
(task 141); this task is single-turn.

## Requirements

- **REQ-139-01** — `Answerer` seam: `Answer(ctx, prompt string, complexity GoalComplexity) (string,
  error)`. `WithGoalAnalyzer` and `WithAnswerer` options set the analyzer/answerer; a nil analyzer
  means every goal is `KindCoding` (pre-060 behavior, backward compatible).
- **REQ-139-02** — In `BeginGoal`, when an analyzer is set and returns `KindAnswer`: call
  `answerer.Answer(goal.Spec, complexity)`, `Reporter.Report(answer)`, set `StateDone`, and emit an
  `ActionCompletion` fleet-audit event. The clarifier/planner/policy/approval are not invoked.
- **REQ-139-03** — A `KindCoding` result falls through to the existing intake path unchanged
  (auto→ConfirmAndPlan, else StateClarifying + ClarifyAndReport). The answerer is not called.
- **REQ-139-04** — A `KindAnswer` goal with no answerer configured reports a clear "no answerer
  configured" message and sets `StateFailed` (never a silent drop).
- **REQ-139-05** — Boundary: `internal/orchestrator` imports no `internal/executor`; `make fitness`
  (F-010/F-014) stays green (the `Answerer` is an interface wired in `internal/cli`).

## Test cases

- **TC-139-01** (`TestBeginGoalAnswerRoute`) — REQ-139-02: analyzer→KindAnswer, fake answerer returns
  `"Paris"`; assert answerer received `goal.Spec` + `ComplexitySimple`, reporter got exactly
  `["Paris"]`, and state is `StateDone`.
- **TC-139-02** (`TestBeginGoalCodingFallsThrough`) — REQ-139-03: analyzer→KindCoding, real
  `HeuristicClarifier`, goal with no repo; assert the answerer is NOT called, the reported line is the
  coding clarifier's "…repository…" question, and state is `StateClarifying`.
- **TC-139-03** (`TestBeginGoalAnswerNoAnswerer`) — REQ-139-04: analyzer→KindAnswer, no answerer;
  assert the reported line contains "no answerer configured" and state is `StateFailed`.

## Non-vacuous / negative controls

- TC-139-01 asserts the exact reported text AND the answerer's received prompt/complexity.
- TC-139-02 asserts the answerer is NOT called (the coding path is genuinely taken, not the answer path).
