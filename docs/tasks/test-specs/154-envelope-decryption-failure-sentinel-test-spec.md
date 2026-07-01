# Test Spec 154: classify AEAD/decryption failures with a sentinel in `internal/envelope`

**Linked task:** [`docs/tasks/backlog/154-envelope-decryption-failure-sentinel.md`](../backlog/154-envelope-decryption-failure-sentinel.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Context

`internal/envelope/confidentiality.go`'s `Open` returns a bare, unwrapped
`errors.New("authentication failed: nacl/box decrypt returned false")` on AEAD
failure. It matches none of the four existing sentinels (`ErrUnknownKey`,
`ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp` — `internal/envelope/envelope.go`).
Consequences of the gap, found during task 150 spec-verification:

- `internal/channel/telegram/adapter.go`'s rejection classifier (task 097) falls
  through to the generic `"envelope_rejected"` reason for every decrypt failure.
- `internal/channel/worker/transport.go`'s `Receiver.classify` (task 083/098) has the
  identical generic `"envelope_rejected"` fallback for the same reason — a second,
  independent consumer of the same gap.
- `examples/agent-cli/main.go`'s `reply-open` (task 150) classifies decrypt failures
  via an `else`-branch heuristic ("any non-sentinel `VerifyAndOpen` error after
  JSON-parse+verify+replay is definitionally a decrypt failure") — correct today, but
  fragile: it silently breaks if `VerifyAndOpen` ever grows a new unclassified failure
  mode between the replay check and `Open`.

This task adds `envelope.ErrDecryptionFailed`, wires `Open` to wrap it, and updates
all three consumers to classify via `errors.Is` instead of falling through to a
generic reason or an else-branch heuristic. This directly extends the task 097
sentinel-classification pattern to cover the one step it missed.

**Module boundaries touched:**
- `internal/envelope` — new sentinel, `Open`'s error wrapped with `%w`
- `internal/channel/telegram` — adapter classifier gains one more `errors.Is` branch
- `internal/channel/worker` — `Receiver.classify` gains one more `errors.Is` branch
- `examples/agent-cli` — `reply-open`'s error-branch heuristic replaced with an
  explicit `errors.Is` check

F-007 (`fitness-envelope-isolation`) and F-0xx (`fitness-worker-transport-isolation`)
must remain green — this only adds an exported error *value* and its wrapping; it does
not change either leaf's import graph.

---

## Requirements coverage

| Req ID     | Description                                                                                                                            | Test cases                        |
|------------|------------------------------------------------------------------------------------------------------------------------------------------|------------------------------------|
| REQ-154-01 | `internal/envelope` exports `ErrDecryptionFailed`; `Open`'s AEAD-failure return is wrapped with `%w` so `errors.Is` matches through `VerifyAndOpen` | TC-154-01, TC-154-02              |
| REQ-154-02 | Malformed-hex-decode failures (`Nonce`/`Payload` decode errors) inside `VerifyAndOpen` are documented and consistently classified — either as `ErrDecryptionFailed` or an explicitly distinct class, not silently left bare | TC-154-03                         |
| REQ-154-03 | The existing four sentinels (`ErrUnknownKey`, `ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp`) continue to classify correctly and do not match `ErrDecryptionFailed` (or vice versa) | TC-154-04                         |
| REQ-154-04 | `internal/channel/telegram`'s adapter classifier emits a specific audit reason (e.g. `"decryption_failed"`) for a decrypt-failing inbound envelope, not the generic `"envelope_rejected"` fallback | TC-154-05                         |
| REQ-154-05 | `internal/channel/worker`'s `Receiver.classify` emits the same specific reason for a decrypt-failing inbound envelope, not the generic `"envelope_rejected"` fallback | TC-154-06                         |
| REQ-154-06 | `examples/agent-cli reply-open`'s wrong-opener-key case classifies via `errors.Is(err, envelope.ErrDecryptionFailed)` (not the else-branch heuristic), still printing "decryption failed" to stderr, empty stdout, exit 1 | TC-154-07                         |
| REQ-154-07 | All pre-existing tests in `internal/envelope`, `internal/channel/telegram`, `internal/channel/worker`, and `examples/agent-cli` continue to pass unchanged (aside from the one adapter/transport assertion each that is intentionally updated to the new specific reason) | TC-154-08                         |

---

## Pre-implementation checklist

- [x] Task 097 merged (`internal/envelope` sentinel pattern + adapter `errors.Is`
  classifier established)
- [x] Task 083/098 merged (`internal/channel/worker` `Receiver.classify` established)
- [x] Task 150 merged (`examples/agent-cli reply-open` else-branch heuristic in place —
  this task replaces it)
- [ ] `make check` green before branching

---

## Test cases

### TC-154-01 — `ErrDecryptionFailed` sentinel matches a direct `Open` AEAD failure

- **Requirement:** REQ-154-01
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go` (or a new `confidentiality_test.go`)

**Setup:** Seal a plaintext with a real sender-priv/recipient-pub pair. Call `Open`
with a mismatched recipient private key (a freshly generated, unrelated keypair) so
`box.Open` returns `ok == false`.

**Step:** `_, err := envelope.Open(ciphertext, nonce, wrongRecipPriv, senderPub)`.

**Expected output:**
- `err` is non-nil.
- `errors.Is(err, envelope.ErrDecryptionFailed)` is `true`.
- `err.Error()` still contains the existing descriptive text (e.g. "authentication
  failed" / "nacl/box decrypt returned false") — the message is preserved, only the
  sentinel wrapping is added.

---

### TC-154-02 — `ErrDecryptionFailed` matches through the full `VerifyAndOpen` call chain

- **Requirement:** REQ-154-01
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:** Build a fully valid, correctly signed envelope (valid signature, fresh
nonce/timestamp, real ciphertext sealed to the correct recipient's X25519 pub) so
`Verify` and the replay check both pass. Then call `VerifyAndOpen` with the correct
signer pub, a fresh `ReplayCache`, but a **wrong** `recipX25519Priv` (mismatched to the
sealed ciphertext) — so only the final `Open` step fails.

**Step:** `_, err := envelope.VerifyAndOpen(env, correctSignPub, cache, wrongRecipPriv, senderPub)`.

**Expected output:**
- `err` is non-nil.
- `errors.Is(err, envelope.ErrDecryptionFailed)` is `true`.
- `errors.Is(err, envelope.ErrUnknownKey)`, `errors.Is(err, envelope.ErrBadSignature)`,
  `errors.Is(err, envelope.ErrReplay)`, and `errors.Is(err, envelope.ErrStaleTimestamp)`
  are all `false` for this same error (proves the new sentinel is additive, not a
  broadened match on an existing one).

---

### TC-154-03 — Malformed-hex payload/nonce classification is explicit and consistent

- **Requirement:** REQ-154-02
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:** Build a validly signed envelope (so `Verify` passes) but set `Payload` to a
non-hex string (e.g. `"not-valid-hex!!"`) before calling `VerifyAndOpen`. Repeat with a
malformed `Nonce` field instead.

**Step:** `_, err := envelope.VerifyAndOpen(env, signPub, cache, recipPriv, senderPub)`
for each malformed-field case.

**Expected output (whichever classification the implementation documents — pick one
and apply it consistently to both fields):**
- Either: `errors.Is(err, envelope.ErrDecryptionFailed)` is `true` for both cases (hex
  decode failure treated as part of the decrypt-failure class), **or**: both cases
  return a distinct, separately-asserted error type/sentinel documented in the task's
  implementation notes (e.g. a malformed-input class that is NOT `ErrDecryptionFailed`
  and NOT any of the other three sentinels).
- Whichever choice is made, the SAME classification applies to both the malformed-hex
  `Nonce` case and the malformed-hex `Payload` case (no inconsistency between the two
  decode sites).
- The task file's implementation notes state which choice was made; this test asserts
  against that documented choice, not an arbitrary one chosen by the test author.

---

### TC-154-04 — Existing four sentinels still classify correctly; no cross-contamination

- **Requirement:** REQ-154-03
- **Level:** L2 (unit test — regression, mirrors TC-097-03a)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:** Reconstruct the four TC-097-03a scenarios:
- `ErrUnknownKey`: `Verify` with a public key of the wrong size.
- `ErrBadSignature`: `Verify` with a tampered signature (correct size, wrong bytes).
- `ErrReplay`: `ReplayCache.Check` the same nonce twice.
- `ErrStaleTimestamp`: `ReplayCache.Check` with a timestamp outside the freshness
  window.

**Step:** For each, wrap the returned error one level (`fmt.Errorf("outer: %w", err)`)
and check `errors.Is` against all five sentinels (the four existing plus the new
`ErrDecryptionFailed`).

**Expected output:**
- Each scenario's wrapped error matches ONLY its own sentinel (`errors.Is` is `true`
  for its own sentinel, `false` for the other four including `ErrDecryptionFailed`).
- No behavior change versus TC-097-03a's original assertions.

---

### TC-154-05 — Adapter classifies a decrypt-failing inbound envelope as `"decryption_failed"`, not the generic fallback

- **Requirement:** REQ-154-04
- **Level:** L2 (unit test with stub Telegram server, mirrors TC-097-03b)
- **Test file:** `internal/channel/telegram/adapter_test.go`

**Setup:** Construct a real envelope that is correctly signed and passes the replay
check but fails only at `Open` (sealed to a different recipient X25519 pub than the
adapter's configured `orchestratorPriv`/`trustedX25519Pub` pair — i.e. the ciphertext
does not decrypt with the adapter's configured keys). Stand up the stub Telegram
server returning this one update. Wire a stub `audit.Sink` recording all events.

**Step:** Call `adapter.Next()`.

**Expected output:**
- Returns `(supervisor.Task{}, false, nil)` — the goal is dropped, fail-closed.
- `audit.Sink` received exactly one rejection event whose reason is `"decryption_failed"`
  (the new specific reason) — **not** `"envelope_rejected"` (the old generic fallback)
  and not `"unknown_key"`/`"replay_detected"` (the other two mapped reasons).
- Any pre-existing adapter test that previously asserted a decrypt failure produced
  the generic `"envelope_rejected"` reason is found and updated to assert
  `"decryption_failed"` instead (locate via `grep -n "envelope_rejected"
  internal/channel/telegram/*_test.go` before editing).

---

### TC-154-06 — Worker transport `Receiver.classify` emits the same specific reason for a decrypt failure

- **Requirement:** REQ-154-05
- **Level:** L2 (unit test, mirrors TC-154-05 for the worker transport leaf)
- **Test file:** `internal/channel/worker/transport_test.go`

**Setup:** Construct a `Receiver` (work-item or result direction, either is
acceptable) with a real correctly-signed, replay-fresh envelope whose ciphertext is
sealed to a different X25519 recipient than the receiver's configured key (decrypt
step fails; verify + replay both pass). Wire a stub `audit.Sink`.

**Step:** Call the code path that invokes `verifyOpen` (`ReceiveWorkItem` or
`ReceiveResult`, matching whichever direction was constructed).

**Expected output:**
- Returns a zero value (`supervisor.Task{}` or `supervisor.Result{}`) and a non-nil
  error.
- `audit.Sink` received exactly one `ActionChannelReject` event whose
  `Detail.Reason` is the same specific reason string used in TC-154-05
  (`"decryption_failed"`) — not `"envelope_rejected"`.
- `Receiver.classify(err)` returns `"decryption_failed"` directly, assertable as a
  unit test on the method if it remains unexported-but-testable from within the
  package, or indirectly via the audit sink if not.

---

### TC-154-07 — `reply-open`'s wrong-opener-key case classifies via the real sentinel, not the heuristic

- **Requirement:** REQ-154-06
- **Level:** L2 (unit test, updates existing `TestTC150_04_FailClosed`)
- **Test file:** `examples/agent-cli/main_test.go`

**Setup:** Reuse the existing `TestTC150_04_FailClosed` "wrong opener key" table case
(`internal/channel/worker` roundtrip envelope opened with an unrelated, freshly
generated `OperatorXPriv`).

**Step:** Run `reply-open` against this case (as the existing test already does).

**Expected output:**
- Exit code 1.
- stderr contains `"decryption failed"` (unchanged user-visible message).
- stdout is empty.
- **Source-level assertion (new):** `examples/agent-cli/main.go`'s reply-open error
  branch contains an explicit `errors.Is(err, envelope.ErrDecryptionFailed)` check
  (or equivalent) for this case — the bare `else` fallback that previously caught this
  case by elimination is replaced. Verify by grepping the source for
  `envelope.ErrDecryptionFailed` in `examples/agent-cli/main.go` and asserting it is
  present; the `else` branch (if retained at all) becomes a last-resort catch for a
  truly unclassified error, not the mechanism that classifies this case.

---

### TC-154-08 — Full regression: pre-existing suites pass unchanged elsewhere

- **Requirement:** REQ-154-07
- **Level:** L2/L3 (regression run)
- **Test file:** N/A (full package run)

**Step:** Run:
```
go test -race -count=1 ./internal/envelope/... ./internal/channel/telegram/... ./internal/channel/worker/... ./examples/agent-cli/...
make check
```

**Expected output:**
- All packages `ok`.
- The only test assertions that change from their pre-task-154 state are: (a) the one
  adapter test and (b) the one worker-transport test that previously observed
  `"envelope_rejected"` for a decrypt failure (if any existed) and now observe
  `"decryption_failed"`, and (c) `TestTC150_04_FailClosed`'s wrong-opener-key case
  gaining the source-level sentinel assertion. All TC-080/083/097/098/150 happy-path
  and other sad-path assertions are untouched.
- `make check` → `All checks passed.`
- `make fitness-envelope-isolation` → `PASS fitness-envelope-isolation: internal/envelope
  is not in internal/supervisor's dependency graph.`
- `make fitness-worker-transport-isolation` → its existing PASS line, unchanged.

---

## Verification plan

- **Highest level achievable:** L2/L3 — no live bot token; no runtime-observable
  surface beyond unit tests and the fitness gates. The adapter's and worker
  transport's new audit reason is runtime-observable via the stub audit sink at L2 —
  no L5/L6 needed.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/envelope/... ./internal/channel/telegram/... ./internal/channel/worker/... ./examples/agent-cli/...
  ```
  Expected: all four packages `ok`.
- **L3 isolation checks:**
  ```
  make fitness-envelope-isolation
  make fitness-worker-transport-isolation
  ```
  Expected: both `PASS ...` (unchanged text from their current baseline).
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Any change to `Seal`'s behavior or signature.
- Rate-limiting, replay-store persistence, or key rotation — unrelated to this
  classification gap.
- A live Telegram bot or live worker-transport integration run (L6) — no
  runtime-observable surface beyond the audit sink exists for this change; L2/L3 is
  the ceiling.
- Renaming or regrouping the existing four sentinels' audit reason strings
  (`"unknown_key"`, `"bad_signature"`/grouped, `"replay_detected"`) — only the
  previously-generic decrypt-failure case gains a new, specific reason.
- An ADR — this extends the existing task-097 sentinel-classification pattern to a
  step it did not cover, rather than introducing a new architectural decision.
