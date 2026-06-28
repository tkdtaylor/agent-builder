# Task 097: Telegram channel adapter — security hardening (SEC-001/002/003)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Harden the Telegram channel adapter (`internal/channel/telegram`) against three
defense-in-depth findings raised by the security auditor after task 080 shipped.
No behavior change for well-formed traffic; all changes are fail-closed (oversized
input dropped, guard timeout drops the goal, sentinel-error classification is
strictly typesafe).

## Context

Task 080 shipped the adapter and passed spec-verifier + security-auditor
APPROVE-for-merge. The auditor flagged three items to address before production
exposure:

- **SEC-001 (medium):** `getUpdates` decodes `resp.Body` with no size cap and does
  not bound per-message `Text` length. An attacker-influenced Telegram server can
  push an arbitrarily large response body or a single gigantic message text.
- **SEC-002 (medium):** `contentGuard.DecideContent` is called with
  `context.Background()`. A hung armor subprocess stalls `Next()` (and thus the
  whole channel) indefinitely.
- **SEC-003 (low):** Rejection-reason classification uses
  `strings.Contains(err.Error(), "replay")` etc. This silently degrades if
  `internal/envelope` reworks its error wording.

**Modules touched:**
- `internal/channel/telegram` — SEC-001, SEC-002, SEC-003 (adapter side)
- `internal/envelope` — SEC-003 only (add exported sentinel errors)

SEC-003 adds exported sentinel errors to `internal/envelope`. After this change the
F-007 fitness check (`make fitness-envelope-isolation`) must still pass — exporting
sentinel *error values* from the leaf does not change its import graph.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                  | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-097-01 | `getUpdates` wraps `resp.Body` in `io.LimitReader` (or `http.MaxBytesReader`) capped at `Config.MaxBodyBytes` (default: a few MB, e.g. 4 MB); an oversized body causes `getUpdates` to return an error without OOM or poll-loop crash.      | must have |
| REQ-097-02 | Before parsing the envelope, the adapter checks `len(update.Message.Text) > Config.MaxMessageBytes` (default: 64 KB); messages exceeding the bound are skipped (offset advanced, audit reject event emitted) without erroring the poll loop. | must have |
| REQ-097-03 | The adapter derives a bounded context for each `contentGuard.DecideContent` call using `context.WithTimeout(ctx, Config.GuardTimeout)`; on timeout the goal is dropped and an audit reject event is emitted (fail-closed).                   | must have |
| REQ-097-04 | `internal/envelope` exports four typed sentinel errors: `ErrUnknownKey`, `ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp`. Each is returned (wrapped with `%w`) from the relevant code paths so `errors.Is` matches through the chain.   | must have |
| REQ-097-05 | The adapter classifies envelope rejection reasons via `errors.Is(err, envelope.ErrXxx)` — not `strings.Contains(err.Error(), ...)`. Each sentinel maps to a specific audit reason string.                                                    | must have |
| REQ-097-06 | All existing TC-080 tests (sign/verify/replay happy + sad paths) pass without behavior change after the sentinel additions.                                                                                                                  | must have |

## Acceptance criteria

- [ ] [REQ-097-01] TC-097-01a: A stub server returning a body exceeding `MaxBodyBytes`
  causes `getUpdates` to return a non-nil error; the test completes within 2 s with no
  memory spike greater than `10 × MaxBodyBytes`.
- [ ] [REQ-097-02] TC-097-01b: A batch with one over-length text update (exceeds
  `MaxMessageBytes`) and one valid update — the adapter returns the valid goal,
  advances the offset past both updates, and emits exactly one audit reject event with
  an oversize-related reason for the skipped update; `armorGuard.invocationCount == 1`.
- [ ] [REQ-097-01/02] TC-097-01c: A normal-sized message passes through without error or
  audit event.
- [ ] [REQ-097-03] TC-097-02a: With a `blockingGuard` stub (blocks until context is
  cancelled) and `Config.GuardTimeout = 100 ms`, `adapter.Next()` returns within 150 ms
  with `(Task{}, false, nil)` and exactly one timeout-related audit reject event; offset
  is advanced.
- [ ] [REQ-097-03] TC-097-02b: With a fast allow-all guard and `Config.GuardTimeout = 500 ms`,
  `adapter.Next()` returns the goal normally with no timeout-related audit event.
- [ ] [REQ-097-04] TC-097-03a: `errors.Is(wrappedErr, envelope.ErrXxx)` returns `true`
  for each of the four sentinels; no sentinel matches the wrong error class.
- [ ] [REQ-097-05] TC-097-03b: For each sentinel class, the adapter emits an audit event
  with the correct specific reason string (not the generic `"envelope_rejected"` fallback).
- [ ] [REQ-097-06] TC-097-03c: `go test -count=1 ./internal/channel/telegram/... ./internal/envelope/...`
  passes in full — all TC-080 paths still green.
- [ ] F-007 still passes: `make fitness-envelope-isolation` → `PASS fitness-envelope-isolation: ...`

## Verification plan

- **Highest level achievable:** L2/L3 — no live bot token; no runtime-observable
  surface beyond unit tests and the fitness gate.
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/channel/telegram/...
  go test -count=1 ./internal/envelope/...
  ```
  Expected: both packages `ok`.
- **L3 isolation check:**
  ```
  make fitness-envelope-isolation
  ```
  Expected: `PASS fitness-envelope-isolation: internal/envelope is not in internal/supervisor's dependency graph.`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Live Telegram bot integration (L6 follow-on).
- Rate-limiting or per-sender throttling.
- Key rotation.
- TLS certificate pinning for the Telegram API endpoint.
- Upstream Telegram API server authentication beyond the bot token.

## Dependencies

- Task 080 merged (`internal/channel/telegram` adapter shipped — this task extends it)
- Task 096 merged (`internal/envelope` leaf shipped — this task adds sentinel errors to it)
