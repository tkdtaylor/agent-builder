# Task 098: Outbound channel Reporter seam

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Add the **outbound** half of the channel that does not exist today: a
`supervisor.Reporter` seam (`Report(ctx, text string) error`) plus a Telegram
**outbound** concrete that seals+signs the reply as an `internal/envelope.Envelope`
(ADR 045) and POSTs it via the bot `sendMessage` API, and an in-memory **fake**
Reporter for downstream L2 tests. The Telegram channel is inbound-only today
(`internal/channel/telegram/adapter.go` implements only
`supervisor.GoalSource.Next()`); this task builds the mirror path — **seal → sign →
send** — so the orchestrator (task 081) has a way to talk back to the human.

This task builds and proves **only the seam + concrete + fake**. The orchestrator's
*use* of the Reporter (soliciting approval, reporting the result summary) is task 081.

## Context

- **ADR 046 Drift D-1 (binding).** "The channel is inbound-only today — there is no
  outbound/report path." REQ-081-02 (solicit approval over the channel) and REQ-081-04
  (report the result summary over the channel) both require an outbound seam that does
  not exist. ADR 046 §2 names it `Reporter { Report(ctx, text string) error }`,
  "implemented by a Telegram *outbound* adapter (bot `sendMessage`, with the **same
  envelope encrypt+sign** as inbound per ADR 045, so replies are confidential too)."
  ADR 046's Consequences section makes the outbound seam a named prerequisite to be
  slotted "ahead of or inside 081"; this is that predecessor task.
- **ADR 045 (binding).** A reply is untrusted, security-sensitive output that must get
  the **same** Ed25519 sign + X25519/AEAD seal treatment as an inbound goal — an
  attacker who could forge a reply could, in the inbound direction, forge an approval.
  The outbound concrete reuses `internal/envelope` (`Seal`, `Sign`) exactly as the
  inbound adapter reuses `VerifyAndOpen`.
- **OQ-11 / sprint plan.** `docs/plans/sprints/orchestrator-decomposition.md` reserves
  ID **098** for "Outbound Reporter seam (ADR 046 D-1)" and sequences it **before** 081
  in the Arc-1 orchestrator chain (`098 → 081 → 082 → …`). OQ-11: "the channel is
  inbound-only today; the Reporter seam (task 098) must add the outbound path with
  ADR-045 encrypt+sign on replies."
- **Mirror, not new crypto.** Inbound = `VerifyAndOpen` (verify → replay-check → open).
  Outbound = the reverse (seal → sign). For replies the orchestrator is the *sender*
  and the operator is the *recipient*, so the keypairs swap roles relative to inbound
  (see the test spec's role table). The emitted reply envelope must be accepted by the
  inbound `envelope.VerifyAndOpen` path when configured with the complementary keys —
  that round-trip is the load-bearing assertion (TC-098-03).

### Reporter interface home package (decided)

The `Reporter` interface is defined in **`internal/supervisor`**, as the symmetric
outbound counterpart to `supervisor.GoalSource` (the inbound seam already living
there). Justification (full version in the test spec):

- **Symmetry / discoverability** — inbound (`GoalSource`) and outbound (`Reporter`)
  seams side-by-side; the orchestrator already imports `internal/supervisor`.
- **Leaf-isolation preserved by construction** — the interface signature is
  pure-stdlib (`Report(context.Context, string) error`), so declaring it adds **no**
  import to `internal/supervisor`. The crypto (`internal/envelope`) lives **only** in
  the Telegram concrete (`internal/channel/telegram`), exactly as the inbound adapter
  does. F-003 (supervisor isolation) and F-007 (`internal/envelope` absent from the
  supervisor graph) both continue to hold (TC-098-06, TC-098-07).
- **Not `internal/orchestrator`** — defining the seam in the consumer would force the
  channel package to import the orchestrator, inverting dependency direction.
- **Not a new shared package** — premature; one outbound consumer, and the symmetric
  home already exists (project rule: no abstraction until the 2nd concrete use case).

```go
// internal/supervisor — symmetric outbound counterpart to GoalSource.
type Reporter interface {
    Report(ctx context.Context, text string) error
}
```

## Requirements

| Req ID     | Description                                                                                                                          | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-098-01 | Define `supervisor.Reporter` (`Report(ctx, text string) error`) as the symmetric outbound seam, in `internal/supervisor`.            | must have |
| REQ-098-02 | Telegram outbound concrete: `Report(text)` seals+signs the reply as an `internal/envelope.Envelope` (ADR 045) and POSTs it via `sendMessage`. | must have |
| REQ-098-03 | The emitted reply envelope round-trips: inbound `envelope.VerifyAndOpen` (complementary keys) accepts it and recovers the exact text. | must have |
| REQ-098-04 | A tampered/forged reply envelope FAILS the inbound verify/open path (signature over canonical body; untrusted signer rejected).      | must have |
| REQ-098-05 | Provide an in-memory fake Reporter (captures reported text in order); both concrete and fake satisfy `supervisor.Reporter`.          | must have |
| REQ-098-06 | No secret leakage: bot token + private keys never logged; only the sealed envelope (no plaintext) appears on the `sendMessage` wire. | must have |
| REQ-098-07 | Leaf-isolation: the seam adds no crypto to `internal/supervisor`'s graph; F-003 and F-007 still pass; `make check` green.            | must have |

## Readiness gate

- [x] Test spec `098-outbound-reporter-seam-test-spec.md` exists (written first)
- [x] Task 096 (`internal/envelope`) merged — `Seal`/`Sign`/`VerifyAndOpen` available
- [x] Task 080 (Telegram inbound adapter) merged — outbound mirrors its crypto + HTTP shape
- [x] Reporter home package decided (`internal/supervisor`, symmetric with `GoalSource`)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-098-01] TC-098-01: `supervisor.Reporter` exists with signature
  `Report(context.Context, string) error`; a stub satisfies it and the method is
  invokable.
