# Task 171: route pending approvals over the channel; approve/deny resumes or aborts

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add `approve <goalID> <taskID>` / `deny <goalID> <taskID>` verbs to both channel
derivation sites (CLI grammar, Telegram reply-to), an `Orchestrator.ResumeApproval`
method that resumes or aborts the paused sub-goal a task 170 `PendingApproval`
names, and a timeout-based auto-escalation for an unresolved approval.

## Context

Task 170 persists a `runstore.PendingApproval` and pauses further sub-goal
dispatch; nothing surfaces it to an operator or resumes it yet. This task closes
that loop, reusing three ALREADY-BUILT patterns rather than inventing new ones:

1. **Grammar/derivation:** `confirm <goalID>`/`cancel <goalID>` (tasks 125/126)
   already establish the exact two-site pattern (`internal/cli/router.go`'s
   `parseMessageLine` + `internal/channel/telegram/adapter.go`'s
   `deriveMessage`) a channel-abstract verb needs; task 126's own task file
   documents both sites as independently required. `approve`/`deny` mirror that
   pattern symmetrically.
2. **CLI operator transport:** `examples/agent-cli`'s `runSend`
   (`examples/agent-cli/main.go:131-`) already sends arbitrary command text; no
   new agent-cli subcommand is needed, an operator runs `agent-cli send ...
   "approve goal-7 task-3"`.
3. **Timeout escalation:** the clarifier seam (task 128,
   `internal/orchestrator/clarifier.go`) already has a linger/timeout/escalate
   state machine; this task's approval timeout mirrors it rather than inventing
   a second one.

**This task deliberately spans three modules** (`internal/cli`,
`internal/channel/telegram`, `internal/orchestrator`) because it is the
consciously-merged twin of what tasks 125/126 did separately: `approve`/`deny`
are small, symmetric, mechanical repetitions of an established pattern, not
three independent designs. If executing this in one session proves too large,
split it at execution time into "171a: grammar + derivation" and "171b:
ResumeApproval + timeout + goalActor wiring" using the same test spec, do not
invent new task IDs, the test spec's TC numbering already separates the two
halves cleanly (TC-171-01..04 vs. TC-171-05..08).

**Reference:**
- `internal/cli/router.go` (`parseMessageLine`, the grammar edit site)
- `internal/channel/telegram/adapter.go` (`deriveMessage`, the derivation edit site)
- `internal/cli/goal_actor.go:237-273` (`applyConfirm`, the wiring pattern to mirror)
- `internal/orchestrator/clarifier.go` (the timeout/escalation pattern to mirror)
- `docs/tasks/completed/125-cli-confirm-grammar.md`,
  `docs/tasks/completed/126-telegram-confirm-derivation.md` (the twin precedent)
- Task 170 (`PendingApproval`, `Record.Pending`, `Record.Status`)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-171-01 | `parseMessageLine` recognizes `approve <goalID> <taskID>`/`deny <goalID> <taskID>`, mirroring `confirm`'s exact validation shape. | must have |
| REQ-171-02 | Telegram `deriveMessage` recognizes `approve <taskID>`/`deny <taskID>` as reply-to commands, mirroring `confirm`'s exact derivation. | must have |
| REQ-171-03 | `Orchestrator.ResumeApproval(ctx, goalID, taskID string, approved bool) error` resumes (re-dispatches) or aborts (marks needs-human, finalizes if last) the named pending sub-goal. | must have |
| REQ-171-04 | An unresolved pending approval past `AGENT_BUILDER_APPROVAL_TIMEOUT` auto-escalates over `Reporter` exactly once. | must have |
| REQ-171-05 | `goalActor` routes `MsgApprove`/`MsgDeny` to `ResumeApproval`. | must have |
| REQ-171-06 | Pre-existing `confirm`/`cancel`/`status`/`info` grammar and derivation unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/171-approval-routing-and-resume-test-spec.md` exists (written first)
- [x] Task 170 merged
- [x] Task 125/126/128 merged (the three patterns this task mirrors)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/supervisor/message.go`: add `MsgApprove`, `MsgDeny` to the
   `MessageKind` enum (mirroring `MsgConfirm`'s addition pattern exactly,
   `message.go:17-22`); add a `TaskID string` field to `Message`
   (`message.go:47-`) if not already present, used only by these two new kinds
   (zero value for all others, unaffected).
