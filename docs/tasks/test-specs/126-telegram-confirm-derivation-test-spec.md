# Test Spec 126: Telegram confirm derivation

**Linked task:** [`docs/tasks/backlog/126-telegram-confirm-derivation.md`](../backlog/126-telegram-confirm-derivation.md)
**Written:** 2026-06-29
**ADR:** 056 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-126-01 | TC-126-01, TC-126-02 | ✅ |
| REQ-126-02 | TC-126-03         | ✅ |
| REQ-126-03 | TC-126-04         | ✅ |

## Test locations

All new tests land in `internal/channel/telegram/adapter_test.go`
(package `telegram`, testing the `deriveMessage` function or an equivalent
internal derivation helper).

- **TC-126-01** (`confirm` as reply-to → `MsgConfirm` threaded to the cached goalID):
  `TestDeriveConfirmReplyToThreadsGoalID`
- **TC-126-02** (`go` and `proceed` as reply-to → `MsgConfirm` threaded):
  `TestDeriveGoAndProceedReplyToProduceMsgConfirm`
- **TC-126-03** (no reply-to → `confirm`/`go`/`proceed` is a new-goal, not MsgConfirm):
  `TestConfirmWithoutReplyToIsNewGoal`
- **TC-126-04** (existing kind derivation unaffected — a `cancel` reply-to still produces MsgCancel):
  `TestExistingKindDerivationUnchangedAfterConfirm`

## Unit under test

`internal/channel/telegram/adapter.go` — the `deriveMessage` function (or the
equivalent inline derivation block inside `Next()`). The addition recognizes the
confirm tokens (`"confirm"`, `"go"`, `"proceed"`) as reply-to commands and derives
`supervisor.MsgConfirm` with the GoalID threaded from the `goalIDCache` (keyed by the
original new-goal message ID). A message with one of these tokens but NO reply-to
(i.e. not a reply to a known goal message) is treated as a `MsgNewGoal` (the full
text is the goal spec) — the same behavior as an unknown reply-to verb.

Note: the `goalIDCache` key for a reply-to is the Telegram `ReplyToMessageID` (the
original new-goal message's Telegram message ID, cast to string), following the
existing pattern for `cancel` and `info` reply-to threading established in task 117.

## Test cases

### TC-126-01: `confirm` as a reply-to → MsgConfirm, goalID from cache

- **Requirement:** REQ-126-01
- **Setup:** construct a fake Telegram update with:
  - `Message.Text` = (an envelope decrypting to) `"confirm"` (the keyword alone; the
    goalID is carried by the reply-to context, not the text).
  - `Message.ReplyToMessage.MessageID` = the original new-goal Telegram message ID
    (e.g. `42`).
  - Seed the adapter's `goalIDCache` with `"42" → "goal-7"`.
  Call `deriveMessage` (or equivalent) with this update after envelope-verify + armor pass.
- **Expected:**
  - `msg.Kind == supervisor.MsgConfirm`
  - `msg.GoalID == "goal-7"` (threaded from the cache — not the Telegram message ID)
  - `msg.Text == ""`
  - `ok == true`, `err == nil`

### TC-126-02: `go` and `proceed` as reply-to → MsgConfirm, goalID from cache

- **Requirement:** REQ-126-01
- **Setup:** same structure as TC-126-01, but test both `"go"` and `"proceed"` as the
  plaintext keyword.
  - For `"go"`: update with `Text = "go"` (or envelope thereof), `ReplyToMessage.MessageID = 42`,
    `goalIDCache["42"] = "goal-7"`. Expected: `Kind=MsgConfirm, GoalID="goal-7"`.
  - For `"proceed"`: update with `Text = "proceed"`, same cache entry. Expected: same.
- **Expected (for each):**
  - `msg.Kind == supervisor.MsgConfirm`
  - `msg.GoalID == "goal-7"`
  - `ok == true`, `err == nil`
  Assert both `"go"` and `"proceed"` produce `MsgConfirm`, not `MsgNewGoal`.

### TC-126-03: `confirm`/`go`/`proceed` WITHOUT reply-to → MsgNewGoal (not MsgConfirm)

- **Requirement:** REQ-126-02
- **Setup:** construct a fake Telegram update with `Message.Text` = `"confirm"`
  (or `"go"` / `"proceed"`) but NO `ReplyToMessage` (nil or zero MessageID).
  `goalIDCache` is empty.
- **Expected:**
  - `msg.Kind == supervisor.MsgNewGoal` (the text is treated as a new goal spec).
  - `msg.Goal.Spec` equals the plaintext keyword (`"confirm"` / `"go"` / `"proceed"`).
  - `msg.Kind != supervisor.MsgConfirm` — confirm derivation requires the reply-to
    thread; a free-standing keyword is a new goal.
  Assert all three keywords produce `MsgNewGoal` in this path.

### TC-126-04: existing kind derivation unchanged — `cancel` reply-to still produces MsgCancel

- **Requirement:** REQ-126-03
- **Setup:** construct a fake Telegram update with `Text = "cancel"` and
  `ReplyToMessage.MessageID = 42`; `goalIDCache["42"] = "goal-5"`.
- **Expected:**
  - `msg.Kind == supervisor.MsgCancel`
  - `msg.GoalID == "goal-5"`
  - `msg.Kind != supervisor.MsgConfirm`
  Also confirm that a `"status"` update (no reply-to) still produces `MsgStatus`.
  These assertions are the non-regression contract: the `confirm`/`go`/`proceed`
  additions must not disturb the derivation logic for the four pre-existing kinds.

## Post-implementation verification

- [ ] `go test -count=1 ./internal/channel/telegram/...` passes with all four TCs
  non-vacuous (hard Kind/GoalID equality, not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] `docs/spec/interfaces.md` updated: the Telegram adapter's derivation table gains
  a row for `confirm`/`go`/`proceed` → `MsgConfirm` in the same commit
- [ ] The derivation logic is guarded: `confirm`/`go`/`proceed` keywords that have a
  reply-to with an UNKNOWN cache entry (the original message is not in `goalIDCache`)
  fall back to `MsgNewGoal` (same as an unknown verb for `info`/`cancel`). If this
  behavior differs, TC-126-01 must include a negative assertion for a missing cache key.

## Test framework notes

- Go `testing`. Use the existing fake-update construction pattern from
  `adapter_test.go` (task 117 established this pattern). The envelope crypto is
  bypassed in unit tests by stubbing the `VerifyAndOpen` path — follow the existing
  test pattern where the adapter's plaintext extraction is the unit boundary.
- Depends on task 124 (`MsgConfirm` constant) and task 125 (the confirm grammar
  exists as precedent, though Telegram derivation is independent of `parseMessageLine`).
- `goalIDCache` access: the cache is `mu`-guarded; in unit tests the adapter is
  constructed and the cache pre-seeded directly (white-box) since there is no exported
  setter. Follow the pattern established in the task 117 adapter tests.
- L2/L3 only — no live Telegram API, no real envelope crypto in unit tests.
  `make check` is the gate.
