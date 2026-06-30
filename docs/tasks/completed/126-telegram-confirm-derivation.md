# Task 126: Telegram confirm derivation

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** 056 — Conversational human-gated orchestrate front door
**Test spec:** [126-telegram-confirm-derivation-test-spec.md](../test-specs/126-telegram-confirm-derivation-test-spec.md)

## Goal

Extend the Telegram adapter's `deriveMessage` logic in
`internal/channel/telegram/adapter.go` to recognize `confirm`, `go`, and `proceed`
as reply-to command keywords, deriving `supervisor.MsgConfirm` with the goalID
threaded from the `goalIDCache`. A message with one of these tokens but NO reply-to
(or an unknown reply-to cache entry) is treated as a `MsgNewGoal` (the full text is
the goal spec). Update `docs/spec/interfaces.md` with the derivation table entry.

## Context

The Telegram adapter already threads `info` and `cancel` reply-to commands to goal
mailboxes via the `goalIDCache` (task 117). ADR 056 adds the confirm tokens:
- `confirm` — the explicit channel-neutral confirm keyword.
- `go` — convenience alias for Telegram (natural UX).
- `proceed` — second alias.
All three map to `MsgConfirm` when sent as replies to the original new-goal message.
A standalone `go` or `confirm` (not a reply) is still a `MsgNewGoal` — the goalID
threading is the discriminator, not the keyword alone.

This task is independent of task 125 (CLI grammar): the Telegram derivation code
is entirely separate from `parseMessageLine`. Both are required to close the
channel-abstract confirm contract from ADR 056.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-126-01 | In a reply-to context, `confirm` / `go` / `proceed` as the plaintext keyword (after envelope-verify + armor) derive `Message{Kind: MsgConfirm, GoalID: <cached goalID>}`. The goalID is looked up from `goalIDCache` keyed by the Telegram `ReplyToMessage.MessageID` cast to string — exactly the same threading as `info`/`cancel`. | must have |
| REQ-126-02 | A `confirm`/`go`/`proceed` message WITHOUT a reply-to (or with a reply-to whose original message ID is not in `goalIDCache`) is treated as `MsgNewGoal` — the text is the goal spec. This is consistent with the existing behavior for unknown reply-to verbs. | must have |
| REQ-126-03 | Existing kind derivation for `MsgNewGoal`, `MsgStatus`, `MsgInfo`, `MsgCancel` is unchanged after this addition. Verified by a regression test. | must have |

## Acceptance criteria

1. `go test -count=1 ./internal/channel/telegram/...` passes; all four TCs
   non-vacuous (TC-126-01: exact Kind/GoalID for `confirm` reply-to;
   TC-126-02: `go`/`proceed` reply-to; TC-126-03: no-reply-to → MsgNewGoal;
   TC-126-04: existing kinds unchanged).
2. `msg.Kind == supervisor.MsgConfirm` and `msg.GoalID == "goal-7"` for a `"confirm"`
   reply-to with `goalIDCache["42"] = "goal-7"` — pinned in TC-126-01.
3. A `"confirm"` without reply-to produces `msg.Kind == supervisor.MsgNewGoal` —
   pinned in TC-126-03.
4. `docs/spec/interfaces.md` Telegram adapter derivation table updated: three new rows
   for `confirm`/`go`/`proceed` reply-to → `MsgConfirm` in the same commit.
5. `make check` passes.
6. `git status` clean on commit.

## Files changed

- `internal/channel/telegram/adapter.go` — extend `deriveMessage` (or equivalent
  inline derivation) with the three confirm keywords.
- `internal/channel/telegram/adapter_test.go` — four new test cases.
- `docs/spec/interfaces.md` — Telegram derivation table updated.

## Design note

The `goalIDCache` key for reply-to threading is `strconv.Itoa(update.Message.ReplyToMessage.MessageID)`
(the existing pattern established in task 117). The `confirm`/`go`/`proceed` recognition
is case-insensitive (lowercase comparison after `strings.ToLower`) to match natural
Telegram UX — users may type `Go` or `PROCEED`. Document this in the implementation
comment.

## Verification plan

**L2 (achievable now — unit tests only, no live Telegram API):**
`go test -count=1 ./internal/channel/telegram/...` — all four TCs pass.
Envelope crypto is bypassed in unit tests using the existing fake-update pattern.

`make check` — lint + build + fitness green.

**L5/L6:** the L6 Telegram round-trip (`confirm` → `MsgConfirm` → planning → approval)
is the acceptance path for the full ADR 056 feature. It requires tasks 127 + 128 to
be merged and a live Telegram bot configured with the operator keys.

## Dependencies

- Task 124 (`MsgConfirm` constant).
- Task 123 merged to `main` (per plan prerequisite).
- Logically parallel to task 125 (both can start after 124; neither depends on the other).

## Out of scope

- CLI grammar for `confirm <goalID>` (task 125, independent).
- Routing `MsgConfirm` to the goal mailbox (task 127).
- Any change to `internal/orchestrator/`, `internal/cli/`, or `internal/supervisor/`.
