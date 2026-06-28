# Test spec — Task 098: Outbound channel Reporter seam

**Linked task:** `docs/tasks/backlog/098-outbound-reporter-seam.md`
**Written:** 2026-06-28
**Status:** ready

## Context

The Telegram channel is **inbound-only** today. `internal/channel/telegram`
(`adapter.go`) implements only `supervisor.GoalSource.Next()` — it pulls a goal
**in** (poll `getUpdates` → `envelope.VerifyAndOpen` → `armor.Guard` → deliver a
`supervisor.Task`). There is **no** method to send a message **back** to the human.

ADR 046 Drift **D-1** and §2/§3 name this gap as binding: the orchestrator (task
081) needs an outbound seam for **REQ-081-02** (solicit human approval over the
channel) and **REQ-081-04** (report the result summary over the channel). ADR 046 §2
names it a **`Reporter`** seam — `Reporter { Report(ctx, text string) error }` — and
mandates that the Telegram concrete seal+sign the reply as an `internal/envelope`
`Envelope` (ADR 045): "implemented by a Telegram *outbound* adapter (bot
`sendMessage`, with the **same envelope encrypt+sign** as inbound per ADR 045, so
replies are confidential too)." The sprint plan reserves this as **task 098** ("OQ-11
— outbound channel transport + reply-envelope wiring", `099`-blocked-on note in
`docs/plans/sprints/orchestrator-decomposition.md`).

This task builds **only the seam + the Telegram outbound concrete + a fake** — it does
**not** wire the orchestrator's *use* of the Reporter (that is task 081).

### The outbound path is the mirror of the inbound path

The inbound adapter does **verify → replay-check → open** (`envelope.VerifyAndOpen`,
which decomposes to `Verify` then `cache.Check` then `Open`). The outbound path is the
exact mirror: **seal → sign**. Concretely, to produce a reply the orchestrator is the
*sender* and the operator is the *recipient*, so the keypairs swap roles relative to
inbound:

| Role | Inbound (operator → orchestrator) | Outbound reply (orchestrator → operator) |
|------|-----------------------------------|------------------------------------------|
| Ed25519 signer | operator signs | **orchestrator signs** (orchestrator Ed25519 private key) |
| Ed25519 verifier (trusted key) | adapter trusts operator's Ed25519 pub | operator's inbound-verify path trusts **orchestrator's Ed25519 pub** |
| X25519 seal (sender priv) | operator's X25519 priv | **orchestrator's X25519 priv** |
| X25519 seal (recipient pub) | orchestrator's X25519 pub | **operator's X25519 pub** |

The envelope produced by `Report(text)` must therefore be **openable+verifiable by the
inbound `envelope.VerifyAndOpen` path** when that path is configured with the
complementary keys (orchestrator Ed25519 pub as trusted-signing-key; operator X25519
priv as recipient; orchestrator X25519 pub as sender). This round-trip is the
load-bearing assertion of the spec (TC-098-03): a reply the channel emits is a
well-formed, signed, sealed `Envelope` that the *same crypto* used inbound accepts.

### Reporter interface home package — decision

`supervisor.GoalSource` lives in `internal/supervisor` (the inbound seam). The
`Reporter` interface is its **outbound symmetric counterpart** and is defined in the
**same package: `internal/supervisor`**. Justification:

- **Symmetry / discoverability.** Inbound seam (`GoalSource`) and outbound seam
  (`Reporter`) sitting side-by-side in `internal/supervisor` is the obvious, greppable
  home — the orchestrator already imports `internal/supervisor` for `Task`/`GoalSource`.
- **Leaf-isolation is preserved by construction.** The `Reporter` *interface* has a
  pure-stdlib signature (`Report(ctx context.Context, text string) error`). Adding an
  interface declaration with a `context`+`string`+`error` signature drags **no** new
  import into `internal/supervisor` — interfaces are not the concrete. The crypto
  (`internal/envelope`) lives **only** in the Telegram concrete
  (`internal/channel/telegram`), exactly as the inbound adapter does today. F-003
  (supervisor isolation) and F-007 (`internal/envelope` absent from the supervisor
  graph) both continue to hold — the supervisor still imports no crypto, no executor,
  no LLM. TC-098-06 / TC-098-07 assert this.
- **Why not `internal/orchestrator`.** Defining the seam in the consumer would force
  the Telegram concrete (a channel package) to import `internal/orchestrator`, inverting
  the dependency direction (channel→orchestrator) and entangling the channel with the
  coordination layer. The seam belongs with the other channel-facing seam, in
  `internal/supervisor`.
- **Why not a new shared package.** A third "seams" package is premature — there is
  exactly one outbound consumer (the orchestrator) and the symmetric home already
  exists. (Project rule: no abstraction until the 2nd concrete use case demands it.)

