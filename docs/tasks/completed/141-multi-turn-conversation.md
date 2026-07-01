# Task 141 — multi-turn conversation for KindAnswer goals (join the interview model)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/141-multi-turn-conversation-test-spec.md`
**Relates to:** ADR 060 §6, tasks 139/140, tasks 115/128 (the existing clarify/interview loop this mirrors).

## Why (corrected scope)

A general-purpose agent must **converse toward a goal**, not one-shot it. The clarify/interview loop
already exists for coding goals (`goal_actor.applyInfo` folds each `info` and re-runs
`ClarifyAndReport` while lingering in `StateClarifying` — tasks 115/128). **Task 139's answer path
regressed this for answers**: a `KindAnswer` goal short-circuits in `BeginGoal`, answers once, and
goes `StateDone` — so it cannot take follow-up questions or be interviewed when vague. This task makes
answer goals join the same conversing/interview machinery.

## Scope

- **`internal/orchestrator`:** add `StateConversing`; hold per-goal conversation history; `answerGoal`
  sets `StateConversing` (not `StateDone`) and stores `[(user,Q1),(assistant,A1)]`; `composeTranscript`
  builds the single prompt the `Answerer` takes; `ContinueAnswer(ctx, goalID, text)` appends the user
  turn, re-answers with the transcript, reports, appends the assistant turn, stays `StateConversing`.
- **`internal/cli/goal_actor.go`:** after `BeginGoal`, if the goal is `StateConversing`, linger on the
  command mailbox — `MsgInfo` → `ContinueAnswer`; `MsgCancel`/ctx.Done/EOF → `StateDone`. Add a
  `StateConversing` case to `applyInfo` (mirrors the `StateClarifying` re-clarify case).
- **Spec + diagrams** updated; L5 (scripted multi-turn) + L6 (live).

## Out of scope

- Persistent cross-session memory (separate north-star slice — this is in-process, per-goal history).
- Clarify-before-answer for vague answer goals (a refinement; the interview loop for coding already
  handles vagueness — extend to answers in a follow-up if the heuristic proves too coding-centric).

## Verification plan

- **L2:** orchestrator `StateConversing` + `ContinueAnswer` + `composeTranscript` + history unit tests;
  goal-actor conversing-loop test with a fake mailbox (info→second answer, cancel→StateDone).
- **L5:** scripted stdin multi-turn against the binary (heuristic analyzer, local brain).
- **L6:** live two-turn: "capital of France?"→Paris → `info … what about Germany?`→Berlin (context carried).

## Boundaries

- Portable across brains: carry the transcript in the single prompt (each CLI is stateless single-shot).
- Read-only inference throughout (no approval); each turn is independently routed + audited.
