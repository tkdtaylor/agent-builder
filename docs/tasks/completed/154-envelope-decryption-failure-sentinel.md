# Task 154: classify AEAD/decryption failures with a sentinel in `internal/envelope`

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** completed

## Goal

Add an exported `ErrDecryptionFailed` sentinel to `internal/envelope`, wrap `Open`'s
AEAD-failure return with it, and switch the three existing consumers of
`envelope.VerifyAndOpen`'s error — `internal/channel/telegram`'s adapter classifier,
`internal/channel/worker`'s `Receiver.classify`, and `examples/agent-cli`'s
`reply-open` — from their current generic fallback / else-branch heuristic to an
explicit `errors.Is(err, envelope.ErrDecryptionFailed)` check. This closes the one gap
task 097's sentinel-classification pattern did not cover: the final decrypt step.

## Context

**Root cause (found during task 150 spec-verification):**
`internal/envelope/confidentiality.go`'s `Open` returns a bare, unwrapped
`errors.New("authentication failed: nacl/box decrypt returned false")` on AEAD
failure. This error matches NONE of the four existing sentinels (`ErrUnknownKey`,
`ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp` — `internal/envelope/envelope.go:44-48`).
Consequences, found across three independent consumers of `VerifyAndOpen`'s error:

- `internal/channel/telegram/adapter.go` (task 097): the classifier's `errors.Is`
  chain falls through to the generic `"envelope_rejected"` audit reason for every
  decrypt failure.
- `internal/channel/worker/transport.go` (task 083/098): `Receiver.classify` has the
  identical generic `"envelope_rejected"` fallback for the same underlying gap — a
  second, independent consumer that would otherwise stay inconsistent with the
  adapter fix if left untouched.
- `examples/agent-cli/main.go` (task 150, `reply-open`): classifies decrypt failures
  via an `else`-branch heuristic — "any non-sentinel `VerifyAndOpen` error reaching
  this branch is, by elimination, a decrypt failure" (verified correct today by task
  150's spec-verifier, but fragile: it silently misclassifies if `VerifyAndOpen` ever
  grows a new failure mode between the replay check and `Open`).

This is a defense-in-depth completeness gap in a security-critical leaf. Task 097
established the sentinel-classification pattern for `Verify` and the replay check;
this task extends the identical pattern to the `Open`/decrypt step.

**Blast radius note:** this task touches four packages across three module
boundaries: `internal/envelope` (security-critical shared leaf, F-007 fitness-gated),
`internal/channel/telegram` (F-0xx fitness-gated), `internal/channel/worker` (F-0xx
`fitness-worker-transport-isolation`-gated), and `examples/agent-cli`. This is wider
than the project's usual "at most two modules" guideline, but it is ONE coherent
responsibility — classify decrypt failures end-to-end, consistently, everywhere the
existing sentinel pattern is already used — not four unrelated changes bundled
together. Each edit is small (one new sentinel definition, one `%w` wrap, three
one-line `errors.Is` branch additions). Take care editing `internal/envelope`: it is
imported by both `internal/channel/telegram` and `internal/channel/worker`, and the
F-007/worker-transport-isolation fitness checks assert it does NOT import back into
`internal/supervisor` — adding an exported error value does not change the import
graph, but re-run both fitness checks after the edit to confirm.

