# Test spec — Task 141: multi-turn conversation for KindAnswer goals

**Task:** `docs/tasks/backlog/141-multi-turn-conversation.md`
**Relates to:** ADR 060 §6, tasks 139/140, tasks 115/116/128.

## Context

A `KindAnswer` goal becomes a conversation: after the first answer the goal lingers in
`StateConversing` with a transcript; `info <goalID> <text>` is a follow-up answered with context;
`cancel`/EOF ends it. The transcript is carried in the single prompt (each brain CLI is stateless).

## Requirements

- **REQ-141-01** — After the first answer, a KindAnswer goal is `StateConversing` (not `StateDone`),
  with history `[(user,Q1),(assistant,A1)]`.
- **REQ-141-02** — `ContinueAnswer(ctx, goalID, text)` appends `(user,text)`, composes the running
  transcript into the Answerer prompt, reports the reply, appends `(assistant,reply)`, stays
  `StateConversing`.
- **REQ-141-03** — `composeTranscript(history)` renders `User: …\nAssistant: …\nUser: <latest>\nAssistant:`
  (deterministic, ordered).
- **REQ-141-04** — Goal actor: while `StateConversing`, `MsgInfo` → `ContinueAnswer`; `MsgCancel` /
  ctx.Done / source EOF → `StateDone`. `applyInfo` is state-aware (clarifying-fold vs conversing-followup).
- **REQ-141-05** — Boundary + fitness unchanged (F-010/F-014, supervisor isolation green).

## Test cases (to implement)

- **TC-141-01** — first answer → `StateConversing` + history has the Q/A pair.
- **TC-141-02** — `ContinueAnswer` with a fake Answerer that echoes the transcript: assert the reply is
  reported, history grows to 4 entries, transcript contained both prior turns + the follow-up.
- **TC-141-03** — `composeTranscript` exact rendering (hard string assertion).
- **TC-141-04** — goal-actor follow-up loop (fake mailbox): info→second answer reported; cancel→StateDone.
- **TC-141-05 (L5/L6)** — scripted/live two-turn: "capital of France?"→Paris, "info … what about
  Germany?"→Berlin (context carried).
