# Test Spec 127: StateClarifying registry state + route MsgConfirm

**Linked task:** [`docs/tasks/backlog/127-state-clarifying-route-msgconfirm.md`](../backlog/127-state-clarifying-route-msgconfirm.md)
**Written:** 2026-06-29
**ADR:** 058 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-127-01 | TC-127-01         | ✅ |
| REQ-127-02 | TC-127-02, TC-127-03 | ✅ |
| REQ-127-03 | TC-127-04         | ✅ |

## Test locations

- `internal/orchestrator/registry_test.go` — TC-127-01 (StateClarifying constant)
- `internal/cli/orchestrate_test.go` (or `router_test.go`) — TC-127-02, TC-127-03, TC-127-04 (routing)

Test function names:
- **TC-127-01:** `TestStateClarifyingConstantAndString`
- **TC-127-02:** `TestRouteMsgConfirmDeliveredToGoalMailbox`
- **TC-127-03:** `TestRouteMsgConfirmUnknownGoalIDGraceful`
- **TC-127-04:** `TestHandleCommandAcceptsMsgConfirmWhenClarifying`

## Unit under test

Two changes in two files:

1. **`internal/orchestrator/registry.go`** — `StateClarifying` is added as a new
   lifecycle state constant (alongside the existing `StateQueued`, `StateDispatching`,
   `StateAwaitingApproval`, `StateDone`, `StateFailed`, `StateCancelled`). Its
   `String()` returns `"clarifying"`.

2. **`internal/cli/orchestrate.go`** (and/or `router.go`) — `routeCommand` (the
   control-loop router function) and/or `handleCommand` (the per-goal command
   dispatcher) are widened to route `MsgConfirm` to the goal's command mailbox.
   Currently the router delivers only `MsgInfo` and `MsgCancel` to mailboxes; this
   task adds `MsgConfirm` to the set of mailbox-routed message kinds. The actor's
   mailbox-drain loop (`drainPostHandle` or equivalent) is widened in the same commit
   to accept `MsgConfirm` (task 128 will add the handling body; here the routing must
   at minimum deliver the message without dropping it).

## Test cases

### TC-127-01: StateClarifying constant exists and String() returns "clarifying"

- **Requirement:** REQ-127-01
- **Setup:** read `orchestrator.StateClarifying`.
- **Expected:**
  - `orchestrator.StateClarifying.String() == "clarifying"`
  - `int(orchestrator.StateClarifying)` is distinct from all other state constants
    (`StateQueued`, `StateDispatching`, `StateAwaitingApproval`, `StateDone`,
    `StateFailed`, `StateCancelled`). Assert via dedup-map over all state values.
  - Existing state constants' `String()` values are unchanged:
    `StateQueued.String() == "queued"`, etc. Assert each one explicitly.

### TC-127-02: MsgConfirm is delivered to the goal mailbox by the router

- **Requirement:** REQ-127-02
- **Setup:** construct a test `commandMailboxes`, create a mailbox for `"goal-5"`,
  then call the routing function (or inline logic equivalent to `handleCommand`) with
  a `supervisor.Message{Kind: supervisor.MsgConfirm, GoalID: "goal-5"}`.
- **Expected:**
  - The message is delivered to the `"goal-5"` mailbox channel.
  - Reading from the mailbox channel returns a message with
    `Kind == supervisor.MsgConfirm` and `GoalID == "goal-5"`.
  - The routing function returns `true` (delivered) or equivalent success signal.
  - The router does NOT call the new-goal path for this message (it is not a
    `MsgNewGoal`).

### TC-127-03: MsgConfirm for an unknown goalID → graceful "no such goal" report

- **Requirement:** REQ-127-02
- **Setup:** construct an `envMessageSource` or call the routing function with
  `supervisor.Message{Kind: supervisor.MsgConfirm, GoalID: "unknown-99"}` when no
  mailbox exists for `"unknown-99"`.
- **Expected:**
  - The router does NOT panic.
  - The reporter receives a graceful message containing `"no such goal"` or
    `"unknown-99"` (the existing pattern for unknown goalIDs — assert
    `strings.Contains(reporter.Reported()[0], "unknown-99")`).
  - No mailbox is auto-created for `"unknown-99"`.
  - The control loop continues (no fatal error returned).

### TC-127-04: the actor's drain loop accepts MsgConfirm from the mailbox

- **Requirement:** REQ-127-03
- **Setup:** construct a `goalActor` in a test context where the mailbox is
  pre-seeded with a `MsgConfirm` message. Call the drain-loop function (or
  `drainPostHandle`) with a context that times out after a short duration.
- **Expected:**
  - The drain loop does NOT panic on receiving `MsgConfirm`.
  - The message is consumed from the channel (drain does not stall).
  - Since task 128 (the handling body) is not yet merged, the actor may drop/log the
    `MsgConfirm` with a "not yet in Clarifying state" note — that behavior is
    acceptable as long as it does not panic and does not stall the drain loop.
  - The drain loop exits cleanly when the context is cancelled.

## Post-implementation verification

- [ ] `go test -count=1 ./internal/orchestrator/... ./internal/cli/...` passes with
  all four TCs non-vacuous (hard assertions, not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] `docs/spec/behaviors.md` updated: `StateClarifying` appears in the goal
  lifecycle state table (after `StateQueued`, before `StateDispatching`) in the same
  commit
- [ ] The router's routing logic (the switch on `msg.Kind`) is extended: the case
  for `MsgInfo`/`MsgCancel` mailbox delivery now also covers `MsgConfirm`

## Test framework notes

- Go `testing`. Reuse `commandMailboxes` directly (it is an unexported type in
  `internal/cli`; tests in the same package can access it). For TC-127-04 use a fake
  or stub `goalActor` with a pre-seeded mailbox channel.
- TC-127-01 is an intra-package test in `internal/orchestrator`; TCs 127-02/03/04
  are in `internal/cli` (same package as the router).
- Depends on task 124 (`MsgConfirm`) and task 125 (CLI grammar precedent, though the
  routing is independent of `parseMessageLine`).
- L2/L3 only — routing and state-machine unit tests, no runtime surface.
  `make check` is the gate.
