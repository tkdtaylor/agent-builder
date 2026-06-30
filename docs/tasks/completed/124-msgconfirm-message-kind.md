# Task 124: MsgConfirm message kind (protocol only)

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** 056 — Conversational human-gated orchestrate front door (to be authored by this executor — see below)
**Test spec:** [124-msgconfirm-message-kind-test-spec.md](../test-specs/124-msgconfirm-message-kind-test-spec.md)

## Goal

Add `MsgConfirm` as a new `MessageKind` constant to `internal/supervisor/message.go`,
update its `String()` method to return `"confirm"`, and update the two affected spec
files (`docs/spec/interfaces.md`, `docs/spec/data-model.md`). Nothing reads or routes
`MsgConfirm` yet — this task is protocol-only; it creates the token that tasks
125–127 will route.

## ADR 056 must be authored first

**Before writing any implementation code**, the executor must author
`docs/architecture/decisions/056-conversational-human-gated-front-door.md`. ADR 056
records the five sub-decisions from the plan:
1. `StateClarifying` precedes `StatePlanning`; `Handle` splits into intake (`BeginGoal`) + plan-onward (`ConfirmAndPlan`).
2. `MsgConfirm` is a first-class, channel-abstract message kind (not a magic string).
3. `Clarifier` is a narrow seam — `HeuristicClarifier` v1 default, `LLMClarifier` opt-in.
4. `AGENT_BUILDER_REQUIRE_APPROVAL` (default true) is operator config orthogonal to policy risk.
5. All human touchpoints (clarifying questions, approval requests, needs-human escalations) flow over the Reporter; escalation moves off the file-backed `tasksource.StatusWriter`.
The ADR extends ADR 054 (control plane), ADR 055 (dispatch authorization), and ADR 046 (approval gate).
Commit the ADR under `docs: add ADR 056 — conversational human-gated front door` before the implementation commit.

## Context

The async control plane (`internal/supervisor/message.go`) currently defines four
`MessageKind` constants: `MsgNewGoal` (0), `MsgStatus` (1), `MsgInfo` (2), `MsgCancel` (3).
ADR 056 introduces `MsgConfirm` (4) as the token the user sends to signal that
clarification is complete and the orchestrator should proceed to planning. It is
channel-abstract: the CLI grammar maps `confirm <goalID>` to it (task 125); the
Telegram adapter derives it from `confirm`/`go`/`proceed` reply-to keywords (task 126).
This task creates the constant and nothing else.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-124-01 | `MsgConfirm` is a new `MessageKind` constant with a distinct, non-zero, non-conflicting iota value (specifically `4`, appended after `MsgCancel`=3). Existing constants are NOT renumbered. | must have |
| REQ-124-02 | `MessageKind.String()` is extended: `MsgConfirm.String()` returns `"confirm"`. All existing `String()` return values are unchanged. The default branch (`"unknown"`) is preserved. | must have |
| REQ-124-03 | `docs/spec/interfaces.md` and `docs/spec/data-model.md` are updated in the same commit to document `MsgConfirm` and its grammar entry. These spec changes are part of the same atomic commit as the code change (AGENTS.md spec-with-code rule). | must have |

## Acceptance criteria

1. `go test -count=1 ./internal/supervisor/...` passes; all three TCs non-vacuous
   (TC-124-01: distinct iota via dedup-map; TC-124-02: exact `String()` values;
   TC-124-03: exact integer values for all five constants).
2. `int(supervisor.MsgConfirm) == 4` — the specific iota value pinned in TC-124-03.
3. `supervisor.MsgConfirm.String() == "confirm"` — exact string pinned in TC-124-02.
4. All existing `MsgNewGoal.String()` / `MsgStatus.String()` / `MsgInfo.String()` /
   `MsgCancel.String()` values are unchanged — regression-checked in TC-124-02.
5. `docs/spec/interfaces.md` has the `MsgConfirm` entry in the `MessageSource`
   interface block (grammar: `confirm <goalID>` → `MsgConfirm, GoalID=<goalID>`).
6. `docs/spec/data-model.md` `Message.Kind` description notes `MsgConfirm`.
7. ADR 056 is committed before the implementation commit.
8. `make check` passes.
9. `git status` clean on commit.

## Files changed

- `internal/supervisor/message.go` — append `MsgConfirm` constant + extend `String()`.
- `docs/spec/interfaces.md` — add `MsgConfirm` to the grammar table.
- `docs/spec/data-model.md` — note `MsgConfirm` in the `Message.Kind` description.
- `docs/architecture/decisions/056-conversational-human-gated-front-door.md` (new) — ADR 056.
- `internal/supervisor/message_test.go` — three new test cases.

## Verification plan

**L2 (achievable now — no runtime surface):**
`go test -count=1 ./internal/supervisor/...` — all three TCs pass with exact-integer
and exact-string assertions. This is a pure enum/value change; no runtime behavior
is altered by this task.

`make check` — lint + build + all fitness functions green. The fitness checks that
could be affected: F-003 (supervisor isolation) and F-007 (envelope isolation) —
adding a constant to an existing enum with no new imports cannot disturb these.

**L5/L6:** not applicable to this task in isolation. The end-to-end runtime path
lands in task 128 (intake state machine) + the downstream tasks.

## Dependencies

None (first task in the ADR 056 series). Task 123 must be merged to `main` before
this task begins, per the plan's prerequisite note.

## Out of scope

- Routing `MsgConfirm` (task 127).
- CLI grammar for `confirm <goalID>` (task 125).
- Telegram derivation of `confirm`/`go`/`proceed` (task 126).
- Any change to `internal/cli/`, `internal/orchestrator/`, or
  `internal/channel/telegram/`.
