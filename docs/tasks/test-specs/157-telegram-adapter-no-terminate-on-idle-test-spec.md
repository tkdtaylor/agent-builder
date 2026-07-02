# Test Spec 157: Telegram `Adapter.Next` no longer terminates the control plane on an empty/rejected poll

**Linked task:** [`docs/tasks/backlog/157-telegram-adapter-no-terminate-on-idle.md`](../backlog/157-telegram-adapter-no-terminate-on-idle.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

`telegram.Adapter.Next()` (`internal/channel/telegram/adapter.go:197-345`) polls
`getUpdates()` ONCE and returns `(supervisor.Message{}, false, nil)` at line 344 when
that single poll batch contains no deliverable message — either because the batch was
genuinely empty (idle poll, the common steady-state case) or because every update in
the batch was rejected (bad envelope, unapproved sender, armor block, oversized
message, etc.).

The orchestrate control loop (`internal/cli/orchestrate.go:685-712`) calls
`oc.source.Next()` in a `for {}` loop and treats `ok == false` as SOURCE EXHAUSTED:

```go
msg, ok, err := oc.source.Next()
...
if !ok {
    break   // internal/cli/orchestrate.go:699
}
```

This conflates "nothing right now, keep polling" with "the source is permanently
done" — the SAME contract violation task 147 fixed for the answer-conversation linger,
but here on the INBOUND read side. Consequences:

- **DoS via one junk message.** Any Telegram user (not necessarily an approved sender
  — the reject can happen before any sender-ID check, e.g. a malformed envelope) can
  send ONE message that every update in a batch fails to yield a deliverable message
  for, and the entire `orchestrate` control plane exits.
- **Non-durability.** A single idle 30s poll with literally no updates ALSO returns
  `ok=false` today, terminating orchestrate even with zero attacker involvement — the
  control plane cannot durably run unattended against a live Telegram bot at all.

Contrast the precedent `envMessageSource.Next` (`internal/cli/router.go:96-119`),
which the loop's contract was written against: it blocks on `s.scanner.Scan()`
internally in a `for` loop and returns `ok=false` ONLY at genuine stdin EOF (or a hard
scanner error) — never on an empty/skippable line.

**The fix:** `Adapter.Next()` re-polls INTERNALLY (a `for` loop around the existing
per-batch scan, respecting a shutdown signal for cancellation) so it returns
`ok=false` ONLY on genuine terminal shutdown (the adapter's own context/stop signal
firing) — never merely because one poll batch had nothing deliverable. A hard
transport failure (`getUpdates` returning a non-nil error) is tolerated internally with
a bounded retry/backoff rather than immediately propagating as a fatal `Next()` error
that also breaks the control loop, UNLESS shutdown fires first.

This requires giving `Adapter` a way to observe genuine shutdown, since
`supervisor.MessageSource.Next() (msg Message, ok bool, err error)` takes no
arguments (mirroring the existing `GoalSource`/`MessageSource` pure-stdlib contract —
this task does NOT change that interface). The adapter is constructed with the SAME
top-level `context.Context` `RunControlLoop` already holds (available at
`assembleTelegramInbound` call time, BEFORE the control loop starts) as a new `Config`
field; `Next()` selects on that context's `Done()` between poll attempts instead of
returning `false` after one empty/rejected batch.

**Module boundaries touched:** `internal/channel/telegram` (the `Adapter`'s internal
polling loop, `Config`/`NewAdapter`) and `internal/cli` (wiring the control loop's
top-level `ctx` into `assembleTelegramInbound`'s `Adapter` construction — a
one-argument addition to an existing call, not new control-flow logic in `orchestrate.go`
itself, since the fix lives entirely inside `Adapter.Next`).

---

## Requirements coverage

| Req ID     | Description                                                                                                                             | Test cases            |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-157-01 | `Adapter` gains a shutdown-observing seam (a `context.Context` field set at construction) that `Next()` selects on between poll attempts | TC-157-01               |
| REQ-157-02 | `Next()` re-polls internally on an empty batch (no updates at all) instead of returning `ok=false` — it returns a REAL message once one arrives, without the control loop ever observing a false `ok` | TC-157-02               |
| REQ-157-03 | `Next()` re-polls internally when a batch's every update is rejected (bad envelope / unapproved sender / armor block / oversized) instead of returning `ok=false` | TC-157-03               |
| REQ-157-04 | `Next()` returns `ok=false` (with `err=nil`) ONLY when the adapter's shutdown context fires — proven by a live scripted-server harness where the adapter is polling, shutdown then fires, and `Next()` returns promptly with `ok=false` | TC-157-04               |
| REQ-157-05 | A hard `getUpdates` transport failure does not immediately propagate as a `Next()` error that kills the control loop; the adapter retries internally with a bounded backoff, still respecting the shutdown context | TC-157-05               |
| REQ-157-06 | End-to-end through the REAL control loop (`internal/cli`'s `RunControlLoop`/`orchestrate` machinery, not just `Adapter.Next` in isolation): an idle poll followed later by a real message delivers that message to the control plane without the loop having terminated in between | TC-157-06               |
| REQ-157-07 | End-to-end: an all-rejected batch (e.g. one unapproved-sender message) does NOT terminate the control loop; a subsequent approved message still arrives | TC-157-07               |
| REQ-157-08 | End-to-end: cancelling the control loop's top-level context DOES cleanly terminate both `Adapter.Next` and the control loop (no goroutine leak, no hang) | TC-157-08               |
| REQ-157-09 | Pre-existing `internal/channel/telegram` and `internal/cli` test suites (tasks 080/097/098/151/152/153) continue to pass unchanged in behavior | TC-157-09               |

---

## Pre-implementation checklist

- [x] Task 147 merged (established the "finite source terminates cleanly, live source
  lingers/re-polls" precedent this task extends to the READ side)
- [x] Task 153 merged (`assembleTelegramInbound` / `inboundFromEnv` full assembly path
  already exists and is exercised by `internal/cli` tests — this task's E2E cases build
  on that harness)
- [ ] `make check` green before branching

---

## Test cases

### TC-157-01 — `Adapter` accepts a shutdown context at construction

- **Requirement:** REQ-157-01
- **Level:** L2 (unit test)
- **Test file:** `internal/channel/telegram/adapter_test.go` or a new `adapter_157_test.go`

**Setup:** Construct `telegram.NewAdapter(telegram.Config{..., Ctx: someCtx})` (or the
chosen field name).

**Step:** Inspect (via a package-internal test, or an observable side effect) that the
adapter retains the passed context and a nil/omitted `Ctx` defaults to
`context.Background()` (never a nil-context panic).

**Expected output:** No panic on omitted `Ctx`; the adapter's internal polling loop
observably uses the supplied context when one is given (verified jointly with
TC-157-04).

---

### TC-157-02 — An empty poll batch does not terminate `Next()`; it re-polls until a real message arrives

- **Requirement:** REQ-157-02
- **Level:** L2 (unit test, stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_157_test.go`

**Setup:** A stub HTTP server (mirroring the existing `scriptedServer` pattern) that
returns an EMPTY `getUpdates` result for its first N calls, then a real, valid
envelope-wrapped new-goal message on call N+1. Construct the adapter with a
long-lived (non-cancelled) `Ctx`.

**Step:** Call `adapter.Next()` exactly ONCE from the test's perspective.

**Expected output:** `Next()` blocks internally through the N empty polls and returns
`(msg, true, nil)` with the real message from call N+1 — the caller never observes an
intermediate `ok=false`. (Poll interval is injectable/short in the test harness so this
does not need to wait real wall-clock idle-poll durations.)

---

### TC-157-03 — An all-rejected batch does not terminate `Next()`; it re-polls until an accepted message arrives

- **Requirement:** REQ-157-03
- **Level:** L2 (unit test, stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_157_test.go`

**Setup:** A stub server whose first batch contains ONE update that fails envelope
verification (malformed JSON or bad signature — any existing reject path already
covered by tasks 080/097/098), and whose second batch contains a valid message.

**Step:** Call `adapter.Next()` once.

**Expected output:** `Next()` returns `(msg, true, nil)` for the SECOND batch's valid
message — the first batch's full-rejection does not surface as `ok=false`. The
rejection still emits its existing audit event (regression: `TestTC0XX` reject-reason
assertions for this same construction continue to pass — audit behavior is unchanged,
only the termination behavior changes).

---

### TC-157-04 — `Next()` returns `ok=false` only when the shutdown context fires

- **Requirement:** REQ-157-04
- **Level:** L2 (unit test, stub Telegram server that never yields a message)
- **Test file:** `internal/channel/telegram/adapter_157_test.go`

**Setup:** A stub server that always returns empty batches. Construct the adapter with
a cancellable `Ctx`.

**Step:** Call `adapter.Next()` in a goroutine; after the test observes (via a
synchronization hook) that at least one poll has happened, cancel the adapter's `Ctx`.

**Expected output:** `Next()` returns `(supervisor.Message{}, false, nil)` within a
bounded time after the cancel (not immediately at the first empty poll, and not
hanging past the cancel) — proving `ok=false` is reserved for genuine shutdown.

---

### TC-157-05 — A hard transport failure is retried internally, not immediately fatal

- **Requirement:** REQ-157-05
- **Level:** L2 (unit test, stub server returning a transport-level error then recovering)
- **Test file:** `internal/channel/telegram/adapter_157_test.go`

**Setup:** A stub server (or injected HTTP round-tripper) that returns a connection
error / non-2xx response for its first M calls, then a valid message on call M+1.

**Step:** Call `adapter.Next()` once with a long-lived `Ctx`.

**Expected output:** `Next()` returns `(msg, true, nil)` for the eventual valid
message — a transient transport failure does not propagate as a fatal `(Message{},
false, err)` on the first failed attempt. (If the implementation instead caps retries
and eventually surfaces a hard error, this test asserts that cap is bounded and
documented — but the DEFAULT covered scenario, recovery within the bound, must
succeed without a `Next()` error.)

---

### TC-157-06 — End-to-end: the real control loop survives an idle poll and later delivers a message

- **Requirement:** REQ-157-06
- **Level:** L5 (real `internal/cli` control-loop machinery over a scripted Telegram stub server)
- **Test file:** `internal/cli/orchestrate_157_test.go` (new)

**Setup:** Build a real `Adapter`/`ReplyAdapter` pair via `assembleTelegramInbound`
(mirroring `tc153FullTelegramEnv`) against a scripted stub server that returns empty
batches for a few polls, then a real new-goal message. Run `RunControlLoop` (or the
package's equivalent entry point) with a bounded overall test timeout.

**Step:** Start the control loop; wait for it to report the goal accepted.

**Expected output:** The control loop is STILL RUNNING through the idle polls (it does
not exit/break at any point before the real message arrives) and correctly routes the
eventual message as `MsgNewGoal` — proving the fix holds through the real
`orchestrate.go` loop, not merely `Adapter.Next` in isolation.

---

### TC-157-07 — End-to-end: an all-rejected batch does not terminate the control loop

- **Requirement:** REQ-157-07
- **Level:** L5 (real control-loop machinery)
- **Test file:** `internal/cli/orchestrate_157_test.go`

**Setup:** Same harness as TC-157-06, but the scripted server's first batch is a
single REJECTED update (e.g. malformed envelope JSON, or — if `AUTH_MODE` other than
`envelope` is exercised — an unapproved sender), followed by a valid message.

**Step:** Start the control loop; wait for it to report the goal accepted.

**Expected output:** The control loop survives the rejected batch and correctly routes
the SECOND, valid message — the DoS scenario from the review (one junk message kills
orchestrate) no longer reproduces.

---

### TC-157-08 — End-to-end: a genuine shutdown still cleanly terminates the control loop

- **Requirement:** REQ-157-08
- **Level:** L5

**Setup:** Same harness family; the scripted server never yields a message.

**Step:** Start the control loop with a cancellable top-level `ctx`; cancel it after a
short delay.

**Expected output:** The control loop returns within a bounded time after the cancel
(no goroutine leak, no hang past a generous test timeout) — the fix does not turn a
genuine shutdown into an infinite hang; it only stops MISCLASSIFYING transient
idle/reject conditions as shutdown.

---

### TC-157-09 — Full regression: pre-existing Telegram/CLI suites pass unchanged

- **Requirement:** REQ-157-09
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
make check
```

**Expected output:** All packages `ok`; every pre-existing task-080/097/098/151/152/153
test continues to pass with unchanged assertions (only `NewAdapter`/`assembleTelegramInbound`
call sites gain the new `Ctx`/context-forwarding parameter, mirroring how task 153 added
a trailing `warnOut` parameter to existing call sites without changing their behavior).
`make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5 — a real `internal/cli` control-loop harness driven
  against a scripted stub Telegram server proves the fix holds through the actual
  `orchestrate` machinery, not just `Adapter.Next` in isolation. A live bot token (L6)
  is optional additional confidence but not required to prove the REQs (no
  Telegram-side behavior beyond `getUpdates`/`sendMessage`, both already exercised by
  the stub server harness, differs on a live bot).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... -run TestTC157
  ```
  Expected: all TC-157-01..05 pass.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... -run TestTC157
  ```
  Expected: TC-157-06/07/08 pass — control loop survives idle/reject, still terminates on shutdown.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (optional, operator-observed):** run `orchestrate` against a real Telegram bot;
  send one unapproved/malformed message, confirm the process is still alive and a
  follow-up valid command still routes.

## Out of scope

- Changing the `supervisor.MessageSource` interface itself (still `Next() (Message,
  bool, error)`, no arguments) — the fix is entirely internal to `Adapter.Next`'s
  polling loop plus a construction-time context field.
- Armor guard wiring (task 158) and pairing-owner seeding (task 159) — this task
  precedes both in the sequenced Telegram set to avoid `orchestrate.go`/`adapter.go`
  merge conflicts; it does not touch armor or pairing-store logic.
- Any change to the wall-clock timeout / cancel-subprocess fix (tasks 155-156) —
  unrelated seam.
