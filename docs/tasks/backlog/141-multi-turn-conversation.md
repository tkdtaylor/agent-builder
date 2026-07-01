# Task 141 — multi-turn conversation for KindAnswer goals

**Status:** backlog
**Spec:** `docs/tasks/test-specs/141-multi-turn-conversation-test-spec.md`
**Relates to:** ADR 060 §6 (multi-turn), tasks 139/140, tasks 115/116/128 (goal-actor lingering + mailbox).

## Goal

Make a `KindAnswer` goal a **conversation, not a one-shot**: after the first reply the goal actor
lingers in `StateConversing`, holding the transcript; a follow-up `info <goalID> <text>` is answered
with context; `cancel <goalID>` (or EOF) ends it.

## Scope

- **`internal/orchestrator`:** add `StateConversing`; hold per-goal conversation history
  `[(role,text)…]`; on the first answer set `StateConversing` (not `StateDone`); a
  `composeTranscript(history)` builder → the single prompt the `Answerer` takes; a `ContinueAnswer`
  method (append user turn → answer → report → append assistant turn).
- **`internal/cli/goal_actor.go`:** after `BeginGoal`, if the goal is `StateConversing`, enter a
  follow-up loop reading the command mailbox — `MsgInfo` → `ContinueAnswer`; `MsgCancel`/ctx.Done/EOF
  → `StateDone`. Reuses the existing drain/mailbox machinery; `handleCommand`/`applyInfo` become
  state-aware (clarifying-fold vs conversing-followup).
- **Spec + diagrams** updated; L5 (scripted multi-turn: "capital of France?" → Paris → "info … what
  about Germany?" → Berlin) + L6 (live).

## Out of scope

- Persistent cross-session memory (a separate north-star slice — this is in-process, per-goal history).

## Verification plan

- **L2:** orchestrator conversing-state + `ContinueAnswer` + transcript unit tests; goal-actor
  follow-up-loop test with a fake mailbox (info→second answer, cancel→StateDone).
- **L5:** scripted stdin multi-turn against the binary (heuristic analyzer, fake/local brain).
- **L6:** live two-turn exchange over the channel with context carried.

## Boundaries

- Portable across brains: carry the transcript in the single prompt (each CLI is stateless single-shot).
- Read-only inference throughout (no approval); each turn is independently routed + audited.