**Interface signature (decided):**

```go
// In internal/supervisor — symmetric outbound counterpart to GoalSource.
// Reporter is the seam for sending a message back to the human over the channel
// (approval solicitation, result summary). text is rendered at the channel edge
// (ADR 046 §2: typed result in the core, plain text at the boundary).
type Reporter interface {
    Report(ctx context.Context, text string) error
}
```

## Requirements coverage

| Req ID     | Description                                                                                                   | Test cases                       |
|------------|---------------------------------------------------------------------------------------------------------------|----------------------------------|
| REQ-098-01 | `supervisor.Reporter` interface defined (`Report(ctx, text string) error`) as the symmetric outbound seam     | TC-098-01, TC-098-05             |
| REQ-098-02 | Telegram outbound concrete: `Report(text)` seals+signs the reply as an `internal/envelope.Envelope` (ADR 045) before POSTing `sendMessage` | TC-098-02, TC-098-03 |
| REQ-098-03 | The emitted reply envelope round-trips: the inbound `envelope.VerifyAndOpen` path (complementary keys) accepts it and recovers the exact reported text | TC-098-03 |
| REQ-098-04 | A tampered reply envelope (payload or signature mutated in transit) FAILS the inbound verify/open path        | TC-098-04                        |
| REQ-098-05 | An in-memory fake Reporter is provided (captures reported text) for 081's L2 tests; both concrete and fake satisfy `supervisor.Reporter` | TC-098-05 |
| REQ-098-06 | Outbound concrete never leaks bot token / private keys to logs; `sendMessage` POST carries only the sealed envelope (no plaintext on the wire) | TC-098-02, TC-098-08 |
| REQ-098-07 | Leaf-isolation: the Telegram outbound concrete drags no crypto into `internal/supervisor`'s graph; F-003 and F-007 still pass; a `make fitness` run is green | TC-098-06, TC-098-07 |

## Pre-implementation checklist

- [x] ADR 046 accepted (2026-06-27) — D-1 names this seam; §2 mandates ADR-045 reply envelope
- [x] ADR 045 accepted — Ed25519 sign + X25519/AEAD seal is the reply treatment
- [x] Task 096 (`internal/envelope`) merged ✅ — `Seal`/`Sign`/`VerifyAndOpen` available
- [x] Task 080 (Telegram inbound adapter) merged ✅ — outbound mirrors its crypto + HTTP shape
- [x] Reporter interface home package decided (`internal/supervisor`, symmetric with `GoalSource`)

---

## Test cases

### TC-098-01 — `supervisor.Reporter` interface exists with the decided signature

- **Requirement:** REQ-098-01
- **Level:** L2 (compile-time + unit assertion)
- **Test file:** `internal/supervisor/reporter_test.go` (or wherever the interface lands)

**Setup:**
Declare a local type with method `Report(ctx context.Context, text string) error` and
assign it to a `supervisor.Reporter` variable.

**Input:**
```go
var _ supervisor.Reporter = stubReporter{}
```

**Expected output:**
- The file compiles (the interface is named `supervisor.Reporter` and its single
  method is `Report(context.Context, string) error`).
- A direct call `r.Report(context.Background(), "hello")` returns the stub's error
  (nil for the happy stub) — i.e. the method is invokable with that exact signature.

---

### TC-098-02 — `Report(text)` POSTs a `sendMessage` carrying a sealed envelope, not plaintext

- **Requirement:** REQ-098-02, REQ-098-06
- **Level:** L2 (unit test with `httptest` stub Telegram server)
- **Test file:** `internal/channel/telegram/reply_test.go`

**Setup:**
- Generate the orchestrator keys (the *sender* for replies): orchestrator Ed25519
  keypair `(orchEdPub, orchEdPriv)` and orchestrator X25519 keypair `(orchXPub, orchXPriv)`.
- Generate the operator keys (the *recipient* for replies): operator X25519 keypair
  `(opXPub, opXPriv)`.
- Stand up an `httptest.NewServer` that records the request path and body of the POST
  it receives, and returns `{"ok":true,"result":{...}}`.
- Construct the outbound concrete (e.g. `telegram.NewReplyAdapter(...)` /
  `Config`) configured with: `BaseURL` = stub server URL, a sentinel `BotToken`,
  `chat_id`, the orchestrator's Ed25519 **private** key (signs), the orchestrator's
  X25519 **private** key (seals as sender), and the operator's X25519 **public** key
  (seal recipient).

**Input:**
```go
err := adapter.Report(context.Background(), "Approve plan? 2 sub-goals: docs-fix, coding-agent")
```

**Expected output:**
- `err == nil`.
- The stub server received exactly one POST whose URL path contains `sendMessage`
  (`.../bot<token>/sendMessage`).
