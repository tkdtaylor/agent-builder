# Test spec — Task 097: Telegram channel adapter security hardening

**Linked task:** `docs/tasks/backlog/097-telegram-security-hardening.md`
**Written:** 2026-06-27
**Status:** ready

## Context

Task 080 shipped the Telegram channel adapter (`internal/channel/telegram`) and passed
both spec-verifier and a security-auditor APPROVE-for-merge. The security auditor
flagged three defense-in-depth findings to address before production exposure. This
task resolves them.

The three findings:

- **SEC-001 (medium) — unbounded input at the untrusted front door.** `getUpdates`
  decodes `resp.Body` with no size cap; no per-message text length cap; no cap on the
  number of updates per batch. An attacker-controlled Telegram server (or
  MITM-attacker) can send an arbitrarily large response body to trigger OOM or
  excessive JSON-decode work.
- **SEC-002 (medium) — no channel-level deadline on armor.** `contentGuard.DecideContent`
  is called with `context.Background()` — a non-cancellable context. A hung armor
  subprocess stalls `Next()` indefinitely.
- **SEC-003 (low) — brittle rejection-reason classification.** The adapter classifies
  envelope rejection reasons via `strings.Contains(err.Error(), ...)` for
  `"replay"`/`"nonce"`/`"unknown_key"`/`"bad_signature"`. This silently degrades if
  `internal/envelope` reworks its error wording.

SEC-003 touches two packages: `internal/envelope` (add exported sentinel errors) and
`internal/channel/telegram` (switch to `errors.Is`). The F-007 envelope-isolation and
supervisor-isolation invariants must remain green after this change.

**Module boundaries touched:**
- `internal/channel/telegram` — SEC-001, SEC-002, SEC-003 (adapter side)
- `internal/envelope` — SEC-003 (sentinel error definitions)

---

## Requirements coverage

| Req ID     | Description                                                                                                | Test cases                          |
|------------|------------------------------------------------------------------------------------------------------------|-------------------------------------|
| REQ-097-01 | Response body is wrapped in `io.LimitReader` (or equivalent) with a configurable max (default ≤ a few MB); oversized body is rejected without OOM and without crashing the poll loop | TC-097-01a, TC-097-01c              |
| REQ-097-02 | Per-message `Text` length is checked against a configurable bound (default ≤ 64 KB); messages exceeding the bound are skipped (offset advanced, audit reject event emitted) | TC-097-01b, TC-097-01c              |
| REQ-097-03 | Armor `DecideContent` call uses a bounded context derived from a configurable channel-level timeout; on timeout the goal is dropped and an audit reject event is emitted (fail-closed) | TC-097-02a, TC-097-02b              |
| REQ-097-04 | `internal/envelope` exports typed sentinel errors (`ErrUnknownKey`, `ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp`), each wrapped appropriately so `errors.Is` matches them in the call chain | TC-097-03a                          |
| REQ-097-05 | The adapter classifies envelope rejection reasons via `errors.Is` against the sentinel errors, not `strings.Contains`; the correct audit reason is emitted for each sentinel class | TC-097-03b                          |
| REQ-097-06 | The existing TC-080 envelope-level tests (sign/verify/replay happy and sad paths) continue to pass without behavior change | TC-097-03c                          |

---

## Pre-implementation checklist

- [x] Task 080 merged (`internal/channel/telegram` adapter shipped)
- [x] Task 096 merged (`internal/envelope` leaf shipped — sentinels will extend it)
- [x] Security-auditor APPROVE-for-merge recorded (findings SEC-001/002/003 accepted as
  defense-in-depth items)
- [ ] `make check` green before branching

---

## Test cases

### TC-097-01a — Oversized response body is rejected without OOM and without crashing the poll loop

