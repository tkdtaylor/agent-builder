# Task 157: Telegram `Adapter.Next` no longer terminates the control plane on an empty/rejected poll

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Fix `telegram.Adapter.Next()` so it returns `ok=false` ONLY on genuine terminal
shutdown, never merely because one `getUpdates` poll batch was empty or every update
in it was rejected. Wire the control loop's top-level context into the adapter at
construction so it has a real shutdown signal to observe.

## Context

**Root cause (full-project review, verified 2026-07-02):**
`Adapter.Next()` (`internal/channel/telegram/adapter.go:197-345`) polls `getUpdates()`
once per call and returns `(supervisor.Message{}, false, nil)` at line 344 whenever
that single batch yields no deliverable message — whether the batch was genuinely
empty (idle poll) or every update in it was rejected (bad envelope, unapproved
sender, armor block, oversized message). The control loop
(`internal/cli/orchestrate.go:685-700`) treats `ok=false` as SOURCE EXHAUSTED and
`break`s, terminating `orchestrate` entirely:

```go
msg, ok, err := oc.source.Next()
...
if !ok {
    break   // line 699
}
```

This is a **remote unauthenticated control-plane DoS**: any Telegram account can send
ONE message that fails envelope verification (no sender-ID approval required for the
message to be REJECTED — a malformed envelope rejects before any sender check) and
the entire orchestrate process exits. It is also a **non-durability bug** with zero
attacker involvement: a single idle 30-second poll with no updates at all ALSO returns
`ok=false`, so the control plane cannot durably run unattended against a live bot.

Contrast the precedent `envMessageSource.Next` (`internal/cli/router.go:96-119`),
which the loop's `ok=false` contract was written against: it blocks on
`s.scanner.Scan()` in an internal `for` loop and returns `ok=false` ONLY at genuine
stdin EOF or a hard scanner error — never on a skippable blank line. `Adapter.Next`
never adopted that internal-retry shape.

**The fix:** `Adapter.Next()` re-polls INTERNALLY — a `for` loop around the existing
per-batch scan — so it keeps polling (respecting a shutdown signal) until either a
deliverable message is found or genuine shutdown fires. This requires giving `Adapter`
a shutdown-observing seam: a `context.Context` set at construction (a new `Config`
field, defaulting to `context.Background()` when omitted so no caller regresses to a
nil-context panic), wired from the SAME top-level context `RunControlLoop` already
holds, threaded through `assembleTelegramInbound`'s existing `Adapter` construction. A
hard `getUpdates` transport failure is retried internally with a bounded backoff
rather than immediately propagating as a fatal `Next()` error, UNLESS shutdown fires
first.

**Reference:**
- `internal/channel/telegram/adapter.go:197-345` (`Next`)
- `internal/cli/orchestrate.go:685-720` (the control loop's `ok=false` handling)
- `internal/cli/router.go:96-119` (`envMessageSource.Next` — the precedent contract)
- `docs/tasks/completed/147-answer-route-terminates-on-source-eof.md` (the analogous
  finite-vs-live-source fix on the OUTBOUND/linger side; this task is the INBOUND
  counterpart)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-157-01 | `Adapter` gains a shutdown-observing `context.Context` seam set at construction (a new `Config` field), defaulting to `context.Background()` when omitted. | must have |
| REQ-157-02 | `Next()` re-polls internally on an empty batch instead of returning `ok=false`; the caller never observes an intermediate false `ok` before a real message arrives. | must have |
| REQ-157-03 | `Next()` re-polls internally when a batch's every update is rejected instead of returning `ok=false`; existing per-rejection audit events are unaffected. | must have |
| REQ-157-04 | `Next()` returns `ok=false` (err=nil) ONLY when the adapter's shutdown context fires, within a bounded time of the cancel. | must have |
| REQ-157-05 | A hard `getUpdates` transport failure is retried internally with a bounded backoff rather than immediately propagating as a fatal `Next()` error, still respecting the shutdown context. | must have |
| REQ-157-06 | End-to-end through the real `internal/cli` control loop: an idle poll followed by a real message delivers that message without the loop having terminated in between. | must have |
| REQ-157-07 | End-to-end: an all-rejected batch does not terminate the control loop; a subsequent valid message still arrives (closes the DoS finding). | must have |
| REQ-157-08 | End-to-end: cancelling the control loop's top-level context still cleanly terminates both `Adapter.Next` and the control loop. | must have |
| REQ-157-09 | Pre-existing `internal/channel/telegram` and `internal/cli` suites (tasks 080/097/098/151/152/153) continue to pass unchanged in behavior. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/157-telegram-adapter-no-terminate-on-idle-test-spec.md` exists (written first)
- [x] Task 147 merged (precedent: finite-source-terminates / live-source-lingers contract)
- [x] Task 153 merged (`assembleTelegramInbound` full assembly path this task's E2E cases build on)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-157-01] TC-157-01: `Adapter` accepts and defaults the shutdown context correctly.
- [ ] [REQ-157-02] TC-157-02: an empty poll batch does not surface as `ok=false`; `Next()` returns the eventual real message.
- [ ] [REQ-157-03] TC-157-03: an all-rejected batch does not surface as `ok=false`; the next accepted message is returned.
- [ ] [REQ-157-04] TC-157-04: `Next()` returns `ok=false` only when the shutdown context fires, within a bounded time.
- [ ] [REQ-157-05] TC-157-05: a transient transport failure is retried internally, not immediately fatal.
- [ ] [REQ-157-06] TC-157-06: the real control loop survives an idle poll and later delivers a message.
- [ ] [REQ-157-07] TC-157-07: the real control loop survives an all-rejected batch (the DoS scenario) and later delivers a message.
- [ ] [REQ-157-08] TC-157-08: the real control loop still cleanly terminates on genuine shutdown.
- [ ] [REQ-157-09] TC-157-09: `go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L5 — a real `internal/cli` control-loop harness driven
  against a scripted stub Telegram server proves the fix holds through the actual
  `orchestrate` machinery. A live bot token (L6) adds no additional confidence beyond
  what the stub-server L5 harness already exercises for this specific fix.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... -run TestTC157
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... -run TestTC157
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** run `orchestrate` against a real Telegram bot,
  send one malformed/unapproved message, confirm the process survives and a follow-up
  valid command still routes.

## Spec/doc footprint (update in the feat commit)

- `docs/spec/behaviors.md` — the Telegram inbound / `Adapter.Next` behavior entry
  (grep for "getUpdates" or the B-0xx entry covering task 080/097) gains a sentence:
  "`Next()` returns `ok=false` only on genuine adapter shutdown; an idle poll or a
  fully-rejected batch re-polls internally and never terminates the control plane
  (task 157)."
- `docs/spec/interfaces.md` — the `telegram.Config`/`NewAdapter` entry documents the
  new shutdown-context field.
- `docs/spec/configuration.md` — no new env var; note (if the implementation adds a
  configurable poll-retry backoff) any new `AGENT_BUILDER_TELEGRAM_*` var introduced.

## Out of scope

- Changing the `supervisor.MessageSource` interface itself.
- Armor guard wiring (task 158) and pairing-owner seeding (task 159).
- The wall-clock timeout / cancel-subprocess fix (tasks 155-156) — unrelated seam.

## Dependencies

- **Blocks on:** task 147 (precedent contract), task 153 (assembly path this task's
  E2E harness builds on) — both already merged.
- **Blocks:** tasks 158 and 159 are sequenced AFTER this task (same files:
  `internal/cli/orchestrate.go`, `internal/channel/telegram/adapter.go`) to avoid
  merge conflicts — land 157 first, then 158, then 159.
