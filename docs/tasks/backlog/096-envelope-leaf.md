# Task 096: internal/envelope leaf package (Ed25519 sign/verify + X25519+AEAD confidentiality + ReplayCache)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Build `internal/envelope` — a pure stdlib (+`golang.org/x/crypto`) leaf package
that provides the shared cryptographic primitive for both the Telegram channel adapter
(task 080) and the orchestrator↔worker transport (task 083). It implements:

1. **Ed25519 sign/verify** over the agent-mesh-compatible `Envelope` JSON shape
   (`From`, `To`, `Nonce`, `TS`, `Payload`, `Sig`) and `signingBytes()` canonicalization
   (ADR 045 §1).
2. **X25519+AEAD confidentiality** via `nacl/box` (X25519+XSalsa20-Poly1305 from
   `golang.org/x/crypto`) — `Seal(plaintext, senderX25519Priv, recipX25519Pub)` and
   `Open(ciphertext, nonce, recipX25519Priv, senderX25519Pub)` (ADR 045 §2).
3. **ReplayCache** — both a time-freshness window (default 60 s, config knob) and a
   nonce set bounded by `2×Window` retention, with eviction of nonces older than the
   retention horizon; in-memory v1 (ADR 045 §3).

This package is the **only** place in the codebase that touches `crypto/ed25519` or
`golang.org/x/crypto/nacl`. Both task 080 and task 083 import it; it imports neither.

## Context

ADR 045 (accepted 2026-06-27) is the design authority for all decisions in this task.
Key findings driving the design:

- agent-mesh (`~/Code/Public/agent-mesh`) is `package main` — not importable as a library.
- agent-mesh exposes no `sign`/`verify` CLI filter verb — only a two-process A2A
  `serve`/`sendto` pair. Wrapping it like `internal/audit` wraps audit-trail is therefore
  not feasible for the sign/verify operation.
- The correct integration shape is to **reimplement the thin envelope over stdlib
  `crypto/ed25519`**, adopting agent-mesh's **`Envelope` JSON shape and `signingBytes()`
  canonicalization** as the wire contract, keeping the two wire-interoperable.

`golang.org/x/crypto` is a **new dependency**, approved by the project owner 2026-06-27
(ADR 045 owner sign-off). Do not add any other new dependencies.

## Requirements