- The request body's message-text field parses as JSON into an `envelope.Envelope`
  with non-empty `Nonce`, `TS`, `Payload`, and `Sig`.
- The literal reported string `"Approve plan? 2 sub-goals: docs-fix, coding-agent"`
  does **NOT** appear anywhere in the raw POST body (the payload is sealed ciphertext,
  hex-encoded — the plaintext must not be on the wire).

---

### TC-098-03 — Round-trip: the emitted reply envelope is accepted by the inbound `VerifyAndOpen` path and recovers the exact text

- **Requirement:** REQ-098-02, REQ-098-03
- **Level:** L2 (unit test — outbound seal+sign fed into inbound verify+open)
- **Test file:** `internal/channel/telegram/reply_test.go`

**Purpose:** This is the load-bearing assertion. It proves the outbound path is the
true mirror of the inbound path: a reply produced by `Report` is a signed, sealed
envelope that the *same crypto used for inbound goals* accepts, with the original text
recovered byte-for-byte.

**Setup:**
- Same key material as TC-098-02 (orchestrator = reply sender, operator = reply recipient).
- Capture the `envelope.Envelope` that `Report` emits (from the recorded POST body in
  TC-098-02, or via a seam that exposes the built envelope — implementation's choice,
  but the assertion is on the wire envelope).

**Input (the inbound-style verify+open, configured with complementary keys):**
```go
plaintext, err := envelope.VerifyAndOpen(
    emittedEnv,
    orchEdPub,                 // trusted signing key = the orchestrator's Ed25519 pub
    envelope.NewReplayCache(60*time.Second),
    opXPriv,                   // recipient private = operator's X25519 priv
    orchXPub,                  // sender public = orchestrator's X25519 pub
)
```

**Expected output:**
- `err == nil` (signature verifies against `orchEdPub`; nonce is fresh; AEAD opens).
- `string(plaintext) == "Approve plan? 2 sub-goals: docs-fix, coding-agent"` (byte-equal
  to the text passed to `Report`).

---

### TC-098-04 — A tampered reply envelope fails the inbound verify/open path

- **Requirement:** REQ-098-04
- **Level:** L2 (unit test)
- **Test file:** `internal/channel/telegram/reply_test.go`

**Setup:**
- Same key material and emitted envelope as TC-098-03.

**Input A (signature/body tamper):** Mutate one byte of the emitted envelope's
`Payload` field (e.g. flip a hex character / append `"00"`), keeping `Sig` as-is, then
call `envelope.VerifyAndOpen(tampered, orchEdPub, cache, opXPriv, orchXPub)`.

**Expected output A:**
- Returns a non-nil error.
- The error matches `envelope.ErrBadSignature` via `errors.Is` (the signature is over
  the canonical body including `Payload`, so a mutated payload fails verify before open).

**Input B (wrong trusted signer):** Take the *unmodified* emitted envelope and call
`VerifyAndOpen` with an **unrelated** Ed25519 public key as the trusted signing key
(not `orchEdPub`).

**Expected output B:**
- Returns a non-nil error matching `envelope.ErrBadSignature` (or `ErrUnknownKey` for a
  wrong-sized key) via `errors.Is`.
- No plaintext is returned (the forged/untrusted reply is rejected before decryption).

---

### TC-098-05 — In-memory fake Reporter captures reported text; both fake and concrete satisfy `supervisor.Reporter`

- **Requirement:** REQ-098-01, REQ-098-05
- **Level:** L2 (unit test)
- **Test file:** `internal/channel/telegram/reply_test.go` (fake may live in a small
  test-support file the orchestrator's 081 tests can import — implementation's choice;
  the assertion is behavioral)

**Setup:**
- Construct the fake Reporter (e.g. `telegram.NewFakeReporter()` or a documented
  `FakeReporter` test double).

**Input:**
```go
var _ supervisor.Reporter = (*telegram.ReplyAdapter)(nil)  // concrete satisfies seam
var _ supervisor.Reporter = (*telegram.FakeReporter)(nil)  // fake satisfies seam

fake := telegram.NewFakeReporter()
_ = fake.Report(context.Background(), "first")
_ = fake.Report(context.Background(), "second")
```

**Expected output:**
- Both compile-time assertions hold (concrete and fake both satisfy `supervisor.Reporter`).
- `fake.Reported()` (or equivalent accessor) returns `["first", "second"]` in order —
  the fake records every reported string verbatim, for 081's L2 tests to assert
  "approval solicited" / "summary reported" without a live bot.

---

### TC-098-06 — F-007 still holds: `internal/envelope` absent from `internal/supervisor`'s graph after the seam is added

- **Requirement:** REQ-098-07
- **Level:** L3 (import-graph / fitness check)
- **Harness:** `make fitness-envelope-isolation`

