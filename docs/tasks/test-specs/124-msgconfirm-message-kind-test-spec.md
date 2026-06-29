# Test Spec 124: MsgConfirm message kind (protocol only)

**Linked task:** [`docs/tasks/backlog/124-msgconfirm-message-kind.md`](../backlog/124-msgconfirm-message-kind.md)
**Written:** 2026-06-29
**ADR:** 056 — Conversational human-gated orchestrate front door (to be authored by the task 124 executor before implementation; extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-124-01 | TC-124-01         | ✅ |
| REQ-124-02 | TC-124-02         | ✅ |
| REQ-124-03 | TC-124-03         | ✅ |

## Test locations

All new tests land in `internal/supervisor/message_test.go`
(package `supervisor`, tests of the message kind enum and `String()` method).

- **TC-124-01** (MsgConfirm is distinct from all existing kinds):
  `TestMsgConfirmIsDistinctKind`
- **TC-124-02** (MsgConfirm.String() returns "confirm"):
  `TestMsgConfirmString`
- **TC-124-03** (iota order preserved — existing constants are unchanged):
  `TestMessageKindIotaOrderPreserved`

## Unit under test

`internal/supervisor/message.go` — the `MessageKind` enum and its `String()` method.
`MsgConfirm` is a new constant appended after `MsgCancel`, preserving the existing
iota values (0=MsgNewGoal, 1=MsgStatus, 2=MsgInfo, 3=MsgCancel) unchanged.

`docs/spec/interfaces.md` and `docs/spec/data-model.md` are updated in the same
commit (spec-with-code rule from AGENTS.md) but have no associated test cases — they
are manually verified via the spec diff in the review step.

## Test cases

### TC-124-01: MsgConfirm is a distinct, non-zero, non-conflicting kind

- **Requirement:** REQ-124-01
- **Setup:** read the numeric value of each known `MessageKind` constant.
- **Expected:**
  - `int(supervisor.MsgConfirm) != int(supervisor.MsgNewGoal)` — i.e. != 0
  - `int(supervisor.MsgConfirm) != int(supervisor.MsgStatus)` — i.e. != 1
  - `int(supervisor.MsgConfirm) != int(supervisor.MsgInfo)` — i.e. != 2
  - `int(supervisor.MsgConfirm) != int(supervisor.MsgCancel)` — i.e. != 3
  - All five iota values are distinct (no two constants share the same int).
  - Assert via a dedup check: put all five into a `map[int]string` and confirm map length == 5.

### TC-124-02: MsgConfirm.String() returns the canonical grammar token "confirm"

- **Requirement:** REQ-124-02
- **Setup:** call `supervisor.MsgConfirm.String()`.
- **Expected:**
  - Return value is exactly `"confirm"` (no leading/trailing whitespace, no uppercase).
  - Existing constants return unchanged strings: `MsgNewGoal.String() == "new-goal"`,
    `MsgStatus.String() == "status"`, `MsgInfo.String() == "info"`,
    `MsgCancel.String() == "cancel"`.
  - An out-of-range `MessageKind(99).String()` still returns `"unknown"` (the default
    branch is intact).

### TC-124-03: iota order preserved — existing constants are not renumbered

- **Requirement:** REQ-124-03
- **Setup:** directly compare each existing constant's integer value.
- **Expected:**
  - `int(supervisor.MsgNewGoal) == 0`
  - `int(supervisor.MsgStatus) == 1`
  - `int(supervisor.MsgInfo) == 2`
  - `int(supervisor.MsgCancel) == 3`
  - `int(supervisor.MsgConfirm) == 4`
  This is the load-bearing iota assertion: it proves the new constant was APPENDED
  (not inserted) so existing callers that switch on `MessageKind` by integer are not
  silently broken.

## Post-implementation verification

- [ ] `go test -count=1 ./internal/supervisor/...` passes with all three TCs
  non-vacuous (hard equality assertions, not smoke tests)
- [ ] `make check` passes (lint + build + all fitness functions green)
- [ ] `docs/spec/interfaces.md` updated: `MsgConfirm` added to the `MessageSource`
  interface block with its grammar description (`confirm <goalID>` →
  `MsgConfirm`) in the same commit
- [ ] `docs/spec/data-model.md` updated: `Message.Kind` description notes `MsgConfirm`
  and the `Clarifying` lifecycle state that will consume it (forward reference) in
  the same commit
- [ ] ADR 056 authored (see task file — the executor writes it before any implementation)

## Test framework notes

- Go `testing`. Pure enum/value tests — no fakes, no IO, no goroutines.
- TC-124-03's exact-integer assertions are the guard against accidentally inserting
  `MsgConfirm` before `MsgCancel` (which would silently renumber `MsgCancel` from 3
  to 4 and break any switch statements that have not yet been updated to include the
  new case). The test is a contract test, not an implementation detail test.
- L2/L3 only — no runtime surface (no CLI, no network). `make check` is the gate.
