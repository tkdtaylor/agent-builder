# Test spec — Task 147: Answer route terminates on source EOF

**Task:** `docs/tasks/backlog/147-answer-route-terminates-on-source-eof.md`
**Relates to:** ADR 060 §6 (multi-turn answer conversation), ADR 054 (control plane / goal actor). Follows task 141 (which introduced the `StateConversing` linger) and task 146 (whose L6 surfaced this bug).

## Context

A `KindAnswer` goal answers, then lingers in `StateConversing` (task 141) so follow-up
`info` messages continue the conversation. The goal actor's linger blocks on
`<-ctx.Done()` only — [internal/cli/goal_actor.go:137-139]:

```go
if st, ok := a.oc.registry.Get(a.goal.ID); ok && st.State == orchestrator.StateConversing {
    <-ctx.Done()
    a.oc.orch.EndConversation(a.goal.ID)
}
```

When the control loop's `MessageSource` is **exhausted** (a finite source: env
`AGENT_BUILDER_GOAL_SPEC` + EOF stdin, a piped goal, `< /dev/null`, the L5 validation
harness), the loop `close(shutdown)`s and calls `wg.Wait()` — but it does **not**
cancel `ctx`. The conversing actor never observes `shutdown`, so it blocks on
`ctx.Done()` forever, `wg.Wait()` never returns, and the process hangs until an
external signal kills it (exit 124/143). The answer was delivered correctly; the
process simply never terminates and reports a signal-kill exit code — so any
non-interactive caller hangs and any exit-status check reads success as failure.

Both [goal_actor.go:135-136] and [conversation.go:109] already document the intended
behavior as "ends on cancel / **source EOF**" — the EOF half is unimplemented. The
`goalActor` struct already carries `shutdown <-chan struct{}`; the fix is to select on
it alongside `ctx.Done()`.

## Requirements

- **REQ-147-01** — A goal in `StateConversing` ends (transitions to `StateDone` and
  its conversation history is dropped via `EndConversation`) when the control loop's
  `shutdown` channel is closed (source exhausted), **without** requiring context
  cancellation. The linger selects on **both** `ctx.Done()` and `a.shutdown`.
- **REQ-147-02** — The orchestrate control loop returns (its run function unblocks
  from `wg.Wait()`) after a **finite** `MessageSource` is drained, even when a goal
  answered and entered `StateConversing`. No external cancellation is required.
- **REQ-147-03** — Cancellation still ends a conversing goal exactly as before: with
  `shutdown` open (input still live), a `ctx` cancel transitions the goal to
  `StateDone`. The `ctx.Done()` path is preserved (no regression).
- **REQ-147-04** — While input remains (source not yet exhausted, `shutdown` open), a
  conversing goal does **not** end prematurely: a follow-up `info` still routes to
  `ContinueAnswer` and the goal stays conversing (task 141 behavior intact — the
  `select` must not fire on a still-open `shutdown`).
- **REQ-147-05** — Comments in `goal_actor.go` and `conversation.go` match the
  implementation (the "source EOF" claim is now true). `docs/spec/behaviors.md`
  reflects that a conversation ends on cancel **or** source drain. `make check` green.

## Test cases

- **TC-147-01** (`TestConversingGoalEndsOnShutdown`, `internal/cli`) — drive a goal
  actor whose goal reaches `StateConversing` (injected `Answerer` returns an answer,
  `GoalAnalyzer` classifies `KindAnswer`); with `ctx` **not** cancelled, close the
  `shutdown` channel and assert the actor's `run` returns **and** the goal's registry
  state is `StateDone`. Guard the "returns" assertion with a timeout (e.g. run in a
  goroutine, `select` on a done-channel vs. a short `time.After`) so the bug manifests
  as a deterministic test failure rather than a hung suite. **Non-vacuity:** with the
  current `<-ctx.Done()`-only code this test times out/fails; with the `select` it
  passes.
- **TC-147-02** (`TestControlLoopReturnsOnFiniteSourceAfterAnswer`, `internal/cli`) —
  run the orchestrate control loop over a finite `envMessageSource`
  (`AGENT_BUILDER_GOAL_SPEC` set, empty/EOF stdin) with an injected answerer+analyzer
  that yields a `KindAnswer`→`StateConversing` goal; assert the loop's run function
  returns (timeout-guarded) with **no** cancellation, and the goal ends `StateDone`.
  This is the producer→consumer proof that source-drain alone unblocks the process.
- **TC-147-03** (`TestConversingGoalStillEndsOnCancel`, `internal/cli`) — regression:
  `shutdown` **open**, `ctx` cancelled → conversing goal reaches `StateDone`. The
  pre-147 cancel path is preserved.
- **TC-147-04** (regression) — the existing multi-turn follow-up test
  (`TestConversingAnswerAndFollowUp`, task 141) still passes: while `shutdown` is open
  an `info` continues the conversation and the goal stays conversing (no premature
  end).

## Verification levels

- **L2** — the unit tests above (`go test ./internal/cli/... ./internal/orchestrator/...`).
- **L3** — `make check` green (Go + fitness, F-014 intact).
- **L6** — operator runs the `orchestrate` answer route over a finite source
  (`AGENT_BUILDER_GOAL_SPEC=… ./agent-builder orchestrate < /dev/null` with
  `AGENT_BUILDER_GOAL_ANALYSIS` on) and observes the process **report the answer and
  exit 0 on its own** — the exact OBS B scenario from task 146, now terminating
  cleanly instead of being killed at a timeout (exit 124/143).