- **Requirement:** REQ-097-01
- **Level:** L2 (unit test with a stub Telegram HTTP server using `net/http/httptest`)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
Configure a `MaxBodyBytes` limit of 1024 bytes in the adapter's `Config`
(e.g. `Config.MaxBodyBytes = 1024`). Stand up a stub Telegram HTTP server that
returns a response body of exactly 4096 bytes of valid-looking JSON prefix — large
enough to exceed the cap but not a complete valid JSON document. For simplicity, the
body can be a byte-repeated sequence `{"ok":true,"result":[` followed by 4000 `x`
bytes and no closing bracket, ensuring the JSON decoder will fail when the limit is
hit.

**Step:** Call `adapter.getUpdates()` (or `adapter.Next()` — the poll path must hit
`getUpdates` internally).

**Expected output:**
- `getUpdates()` returns a non-nil error (body was cut by the limiter; decode fails).
- The error message contains `"limit"` or `"truncated"` or `"body"` (substring match —
  implementation's choice; the key property is that an error is returned, not a hang
  or panic).
- The process does NOT exhaust heap memory (no OOM). This is asserted implicitly by
  the test completing without a timeout kill; an explicit `runtime.ReadMemStats` check
  before and after, confirming `HeapAlloc` did not grow by more than `10 × MaxBodyBytes`,
  is an acceptable stronger assertion.
- The overall test completes within 2 seconds (no infinite loop).

---

### TC-097-01b — Over-length message text is skipped; offset advances; audit reject event is emitted

