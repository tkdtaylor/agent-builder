# Task 125: CLI confirm grammar

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** 058 — Conversational human-gated orchestrate front door
**Test spec:** [125-cli-confirm-grammar-test-spec.md](../test-specs/125-cli-confirm-grammar-test-spec.md)

## Goal

Extend `parseMessageLine` in `internal/cli/router.go` to recognize `confirm <goalID>`
as a control verb, mapping it to `supervisor.MsgConfirm`. Update `docs/spec/interfaces.md`
to document the new grammar entry. Bare `go`/`proceed` aliases are explicitly
deferred (not in scope for this task).

## Context

`parseMessageLine` currently handles four control verbs: `status`, `info`, `cancel`,
and (implicitly) bare lines as `MsgNewGoal`. ADR 058 adds `confirm <goalID>` as the
fifth verb — the user signals that clarification is complete and planning should
proceed. The implementation mirrors the `cancel <goalID>` case: both require exactly
one argument (goalID); both return `ErrMalformedInput` when the argument is missing.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-125-01 | `parseMessageLine("confirm <goalID>", seq)` returns `Message{Kind: MsgConfirm, GoalID: goalID}` with `ok=true, err=nil`. The goalID is the first space-delimited token after `confirm`. | must have |
| REQ-125-02 | `parseMessageLine("confirm", seq)` (no goalID) returns `ok=false` with `errors.Is(err, ErrMalformedInput) == true`. The error includes the original line and the expected grammar. `confirm` is NEVER silently treated as a new-goal. | must have |
| REQ-125-03 | All four pre-existing verbs (`status`, `info`, `cancel`, bare new-goal) continue to produce exactly the same `Message` values as before this change. Verified by a regression test. | must have |

## Acceptance criteria

1. `go test -count=1 ./internal/cli/...` passes; all four TCs non-vacuous
   (TC-125-01: exact Kind/GoalID/ok/err; TC-125-02: ErrMalformedInput wrapping;
   TC-125-03: confirm not downgraded to MsgNewGoal; TC-125-04: existing verbs unchanged).
2. `parseMessageLine("confirm goal-7", &seq).Kind == supervisor.MsgConfirm` and
   `.GoalID == "goal-7"` — pinned in TC-125-01.
3. `errors.Is(parseMessageLine("confirm", &seq) error, cli.ErrMalformedInput) == true` —
   pinned in TC-125-02.
4. `docs/spec/interfaces.md` stdin command grammar table updated with
   `confirm <goalID>` → `MsgConfirm, GoalID=<goalID>` in the same commit.
5. The `router.go` grammar docstring is updated to include `confirm <goalID>`.
6. `make check` passes.
7. `git status` clean on commit.

## Files changed

- `internal/cli/router.go` — add `"confirm"` case to `parseMessageLine` switch.
- `internal/cli/router_test.go` — four new test cases.
- `docs/spec/interfaces.md` — grammar table updated.

## Verification plan

**L2 (achievable now — no runtime surface):**
`go test -count=1 ./internal/cli/...` — all four TCs pass. Pure function tests on
`parseMessageLine`; no goroutines, no IO.

`make check` — lint + build + fitness green.

**L5/L6:** not applicable to this task in isolation. The end-to-end path requires
task 127 (routing) and task 128 (intake state machine) to be merged first.

## Dependencies

- Task 124 (`MsgConfirm` constant in `internal/supervisor/message.go`).
- Task 123 merged to `main` (per plan prerequisite).

## Out of scope

- Bare `go`/`proceed` aliases — these are explicitly deferred.
- Routing `MsgConfirm` to the goal mailbox (task 127).
- The Telegram derivation path (task 126, independent of this change).
- Changes to `internal/orchestrator/`, `internal/channel/telegram/`, or any
  file outside `internal/cli/router.go` and the test file.
