# Task 147 — Answer route terminates on source EOF (conversing-linger drain)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/147-answer-route-terminates-on-source-eof-test-spec.md`
**Relates to:** ADR 060 §6 (multi-turn answer conversation), ADR 054 (control plane / goal actor). Follows task 141 (introduced the `StateConversing` linger) and task 146 (whose L6 surfaced this).

## Why

A `KindAnswer` goal answers and lingers in `StateConversing` for follow-ups (task
141). The linger blocks on `<-ctx.Done()` only (`internal/cli/goal_actor.go:137-139`).
When the control-loop `MessageSource` is a **finite** source (env
`AGENT_BUILDER_GOAL_SPEC` + EOF stdin, a piped goal, `< /dev/null`, the L5 validation
harness), the loop closes `shutdown` and calls `wg.Wait()` but never cancels `ctx`, so
the conversing actor blocks forever. The process hangs until killed and then reports a
signal-kill exit code (124/143).

The answer content is fine — the harm is **non-termination**: any non-interactive
caller hangs after the answer is delivered, and any exit-status check reads a
successful answer as a failure. This blocks the answer route from ever reaching L5
(the validation harness would hang / record failure). The linger is correct on a live
channel (Telegram, interactive stdin) and wrong on a finite source.

Surfaced concretely by task 146's L6 OBS B: the security-goal answer printed correctly,
then the process was killed at the 200 s timeout (`exit=124`).

## Scope

- **`internal/cli/goal_actor.go`:** in the `StateConversing` linger, select on
  **both** `ctx.Done()` and `a.shutdown` (already a field on `goalActor`), then
  `EndConversation`. A conversing goal ends on cancel **or** source drain. Update the
  linger comment (lines ~135-136) so it matches (it already claims "source EOF").
- **`internal/orchestrator/conversation.go`:** the `EndConversation` doc comment
  already says "on cancel / source EOF" — keep it accurate (no behavior change here).
- **`docs/spec/behaviors.md`:** the answer-conversation behavior reflects that the
  conversation ends on cancel **or** source drain (finite source → clean termination).

## Out of scope

- Changing the live-channel linger semantics (Telegram/interactive stdin still linger
  for follow-ups while input remains — REQ-147-04 guards this).
- In-flight dispatch teardown / cancellation (task 116 owns that); this task only
  affects the post-answer conversing linger, which holds no worker.
- Any change to tier routing (tasks 145/146).

## Verification plan

- **Highest level achievable here:** L6 (operator runs the answer route over a finite
  source and observes the process exit 0 on its own after reporting the answer).
- **L2:** `TestConversingGoalEndsOnShutdown` (shutdown-close → `StateDone`, actor
  returns, timeout-guarded), `TestControlLoopReturnsOnFiniteSourceAfterAnswer` (finite
  source → loop returns with no cancel, goal `StateDone`), `TestConversingGoalStillEndsOnCancel`
  (regression: cancel path preserved), and task 141's `TestConversingAnswerAndFollowUp`
  still green (no premature end while input remains). Non-vacuity: the shutdown test
  hangs/fails on the current `<-ctx.Done()`-only code and passes with the `select`.
- **L3:** `make check` green (Go + fitness, F-014 intact).
- **L6:** `AGENT_BUILDER_GOAL_SPEC="What is the capital of France?" AGENT_BUILDER_GOAL_ANALYSIS=heuristic … ./agent-builder orchestrate < /dev/null`
  reports the answer and **exits 0 without an external kill** (contrast task 146 OBS B
  `exit=124`).

## Notes

No ADR required — this restores behavior both code comments already document; it is a
bug fix, not a new design decision. The `goalActor.shutdown` field already exists and
is threaded from the control loop (`runGoalActor`), so no new plumbing is needed.
