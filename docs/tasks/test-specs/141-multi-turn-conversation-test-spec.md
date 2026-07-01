# Test spec — Task 141: multi-turn conversation for KindAnswer goals

**Task:** `docs/tasks/backlog/141-multi-turn-conversation.md`
**Relates to:** ADR 060 §6, tasks 139/140, tasks 115/128.

## Context

A `KindAnswer` goal becomes a conversation: after the first answer it lingers in `StateConversing`
with a transcript; a follow-up `info <goalID> <text>` is answered with context; `cancel`/EOF ends it.
The transcript is carried in the single prompt (each brain CLI is stateless single-shot). Reuses the
existing goal-actor mailbox/lingering machinery (the same `applyInfo` seam that re-clarifies coding
goals).

## Requirements

- **REQ-141-01** — After the first answer, a KindAnswer goal is `StateConversing` (not `StateDone`),
  with history `[(user,Q1),(assistant,A1)]`. `IsConversing(goalID)` reports true.
- **REQ-141-02** — `ContinueAnswer(ctx, goalID, text)` appends `(user,text)`, composes the running
  transcript into the Answerer prompt, reports the reply, appends `(assistant,reply)`, stays
  `StateConversing`. The follow-up prompt carries the prior turns as context.
- **REQ-141-03** — `composeTranscript` renders `User: …\nAssistant: …\nUser: <latest>\nAssistant:`
  (deterministic, ordered).
- **REQ-141-04** — `EndConversation(goalID)` sets `StateDone` and drops history. The goal actor, while
  the goal is `StateConversing`, lingers on the mailbox — `MsgInfo` → `ContinueAnswer` (via
  `applyInfo`'s `StateConversing` case); `MsgCancel`/ctx.Done/EOF → `EndConversation`.
- **REQ-141-05** — Boundary + fitness unchanged (F-010/F-014, supervisor isolation green); the
  concurrency is `-race` clean (history guarded by `convMu`, not held across the Answer IO call).

## Test cases

- **TC-141-01/02** (`TestConversingAnswerAndFollowUp`, `-race`) — first answer → `StateConversing` +
  `IsConversing`; follow-up via `ContinueAnswer` (echo answerer) → both replies reported in order
  (`Paris`, `Berlin`), the follow-up prompt contains Q1+A1+Q2 (context carried), history grows to 4
  turns, still `StateConversing`.
- **TC-141-03** (`TestComposeTranscript`) — exact transcript rendering (hard string assertion).
- **TC-141-04** (`TestEndConversation`) — `EndConversation` → `StateDone`, `IsConversing` false. The
  goal-actor linger + `applyInfo` `StateConversing` routing is verified at **L6** (live).
- **TC-141-05 (L6, operator)** — live orchestrate over the channel: `GOAL_SPEC="capital of France?"` →
  `Paris`; `info q1 capital of Germany?` → `Berlin`; `cancel q1` → conversation ends.

## Non-vacuous / negative controls

- TC-141-01/02 asserts the follow-up prompt actually CONTAINS the prior turns (proves context is
  carried, not just that a second answer was produced), and that the state stays conversing after the
  follow-up (not prematurely terminal).