**Reference:**
- `internal/envelope/confidentiality.go` (`Open`)
- `internal/envelope/envelope.go` (existing sentinel block, `VerifyAndOpen`)
- `internal/channel/telegram/adapter.go` (rejection classifier, ~line 188-197)
- `internal/channel/worker/transport.go` (`Receiver.classify`, ~line 272-287)
- `examples/agent-cli/main.go` (`reply-open`'s error branch, ~line 408-424)
- `docs/tasks/completed/097-telegram-security-hardening.md` and its test spec — the
  precedent this task extends

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-154-01 | `internal/envelope` exports `ErrDecryptionFailed`. `Open`'s AEAD-failure error is wrapped with `%w` so `errors.Is(err, envelope.ErrDecryptionFailed)` is `true` both for a direct `Open` call and through the full `VerifyAndOpen` call chain when only the final decrypt step fails. The existing descriptive message text is preserved. | must have |
| REQ-154-02 | The malformed-hex decode failure paths inside `VerifyAndOpen` (bad `Nonce` hex, bad `Payload` hex) are classified consistently — either both folded into `ErrDecryptionFailed`, or both mapped to one distinct, documented class. Whichever choice is made is stated in this task's implementation notes and applied identically to both decode sites. | must have |
| REQ-154-03 | The four pre-existing sentinels (`ErrUnknownKey`, `ErrBadSignature`, `ErrReplay`, `ErrStaleTimestamp`) continue to classify their own scenarios correctly and do not cross-match `ErrDecryptionFailed` (and vice versa). | must have |
| REQ-154-04 | `internal/channel/telegram`'s adapter classifier gains an `errors.Is(err, envelope.ErrDecryptionFailed)` branch, placed before the generic fallback, mapping to a specific audit reason (`"decryption_failed"`). Any existing adapter test asserting the old generic reason for a decrypt failure is found and updated. | must have |
| REQ-154-05 | `internal/channel/worker`'s `Receiver.classify` gains the identical `errors.Is(err, envelope.ErrDecryptionFailed)` branch mapping to the same `"decryption_failed"` reason string, keeping the two independent consumers consistent. | must have |
| REQ-154-06 | `examples/agent-cli`'s `reply-open` replaces its else-branch decrypt heuristic with an explicit `errors.Is(err, envelope.ErrDecryptionFailed)` branch printing `"error: decryption failed\n"` (unchanged user-visible text); a safe generic fallback remains for any error that still reaches the `else` after the new branch (defense against a future unclassified failure mode). | must have |
| REQ-154-07 | All pre-existing tests in `internal/envelope`, `internal/channel/telegram`, `internal/channel/worker`, and `examples/agent-cli` continue to pass, aside from the specific assertions intentionally updated by REQ-154-04/05/06. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/154-envelope-decryption-failure-sentinel-test-spec.md` exists (written first — 2026-07-01)
- [x] Task 097 merged (`internal/envelope` sentinel pattern + adapter classifier established)
- [x] Task 083/098 merged (`internal/channel/worker` `Receiver.classify` established)
- [x] Task 150 merged (`examples/agent-cli reply-open` heuristic in place — this task replaces it)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [x] [REQ-154-01] TC-154-01: `errors.Is(err, envelope.ErrDecryptionFailed)` is `true` for a direct `Open` AEAD failure (wrong recipient key); descriptive message text preserved.
- [x] [REQ-154-01] TC-154-02: `errors.Is(err, envelope.ErrDecryptionFailed)` is `true` through the full `VerifyAndOpen` chain when only the `Open` step fails; the other four sentinels do NOT match this same error.
- [x] [REQ-154-02] TC-154-03: malformed-hex `Nonce` and malformed-hex `Payload` are classified identically to each other, per whichever choice this task documents.
- [x] [REQ-154-03] TC-154-04: the four pre-existing sentinel scenarios (unknown key, bad signature, replay, stale timestamp) each match only their own sentinel — no cross-contamination with `ErrDecryptionFailed` or each other.
- [x] [REQ-154-04] TC-154-05: a decrypt-failing inbound Telegram envelope produces exactly one `"decryption_failed"` audit reason (not `"envelope_rejected"`); any pre-existing adapter test asserting the old generic reason is located and updated.
- [x] [REQ-154-05] TC-154-06: a decrypt-failing inbound envelope through `internal/channel/worker`'s `Receiver` produces the same `"decryption_failed"` `Detail.Reason` via the audit sink.
- [x] [REQ-154-06] TC-154-07: `reply-open`'s wrong-opener-key case still prints `"decryption failed"` to stderr (exit 1, empty stdout) and now does so via an explicit `errors.Is(err, envelope.ErrDecryptionFailed)` branch in source, not the else-branch heuristic.
- [x] [REQ-154-07] TC-154-08: `go test -race -count=1 ./internal/envelope/... ./internal/channel/telegram/... ./internal/channel/worker/... ./examples/agent-cli/...` passes in full; `make check` passes; `make fitness-envelope-isolation` and `make fitness-worker-transport-isolation` both still PASS.

## Verification plan

- **Highest level achievable:** L2/L3 — no live bot token; no runtime-observable
  surface beyond unit tests and the fitness gates. The new audit reason is
  runtime-observable via the stub audit sink at L2 in both the adapter and the worker
  transport tests. No L5/L6 required.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/envelope/...
  go test -race -count=1 ./internal/channel/telegram/...
  go test -race -count=1 ./internal/channel/worker/...
  go test -race -count=1 ./examples/agent-cli/...
  ```
  Expected: all four packages `ok`.
- **L3 isolation checks:**
  ```
  make fitness-envelope-isolation
  make fitness-worker-transport-isolation
  ```
  Expected: both existing `PASS ...` lines, unchanged text.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md` — the `Receiver`/sentinel block (~line 967-990) documents
  the reject-reason classification set (`bad_signature`, `unknown_key`,
  `replay_detected`, `role_mismatch`); add `decryption_failed` to that documented set
  and note the new `envelope.ErrDecryptionFailed` sentinel alongside `ErrRoleMismatch`.
- `docs/spec/data-model.md` — the `audit.EventDetail.Reason` field's example value list
  (~line 710: `"unknown_key"`, `"replay_detected"`, `"armor_blocked"`) gains
  `"decryption_failed"`.
- No `docs/spec/behaviors.md` change — that file does not currently document the
  channel-reject reason set at all; the classification detail lives in
  `interfaces.md`/`data-model.md` only.

## Implementation notes

**Malformed-hex classification decision:**

Malformed-hex decode failures (bad hex in `Nonce` or `Payload` fields during `VerifyAndOpen`) 
are NOT wrapped as `ErrDecryptionFailed`. They return bare errors from `hex.DecodeString()` 
and are classified as unclassified fallback `"envelope_rejected"` by the audit classifiers.

**Rationale:** These are input-validation failures that occur BEFORE the decrypt step. 
Hex parsing happens before nonce/payload are used in the AEAD operation, so conflating them 
with actual AEAD decrypt failures would be semantically inaccurate. Keeping them bare and 
falling back to `"envelope_rejected"` is consistent and honest — they are malformed input, 
not decrypt failures. Both `Nonce` and `Payload` malformed-hex cases are handled identically 
and consistently (TC-154-03 verifies this).

## Out of scope

- Any change to `Seal`'s signature or behavior.
- Rate-limiting, replay-store persistence, or key rotation.
- A live Telegram bot or live worker-transport integration run (L6) — no
  runtime-observable surface beyond the audit sink exists for this change.
- Renaming or regrouping the existing four sentinels' audit reason strings.
- An ADR. This extends the existing task-097 sentinel-classification pattern to a step
  it did not cover; it is not a new architectural decision. (Recommend not writing
  one — flag for the main session if it disagrees.)

## Dependencies

- **Blocks on:** task 097 (sentinel pattern + adapter classifier), task 083/098
  (`internal/channel/worker` `Receiver.classify`), task 150 (`reply-open` heuristic) —
  all three already merged.
- **Blocks:** none.