2. `internal/cli/router.go`, `parseMessageLine`: add `case` handling for
   `"approve"`/`"deny"` prefixes, requiring exactly two space-delimited tokens
   after the verb (goalID, taskID), returning `ErrMalformedInput` when either is
   missing, mirroring `"cancel"`'s existing two-token-required shape (adapted
   from `confirm`'s one-token shape).
3. `internal/channel/telegram/adapter.go`, `deriveMessage`: add `"approve"`/`"deny"`
   to the reply-to command keyword set, threading `goalIDCache` exactly as
   `"confirm"`/`"go"`/`"proceed"` already do, and parsing the remaining text
   after the verb as the `taskID`. A standalone (non-reply) `"approve"`/`"deny"`
   falls through to the existing `MsgNewGoal` default, matching `confirm`'s
   documented fallback.
4. `internal/orchestrator/orchestrator.go`, new method:
   ```go
   func (o *Orchestrator) ResumeApproval(ctx context.Context, goalID, taskID string, approved bool) error
   ```
   Loads the `Record`, finds the `PendingApproval` matching `taskID`, removes it
   from `Record.Pending`. On `approved`, re-dispatches that ONE sub-goal (reuse
   `dispatchOne`, looking up the `SubGoal` from the unmarshaled `Record.Plan`).
   On `!approved`, marks that sub-goal's `AttemptState.Status =
   runstore.StatusNeedsHuman` without dispatching. Either way, if
   `Record.Pending` is now empty and no other sub-goal is still
   running/pending, finalize the plan (reuse whatever finalization/reporting
   path `dispatchPlan` already calls on natural completion) and set
   `Record.Status` to the appropriate terminal value; otherwise set it back to
   `StatusRunning`.
5. `internal/orchestrator`, timeout sweep: extend the goal actor's existing
   `sweep` method (`internal/cli/goal_actor.go:211-226`) or an orchestrator-side
   equivalent, mirroring the clarifier's timeout check
   (`internal/orchestrator/clarifier.go`), to scan `Record.Pending` entries
   older than `AGENT_BUILDER_APPROVAL_TIMEOUT` (new env var, default `1h`) and
   call `Reporter.Report` once per timed-out entry (track an "already escalated"
   marker on the `PendingApproval`, mirroring the clarifier's own once-only
   guarantee, to avoid re-escalating on every sweep tick).
6. `internal/cli/goal_actor.go`: add `applyApproval`, mirroring `applyConfirm`
   (`goal_actor.go:237-273`) exactly, calling `Orchestrator.ResumeApproval`.
   Wire it from `handleCommand`'s dispatch table alongside `MsgConfirm`/`MsgCancel`.
7. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-171-01] TC-171-01/02: grammar recognition + malformed-input rejection.
- [ ] [REQ-171-02] TC-171-03/04: Telegram derivation, reply-to and standalone cases.
- [ ] [REQ-171-03] TC-171-05/06: `ResumeApproval` resumes on approve, aborts+finalizes on deny.
- [ ] [REQ-171-04] TC-171-07: timeout auto-escalates exactly once.
- [ ] [REQ-171-05] TC-171-08: `goalActor` routes to `ResumeApproval`.
- [ ] [REQ-171-06] TC-171-09: pre-existing grammar/derivation unaffected.
- [ ] TC-171-10: `go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/orchestrator/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L2/L3, mirroring tasks 125/126/128's own
  verification level (grammar/derivation/state-machine additions with existing
  test-harness coverage, no new runtime binary surface).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/orchestrator/... -run TestTC171
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md`: stdin command grammar table gains `approve
  <goalID> <taskID>`/`deny <goalID> <taskID>`; the Telegram derivation table
  gains the matching reply-to entries.
- `docs/spec/configuration.md`: new `AGENT_BUILDER_APPROVAL_TIMEOUT` row.
- `docs/spec/behaviors.md`: new behavior entry for approve/deny routing and
  timeout escalation.

## Out of scope

- Any change to `confirm`/`cancel`/`status`/`info` grammar or derivation.
- Any change to the plan-level `pauseForApproval`/`Resume`/`Approval` flow.
- `examples/agent-cli` code changes.

## Dependencies

- **Blocks on:** task 170, 125, 126, 128 (all already merged or in this batch).
- **Blocks:** none.