**Purpose:** Adding `supervisor.Reporter` to `internal/supervisor` must NOT pull
`internal/envelope` into the supervisor's import graph — the interface is pure-stdlib;
the crypto stays in the Telegram concrete.

**Input:** `make fitness-envelope-isolation`
(equivalently `go list -deps ./internal/supervisor/... | grep -F internal/envelope`
returns no output).

**Expected output:**
- `PASS fitness-envelope-isolation: internal/envelope is not in internal/supervisor's dependency graph.`
- Exit 0.

---

### TC-098-07 — F-003 still holds: supervisor graph has no executor/LLM/web after the seam is added; full gate green

- **Requirement:** REQ-098-07
- **Level:** L3 (fitness check)
- **Harness:** `make fitness-supervisor-isolation` then `make check`

**Input:**
```
make fitness-supervisor-isolation
make check
```

**Expected output:**
- `PASS fitness-supervisor-isolation: supervisor import graph contains no executor/LLM/web-fetch packages.`
- `make check` → `All checks passed.` (lint + test + the full fitness suite, including
  F-003 and F-007, all green with the new seam + concrete in the tree).

**Note on outbound-leaf isolation:** the Telegram outbound concrete lives in the same
package as the inbound adapter (`internal/channel/telegram`), which already imports
`internal/envelope` and is already outside the supervisor graph (proven by F-007). No
**new** fitness target is required for 098 — the existing F-003 + F-007 checks cover the
invariant, because the only structural change inside the supervisor's graph is a
pure-stdlib interface. (If a future task moves the outbound concrete into its own
package, mirror the F-005/F-006/F-007 pattern then; it is not warranted now — no second
use case demands it.) The task file records this decision explicitly so a reviewer does
not mistake the absence of a new target for an oversight.

---

### TC-098-08 — Bot token and private keys never appear in logs

- **Requirement:** REQ-098-06
- **Level:** L2 (unit test — log capture)
- **Test file:** `internal/channel/telegram/reply_test.go`

**Setup:**
- Configure the outbound concrete with a sentinel `BotToken = "REPLY_BOT_TOKEN_SENTINEL_98765"`,
  a real orchestrator Ed25519 private key, and a real orchestrator X25519 private key.
- Attach a `slog` logger writing to a `bytes.Buffer` at debug level.

**Input:**
```go
_ = adapter.Report(context.Background(), "summary: 2 sub-goals complete")
logOutput := logBuffer.String()
```

**Expected output:**
- `logOutput` does NOT contain the sentinel bot token.
- `logOutput` does NOT contain the hex encoding of the orchestrator X25519 private key
  bytes, nor its base64 encoding.
- `logOutput` does NOT contain the hex encoding of the orchestrator Ed25519 private
  key seed.
- `logOutput` does NOT contain a `-----BEGIN` PEM marker.
  (Mirrors TC-080-06 for the inbound adapter.)

---

## Verification plan

- **Highest level achievable:** **L2 / L3.** The outbound concrete's correctness is
  proven by unit tests with an `httptest` stub Telegram server + an envelope round-trip
  (seal+sign → inbound verify+open), and by the F-003 / F-007 import-graph fitness
  checks. There is no host-side runtime surface this task can exercise live without a
  real bot token.
- **L6 deferred (operator-run):** A live Telegram `sendMessage` round-trip against a
  real bot token (the reply actually arriving in the operator's Telegram client, and
  the operator's companion verifying the reply envelope) is **L6, operator-run**, and is
  **explicitly deferred** — same posture as task 080's inbound live-bot deferral.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/channel/telegram/... ./internal/supervisor/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/channel/telegram` and
  `ok github.com/tkdtaylor/agent-builder/internal/supervisor`.
- **L3 import-graph + fitness:**
  ```
  go list -deps ./internal/supervisor/... | grep -F internal/envelope   # no output
  make fitness-envelope-isolation
  make fitness-supervisor-isolation
  ```
  Expected: empty output for the grep; both fitness targets PASS.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- The orchestrator's **use** of the Reporter (calling `Report` to solicit approval and
  to report the result summary) — that is **task 081** (REQ-081-02, REQ-081-04). This
  task builds and proves the seam + concrete + fake only.
- Rendering a typed `PlanResult` to text — the Reporter takes a `string`; the typed
  result and its text rendering live in the orchestrator (task 081, ADR 046 §2).
- The live Telegram `sendMessage` round-trip against a real bot token — **L6,
  operator-run, deferred** (above).
- Inbound approval-message handling (the approval returning over the inbound channel) —
  that reuses the existing inbound `GoalSource` path and is wired by task 081 (ADR 046 §4).
- Key provisioning / rotation — operator's out-of-band responsibility (per ADR 045,
  task 096 out-of-scope).
</content>
</invoke>
