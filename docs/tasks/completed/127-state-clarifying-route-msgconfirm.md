# Task 127: StateClarifying registry state + route MsgConfirm

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** completed
**ADR:** 056 — Conversational human-gated orchestrate front door
**Test spec:** [127-state-clarifying-route-msgconfirm-test-spec.md](../test-specs/127-state-clarifying-route-msgconfirm-test-spec.md)

## Goal

Add `StateClarifying` as a new goal lifecycle state to `internal/orchestrator/registry.go`,
and widen `routeCommand` / `handleCommand` in `internal/cli/orchestrate.go` (and/or
`goal_actor.go`) to deliver `MsgConfirm` to the addressed goal's command mailbox —
using the same pattern as `MsgInfo` and `MsgCancel`. Update `docs/spec/behaviors.md`
with the new state in the lifecycle table.

## Context

The goal lifecycle currently has: `StateQueued → StateDispatching → StateDone/Failed/Cancelled`,
with `StateAwaitingApproval` as a pause state. ADR 056 inserts `StateClarifying`
between `StateQueued` and `StateDispatching` (the intake phase runs before planning).
The control-loop router already delivers `MsgInfo` and `MsgCancel` to goal mailboxes;
this task extends it to cover `MsgConfirm` with the same routing logic and "no such
goal" graceful fallback.

This task does NOT implement the clarification body (that is task 128). It only:
1. Adds the `StateClarifying` constant and its `String()`.
2. Widens the router to deliver `MsgConfirm` to the mailbox.
3. Widens the actor's drain loop to ACCEPT `MsgConfirm` from the mailbox without
   panicking (handling body is task 128's concern).

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-127-01 | `orchestrator.StateClarifying` is a new state constant with distinct integer value and `String() == "clarifying"`. All existing state constants' `String()` values are unchanged. | must have |
| REQ-127-02 | `routeCommand` / `handleCommand` routes `MsgConfirm` to the goal's command mailbox via the existing `commandMailboxes.deliver` path. A `MsgConfirm` for an unknown goalID produces a graceful "no such goal" report (identical to the behavior for unknown-goalID `info`/`cancel`). | must have |
| REQ-127-03 | The actor's mailbox drain loop accepts `MsgConfirm` without panicking. It may log a "not yet in Clarifying state" note or no-op if task 128 is not yet merged; it must not stall and must not panic. | must have |

## Acceptance criteria

1. `go test -count=1 ./internal/orchestrator/... ./internal/cli/...` passes; all
   four TCs non-vacuous.
2. `orchestrator.StateClarifying.String() == "clarifying"` and its integer value is
   distinct from all other state constants — pinned in TC-127-01.
3. A `MsgConfirm` for a known goalID reaches the mailbox channel (TC-127-02).
4. A `MsgConfirm` for an unknown goalID produces a reporter "no such goal" line
   (TC-127-03).
5. The drain loop does not panic on receiving `MsgConfirm` (TC-127-04).
6. `docs/spec/behaviors.md` lifecycle state table updated with `StateClarifying`
   in the same commit.
7. `make check` passes.
8. `git status` clean on commit.

## Files changed

- `internal/orchestrator/registry.go` — append `StateClarifying` constant + extend `String()`.
- `internal/cli/orchestrate.go` (or `router.go`) — widen router/handler to route `MsgConfirm` to mailboxes.
- `internal/cli/goal_actor.go` — widen drain loop to accept `MsgConfirm` without panicking.
- `internal/orchestrator/registry_test.go` — TC-127-01.
- `internal/cli/orchestrate_test.go` (or `router_test.go`) — TC-127-02, TC-127-03, TC-127-04.
- `docs/spec/behaviors.md` — lifecycle state table updated.

## Verification plan

**L2 (achievable now — routing/state unit tests):**
`go test -count=1 ./internal/orchestrator/... ./internal/cli/...` — all four TCs pass.

`make check` — lint + build + fitness green.

**L5/L6:** routing without the intake body (task 128) cannot be fully exercised live.
The end-to-end run for this task's routing is implicitly proven by task 128's L5
conversation assertion (TC-128-07), since that test requires routing to be correct.

## Dependencies

- Task 124 (`MsgConfirm` constant).
- Task 125 or 126 (grammar — technically independent; but the routing is only useful
  if a message-source can produce `MsgConfirm`).
- Task 123 merged to `main` (per plan prerequisite).

## Out of scope

- The clarification body inside `BeginGoal` + `ConfirmAndPlan` (task 128).
- The `Clarifier` interface (task 128).
- Approval-default (task 129).
- Escalation over the channel (task 130).