| Req ID     | Description                                                                                                                                                        | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-096-01 | `Sign(env Envelope, priv ed25519.PrivateKey) (Envelope, error)` produces a signed envelope; `Verify(env Envelope, pub ed25519.PublicKey) error` verifies it; unknown key and tampered payload both return a named error. | must have |
| REQ-096-02 | `Seal(plaintext []byte, senderPriv, recipPub [32]byte) (ciphertext []byte, nonce [24]byte, err error)` and `Open(ciphertext []byte, nonce [24]byte, recipPriv, senderPub [32]byte) ([]byte, error)` using `nacl/box`; ciphertext is never equal to plaintext. | must have |
| REQ-096-03 | `ReplayCache` with configurable `Window` (default 60 s): `Check(nonce string, ts time.Time) error` rejects stale timestamps (outside `[now-W, now+W]`), rejects previously-seen nonces, retains each nonce for at most `2×Window`, and evicts nonces older than `2×Window` so the set does not grow unbounded. | must have |
| REQ-096-04 | Full round-trip (Sign+Seal → wire JSON → Unmarshal → Open+Verify) delivers the original plaintext. | must have |
| REQ-096-05 | **Leaf-purity (F-007):** `internal/envelope` imports only stdlib + `golang.org/x/crypto`; it imports NO `agent-builder/internal/*` package; it must NOT appear in `go list -deps ./internal/supervisor/...`. A new `make fitness-envelope-isolation` Makefile target asserts this. The check is added to the `fitness:` prerequisite list and documented in `docs/spec/fitness-functions.md` as F-007. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/096-envelope-leaf-test-spec.md` exists (written first — 2026-06-27)
- [x] ADR 045 accepted — all three open design questions from task 080's original spec are resolved
- [x] `golang.org/x/crypto` dependency approved (ADR 045 owner sign-off 2026-06-27)
- [ ] `go.mod` has `golang.org/x/crypto` added (`go get golang.org/x/crypto`) and `go.sum` updated before implementation begins
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-096-01] TC-096-01: `Sign` + `Verify` happy path — `Verify` returns nil; `Payload` is preserved; `Sig` is non-empty hex.
- [ ] [REQ-096-01] TC-096-02: `Verify` with the wrong public key returns a non-nil error containing `"unknown_key"` or `"bad_signature"` or `"signature"`.
- [ ] [REQ-096-01] TC-096-03: `Verify` on a tampered payload (one byte appended) returns a non-nil error.
- [ ] [REQ-096-02] TC-096-04: `Seal` produces ciphertext ≠ plaintext; `len(ciphertext) > len(plaintext)`.
- [ ] [REQ-096-02] TC-096-05: `Open` on valid ciphertext returns the original plaintext; `Open` on tampered ciphertext returns a non-nil error and no plaintext.
- [ ] [REQ-096-03] TC-096-06: `ReplayCache.Check` accepts a fresh nonce; rejects a timestamp older than `W`; rejects a timestamp newer than `W`.
- [ ] [REQ-096-03] TC-096-07: `ReplayCache.Check` accepts a nonce the first time; rejects the same nonce a second time.
- [ ] [REQ-096-03] TC-096-08: After `2×Window` the nonce set does not grow unbounded; eviction runs correctly.
- [ ] [REQ-096-04] TC-096-09: Full round-trip (Sign+Seal → JSON marshal → JSON unmarshal → Open+Verify) returns `string(got) == original plaintext`; wire JSON has all six agent-mesh keys (`from`, `to`, `nonce`, `ts`, `payload`, `sig`).
- [ ] [REQ-096-05] TC-096-10: `go list -deps ./internal/envelope/...` contains no `agent-builder/internal/` path other than `internal/envelope` itself.
- [ ] [REQ-096-05] TC-096-11: `make fitness-envelope-isolation` exits 0; `PASS fitness-envelope-isolation: ...` printed. Negative evidence: synthetic import of `internal/envelope` from `internal/supervisor` triggers FAIL + exit 1.

## Verification plan

- **Highest level achievable:** L3 — `internal/envelope` has no CLI or runtime-observable
  surface of its own; correctness is proven by unit tests and the import-graph/fitness checks.
- **Harness command:**
  ```
  go test -count=1 ./internal/envelope/...
  make fitness-envelope-isolation
  make check
  ```
  Expected:
  - `ok github.com/tkdtaylor/agent-builder/internal/envelope`
  - `PASS fitness-envelope-isolation: internal/envelope is not in internal/supervisor's dependency graph.`
  - `All checks passed.`

## Out of scope

- The Telegram bot adapter (`internal/channel/telegram`) — task 080.
- The orchestrator↔worker transport wiring — task 083.
- Key rotation.
- Persistent replay cache (in-memory v1 only; a persistent backend with memory-guard
  integration is an explicit follow-on per ADR 045 §3).
- Key-provisioning tooling (generating/distributing keypairs); out-of-band for v1.
- Wrapping the agent-mesh binary (there is no sign/verify filter verb to wrap — ADR 045 §Context).

## Dependencies

- **Blocks on (none):** This task is NOT blocked by tasks 080 or 081. It has no
  `agent-builder/internal/*` imports, so it can be implemented on any branch in
  parallel with other work.
- **Informs:** task 080 (channel adapter consumes `internal/envelope`), task 083
  (worker transport consumes `internal/envelope`).
- **New dependency to add:** `golang.org/x/crypto` (run `go get golang.org/x/crypto`
  on the task branch; update `go.mod` + `go.sum`).
