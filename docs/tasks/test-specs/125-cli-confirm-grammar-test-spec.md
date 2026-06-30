# Test Spec 125: CLI confirm grammar

**Linked task:** [`docs/tasks/backlog/125-cli-confirm-grammar.md`](../backlog/125-cli-confirm-grammar.md)
**Written:** 2026-06-29
**ADR:** 058 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-125-01 | TC-125-01         | ✅ |
| REQ-125-02 | TC-125-02, TC-125-03 | ✅ |
| REQ-125-03 | TC-125-04         | ✅ |

## Test locations

All new tests land in `internal/cli/router_test.go`
(package `cli`, tests of `parseMessageLine`).

- **TC-125-01** (`confirm <goalID>` → `MsgConfirm`):
  `TestParseConfirmLine`
- **TC-125-02** (`confirm` with no goalID → malformed-input error):
  `TestParseConfirmMissingGoalID`
- **TC-125-03** (`confirm` is NOT a bare new-goal):
  `TestParseConfirmNotNewGoal`
- **TC-125-04** (existing control verbs unaffected by the addition):
  `TestExistingVerbsUnchangedAfterConfirm`

## Unit under test

`internal/cli/router.go` — `parseMessageLine`. A new `"confirm"` case is added to the
switch statement. `confirm <goalID>` maps to `supervisor.Message{Kind:
supervisor.MsgConfirm, GoalID: fields[1]}`. `confirm` with no goalID wraps
`ErrMalformedInput` (consistent with `cancel`'s treatment). Bare `go`/`proceed` are
explicitly deferred (not handled in this task) — confirmed out of scope in the plan.

## Test cases

### TC-125-01: `confirm <goalID>` → MsgConfirm

- **Requirement:** REQ-125-01
- **Setup:** call `parseMessageLine("confirm goal-7", &seq)` where `seq = 0`.
- **Expected:**
  - `msg.Kind == supervisor.MsgConfirm`
  - `msg.GoalID == "goal-7"`
  - `msg.Goal == supervisor.Task{}` (zero value — confirm carries no Task payload)
  - `msg.Text == ""` (no text payload on a confirm)
  - `ok == true`, `err == nil`
  - `seq` is unchanged (auto-sequence incremented only for bare new-goal lines)

### TC-125-02: `confirm` with no goalID → ErrMalformedInput

- **Requirement:** REQ-125-02
- **Setup:** call `parseMessageLine("confirm", &seq)`.
- **Expected:**
  - `ok == false`
  - `errors.Is(err, cli.ErrMalformedInput) == true`
  - The error message includes the original line `"confirm"` and the expected grammar
    (`want: confirm <goalID>`), by exact substring match:
    `strings.Contains(err.Error(), "confirm")` and
    `strings.Contains(err.Error(), "goalID")`.
  - `seq` is unchanged.

### TC-125-03: `confirm` is not silently treated as a new-goal

- **Requirement:** REQ-125-02
- **Setup:** call `parseMessageLine("confirm", &seq)`.
- **Expected:**
  - The returned `msg.Kind` is NOT `supervisor.MsgNewGoal` — confirm is never
    downgraded to a bare goal line on a parse error.
  - `ok == false` (the caller does not deliver a message on parse failure).

### TC-125-04: existing verbs are unaffected

- **Requirement:** REQ-125-03
- **Setup:** call `parseMessageLine` for each of the four pre-existing verbs:
  `"status"`, `"status goal-3"`, `"info goal-3 some text"`, `"cancel goal-3"`,
  and a bare new-goal `"build the thing"`.
- **Expected (exact assertions for each):**
  - `"status"` → `Kind=MsgStatus, GoalID=""`
  - `"status goal-3"` → `Kind=MsgStatus, GoalID="goal-3"`
  - `"info goal-3 some text"` → `Kind=MsgInfo, GoalID="goal-3", Text="some text"`
  - `"cancel goal-3"` → `Kind=MsgCancel, GoalID="goal-3"`
  - `"build the thing"` → `Kind=MsgNewGoal` (auto-assigned ID), `msg.Goal.Spec=="build the thing"`
  None of these produce `MsgConfirm`. Assert exact Kind equality for each.

## Post-implementation verification

- [ ] `go test -count=1 ./internal/cli/...` passes with all four TCs non-vacuous
  (hard assertions on Kind, GoalID, ok, err — not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] `docs/spec/interfaces.md` updated: the stdin command grammar table has a row for
  `confirm <goalID>` → `MsgConfirm, GoalID=<goalID>` in the same commit
- [ ] `parseMessageLine` and the grammar docstring in `router.go` both document the
  new `confirm` verb (a missing-goalID confirm is rejected analogously to `cancel`)

## Test framework notes

- Go `testing`. Pure function tests on `parseMessageLine` — no goroutines, no IO.
- Depends on task 124 (`MsgConfirm` enum constant and `String()` method) being merged.
- Bare `go`/`proceed` are NOT tested here (deferred to task 126 Telegram derivation
  and a potential future task for CLI bare-confirm convenience). The test explicitly
  confirms that `"go"` and `"proceed"` parse as bare new-goals (MsgNewGoal), not as
  MsgConfirm, since this task does not add those aliases to the CLI grammar.
- L2/L3 only — no runtime surface. `make check` is the gate.