- [ ] [REQ-098-02 / REQ-098-06] TC-098-02: `Report(text)` POSTs exactly one
  `sendMessage` whose body's message-text parses to an `envelope.Envelope` with
  non-empty `Nonce`/`TS`/`Payload`/`Sig`; the plaintext string is **absent** from the
  raw POST body.
- [ ] [REQ-098-02 / REQ-098-03] TC-098-03: the emitted envelope, fed to
  `envelope.VerifyAndOpen` with complementary keys (orchestrator Ed25519 pub as trusted
  signer; operator X25519 priv as recipient; orchestrator X25519 pub as sender),
  returns `err == nil` and `string(plaintext) == <reported text>`.
- [ ] [REQ-098-04] TC-098-04: a payload-mutated envelope fails verify with
  `errors.Is(err, envelope.ErrBadSignature)`; an unmodified envelope verified against
  an unrelated signing key fails with `ErrBadSignature`/`ErrUnknownKey`; no plaintext
  returned in either case.
- [ ] [REQ-098-01 / REQ-098-05] TC-098-05: both `*ReplyAdapter` and `*FakeReporter`
  satisfy `supervisor.Reporter`; `fake.Reported()` returns the reported strings in
  order (`["first","second"]`).
- [ ] [REQ-098-07] TC-098-06: `make fitness-envelope-isolation` → PASS
  (`internal/envelope` not in `internal/supervisor`'s graph after the seam is added).
- [ ] [REQ-098-07] TC-098-07: `make fitness-supervisor-isolation` → PASS and
  `make check` → `All checks passed.`
- [ ] [REQ-098-06] TC-098-08: with a sentinel bot token + real private keys configured,
  debug logs contain neither the bot token, nor the hex/base64 of the X25519 priv, nor
  the hex of the Ed25519 priv seed, nor a `-----BEGIN` PEM marker.

## Verification plan

- **Highest level achievable:** **L2 / L3.** Unit tests with an `httptest` stub
  Telegram server + an envelope round-trip (seal+sign fed into inbound verify+open),
  plus F-003 / F-007 import-graph fitness checks. No host-side runtime surface is
  exercisable live without a real bot token.
- **L6 deferred (operator-run):** a live Telegram `sendMessage` round-trip against a
  real bot token — the reply arriving in the operator's client and its companion
  verifying the reply envelope — is L6, operator-run, and **explicitly deferred** (same
  posture as task 080's inbound live-bot deferral).
- **Harness command:**
  ```
  go test -count=1 ./internal/channel/telegram/... ./internal/supervisor/...
  go list -deps ./internal/supervisor/... | grep -F internal/envelope   # expect: no output
  make fitness-envelope-isolation
  make fitness-supervisor-isolation
  make check
  ```
  Expected:
  - Unit tests → `ok …/internal/channel/telegram` and `ok …/internal/supervisor`
  - `go list … grep` → no output (envelope stays out of the supervisor graph)
  - both fitness targets → PASS
  - `make check` → `All checks passed.`

**No new fitness target is required.** The outbound concrete lives in the existing
`internal/channel/telegram` package, which already imports `internal/envelope` and is
already outside the supervisor graph (F-007 covers it). The only structural change
inside the supervisor's graph is a pure-stdlib interface. F-003 + F-007 together pin
the invariant. (If a future task moves the outbound concrete into its own package,
mirror the F-005/F-006/F-007 pattern then — not warranted now.)

## Out of scope

- The orchestrator's **use** of the Reporter (calling `Report` to solicit approval and
  to report the result summary) — **task 081** (REQ-081-02, REQ-081-04).
- Rendering a typed `PlanResult` to text — the Reporter takes a `string`; the typed
  result + its text rendering live in the orchestrator (task 081, ADR 046 §2).
- The **live** Telegram `sendMessage` round-trip against a real bot token — **L6,
  operator-run, deferred**.
- Inbound approval-message handling (the approval returning over the inbound channel) —
  reuses the existing `GoalSource` path; wired by task 081 (ADR 046 §4).
- Key provisioning / rotation — operator's out-of-band responsibility (ADR 045).

## Dependencies

- Task 096 (`internal/envelope` leaf — `Seal`/`Sign`/`VerifyAndOpen`) ✅ merged
- Task 080 (Telegram inbound adapter + `supervisor.GoalSource`) ✅ merged
- Informs / unblocks: Task 081 (orchestrator core — REQ-081-02 approval over the
  channel, REQ-081-04 result summary over the channel)
</content>