- **Requirement:** REQ-097-02
- **Level:** L2 (unit test with stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
Configure `Config.MaxMessageBytes = 256` (a small limit for test purposes). Stand up
a stub server returning two updates:
- `update_id = 200`: `message.text` is a string of 300 bytes (exceeds limit).
- `update_id = 201`: `message.text` is a short valid envelope JSON (within limit,
  signed and sealed correctly using the same helper as TC-080-01).

Wire a counting stub `armor.Guard` (records how many times `DecideContent` is called),
a stub `audit.Sink` that records all events, and a valid `ReplayCache`.

**Step:** Call `adapter.Next()`.

**Expected output:**
- Returns the goal decoded from `update_id = 201` (the second, valid update):
  `(supervisor.Task{...}, true, nil)`.
- `adapter.offset` equals `202` — both offsets were advanced (200 and 201 consumed).
- `audit.Sink` received exactly one rejection event for `update_id = 200` with an
  action or reason containing `"oversize"` or `"text_too_long"` or `"max_message"` or
  similar (exact string is implementation's choice; assert non-zero rejection count
  and that the reason is not an envelope or armor failure reason).
- `armorGuard.invocationCount == 1` — armor was NOT called on the oversize message,
  only on the valid second message.

---

### TC-097-01c — Normal-sized message passes through unbounded; poll loop remains operational after a limit event

- **Requirement:** REQ-097-01, REQ-097-02
- **Level:** L2 (unit test with stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
Configure `Config.MaxBodyBytes` at a generous but finite value (e.g. 1 MB) and
`Config.MaxMessageBytes` at 64 KB. Stand up a stub server returning one update
with a message text well within both limits (the same valid envelope as TC-080-01).

**Step:** Call `adapter.Next()`.

**Expected output:**
- Returns `(supervisor.Task{Spec: <plaintext>}, true, nil)` — the goal is delivered
  normally.
- No audit rejection events.
- The test completes without error.

**Purpose:** Regression guard — confirms the limit machinery does not block
normal traffic.

---

### TC-097-02a — Blocked armor guard (slow): Next() returns within the channel-level timeout; goal is dropped; audit event emitted

- **Requirement:** REQ-097-03
- **Level:** L2 (unit test with stub guard and stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
Configure `Config.GuardTimeout = 100 * time.Millisecond` (a short timeout for test
purposes).

Implement a `blockingGuard` stub that satisfies `ContentGuard`:
```go
type blockingGuard struct{}
func (g *blockingGuard) DecideContent(ctx context.Context, _ ingestion.ContentCandidate) (ingestion.Decision, error) {
    <-ctx.Done()   // blocks until the context is cancelled
    return ingestion.Decision{}, ctx.Err()
}
```

Stand up a stub Telegram server returning one valid update (correctly signed and
sealed envelope — cryptographic checks must pass so the code reaches the armor call).
Wire a stub `audit.Sink` that records all events.

**Step:** Call `adapter.Next()`. Record wall-clock time before and after.

**Expected output:**
- `adapter.Next()` returns within `GuardTimeout + 50 ms` (i.e., within 150 ms for
  this test). The test has a hard outer deadline of 2 seconds to catch regressions
  where the timeout is not applied.
- Returns `(supervisor.Task{}, false, nil)` — the goal is dropped (fail-closed on
  guard timeout).
- `audit.Sink` received exactly one rejection event with an action or reason
  containing `"timeout"` or `"guard_timeout"` or `"armor_error"` (substring match —
  the important property is that a rejection event was emitted for this update, not
  silence).
- `adapter.offset` equals `update_id + 1` — the update is consumed (not re-polled
  after the timeout).

---

### TC-097-02b — Short guard timeout does not affect a fast-returning guard

- **Requirement:** REQ-097-03
- **Level:** L2 (unit test)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
Configure `Config.GuardTimeout = 500 * time.Millisecond`. Use the standard allow-all
stub guard from TC-080-01 (returns `Allow` immediately). Stand up a stub server with
one valid update.

**Step:** Call `adapter.Next()`.

**Expected output:**
- Returns `(supervisor.Task{Spec: <plaintext>}, true, nil)` — goal delivered normally.
- No timeout-related audit events.

**Purpose:** Regression guard — confirms the bounded context does not break the
normal (fast) code path.

---

### TC-097-03a — Sentinel errors: errors.Is matches each typed error across the wrapping chain

- **Requirement:** REQ-097-04
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go` (or `sentinel_test.go`)

**Setup:**
The following four sentinel variables are exported from `internal/envelope`:
- `envelope.ErrUnknownKey`
- `envelope.ErrBadSignature`
- `envelope.ErrReplay`
- `envelope.ErrStaleTimestamp`

For each sentinel, construct the scenario that causes `internal/envelope` to produce
that error, and wrap it one additional level to simulate the call chain:

```
// ErrUnknownKey: Verify with wrong public key
_, wrongPub, _ := ed25519.GenerateKey(rand.Reader)
err := envelope.Verify(signedEnv, wrongPub)         // signed with a different key
wrapped := fmt.Errorf("outer: %w", err)

// ErrBadSignature: Tamper the sig field
tampered := signedEnv; tampered.Sig = "deadbeef" + tampered.Sig[8:]
err = envelope.Verify(tampered, correctPub)
wrapped = fmt.Errorf("outer: %w", err)

// ErrReplay: Check same nonce twice in the ReplayCache
cache := envelope.NewReplayCache(60 * time.Second)
_ = cache.Check("nonce-A", time.Now())
err = cache.Check("nonce-A", time.Now())
wrapped = fmt.Errorf("outer: %w", err)

// ErrStaleTimestamp: Check with a stale timestamp
err = cache.Check("fresh-nonce", time.Now().Add(-61*time.Second))
wrapped = fmt.Errorf("outer: %w", err)
```

**Expected output (for each sentinel):**
- `errors.Is(wrapped, envelope.ErrUnknownKey)` returns `true` for the unknown-key case.
- `errors.Is(wrapped, envelope.ErrBadSignature)` returns `true` for the bad-signature case.
- `errors.Is(wrapped, envelope.ErrReplay)` returns `true` for the replay case.
- `errors.Is(wrapped, envelope.ErrStaleTimestamp)` returns `true` for the stale-timestamp case.
- No sentinel matches incorrectly (e.g., `errors.Is(replayErr, ErrBadSignature)` returns `false`).

---

### TC-097-03b — Adapter routes each sentinel to the correct audit reason via errors.Is

- **Requirement:** REQ-097-05
- **Level:** L2 (unit test with stub Telegram server)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:**
For each of the four sentinel error classes, construct a stub `VerifyAndOpen`
replacement that returns `fmt.Errorf("wrap: %w", envelope.ErrXxx)` without
performing real cryptography. In practice this means using a stub or injected
`envelopeVerifier` that the test controls, or constructing the specific envelope that
triggers each sentinel naturally (whichever is simpler to implement — see note
below).

Wire a stub `audit.Sink` that records all emitted events.

**For each sentinel, call `adapter.Next()` and assert:**

| Sentinel returned by envelope layer | Expected audit event reason (substring) |
|-------------------------------------|------------------------------------------|
| `envelope.ErrUnknownKey`            | `"unknown_key"`                          |
| `envelope.ErrBadSignature`          | `"bad_signature"` or `"unknown_key"` (implementation may group these) |
| `envelope.ErrReplay`                | `"replay_detected"` or `"replay"`        |
| `envelope.ErrStaleTimestamp`        | `"stale"` or `"replay_detected"` (implementation may group stale under replay) |

**Expected output:**
- For each case: exactly one audit event is emitted for the update; the reason string
  matches the substring in the table above.
- The audit event is NOT emitted with `"envelope_rejected"` as the sole reason (the
  generic fallback) — the sentinel must have been used to produce a more specific
  reason. (Exception: if `ErrBadSignature` and `ErrUnknownKey` are grouped under
  `"unknown_key"`, that is acceptable if the test documents the grouping.)
- `errors.Is` is the classification mechanism — assert that no `strings.Contains` on
  `err.Error()` is present in the implementation (this is a code-level assertion
  enforced during code review; the test verifies the audit output).

**Implementation note:** The simplest approach is to inject a narrow interface for the
envelope verification step so the test can stub it to return wrapped sentinel errors
directly. Alternatively, the test can construct real envelopes that trigger each
error naturally (unknown-key and bad-sig via real crypto; replay via double-submit to
`ReplayCache`). Either approach is acceptable; document which is used.

---

### TC-097-03c — Existing TC-080 envelope-path tests pass without behavior change

- **Requirement:** REQ-097-06
- **Level:** L2 (regression run of existing tests)
- **Test file:** `internal/channel/telegram/adapter_test.go` (no new code needed; run the existing suite)

**Step:** Run `go test -count=1 ./internal/channel/telegram/... ./internal/envelope/...`.

**Expected output:**
- All tests pass.
- Specifically, the tests corresponding to TC-080-01 (happy path), TC-080-02 (unknown
  key), TC-080-03 (replay), TC-080-04 (armor block), TC-080-05 (interface assertion),
  and TC-080-06 (log scrub) all still pass.
- The only behavior change is: rejection audit events that previously carried
  `"envelope_rejected"` as a generic fallback now carry a specific reason derived
  from `errors.Is` classification — but the tests in the existing suite check for a
  non-zero rejection event count and a reason that is NOT the private key or bot
  token, so they remain valid.

---

## Verification plan

- **Highest level achievable:** L2/L3 — no live Telegram bot token is available; the
  adapter has no other runtime-observable surface.
- **L2 harness command (channel hardening):**
  ```
  go test -count=1 ./internal/channel/telegram/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/channel/telegram`
- **L2 harness command (envelope sentinel errors):**
  ```
  go test -count=1 ./internal/envelope/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/envelope`
- **L3 isolation check (F-007 must remain green after adding sentinel exports):**
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

- Live Telegram bot integration (no bot token available; L6 is a follow-on).
- Rate-limiting or per-sender throttling (separate defense-in-depth topic; no
  security-audit finding for it in task 080).
- Key rotation (tracked separately).
- TLS certificate pinning for the Telegram API endpoint (out-of-scope for the adapter
  layer; handled at the HTTP client level by the operator).
- Upstream (Telegram API server) authentication beyond the bot token — the token is a
  bearer credential; MITM resistance is the transport's job (TLS), not the adapter's.
