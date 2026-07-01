# Task 150: `agent-cli reply-open` — decrypt + verify a sealed outbound reply envelope

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** backlog

## Goal

Add the `reply-open` subcommand to `examples/agent-cli` (task 148's dispatcher): take a reply
envelope JSON (file, stdin, or inline flag) as produced by the orchestrator's
`telegram.ReplyAdapter.Report`, verify it with the orchestrator's Ed25519 public key,
open it with the operator's X25519 private key + the orchestrator's X25519 public key,
and print the recovered plaintext. This is how an operator reads the agent's replies,
which appear in the Telegram chat as ciphertext.

## Context

Outbound reply direction (orchestrator → operator) per `internal/channel/telegram/reply.go`:
sealed `orchestratorX25519Priv → operatorX25519Pub`, signed with the orchestrator's
Ed25519 private key, `From:"orchestrator"`, `To:"operator"`. So `reply-open` verifies with
the **orchestrator's Ed25519 PUB** and opens with **operator X25519 priv + orchestrator
X25519 pub** — the exact reverse key-role assignment from task 149's `send` path. Getting
this backwards silently fails every decrypt with an opaque error, so the role assignment
is pinned by a dedicated test case (TC-150-02) in addition to the round-trip test.

**Design constraint (do not build a poller):** the bot's outbound replies appear in the
chat as ciphertext but are NOT returned by the bot's own `getUpdates` (that endpoint only
returns messages directed to the bot, which the orchestrator itself polls — running a
second poller on the same bot token would race/conflict with the orchestrator's own
polling loop). `reply-open` therefore takes the reply envelope JSON as an explicit input
(pasted from the Telegram app, or saved to a file) — it does NOT poll `getUpdates` and
makes no network calls at all.

Reference: `internal/channel/telegram/reply.go`, `internal/envelope/envelope.go`
(`VerifyAndOpen` mandatory ordering doc), `internal/channel/telegram/reply_test.go`
(`generateReplyKeys`/`stubSendMessageServer`/`TestTC098_03_RoundTrip` patterns to
replicate for the reverse direction).

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-150-01 | `reply-open` decrypts a real `ReplyAdapter`-shaped envelope byte-for-byte, using the correct key-role assignment: `signPub`=orchestrator Ed25519 pub, `recipPriv`=operator X25519 priv, `senderPub`=orchestrator X25519 pub (all sourced from the task 148 keyfile). | must have |
| REQ-150-02 | Accepts envelope JSON from a positional file argument, from stdin, or from an inline `--envelope` flag (exactly one source; both file-arg and `--envelope` given is a usage error); bad signature, wrong key, and tampered ciphertext all fail closed with a classified error and empty stdout — never partial/garbage plaintext; a second invocation against the same saved envelope succeeds identically (no persistent replay-rejection state across process invocations). | must have |
| REQ-150-03 | `reply-open` makes zero network calls — no `getUpdates` polling, no `baseURL`/`botToken` parameters anywhere in its code path. | must have |
| REQ-150-04 | Operator X25519 private key and orchestrator public keys never appear in logs (recovered plaintext on stdout is the intended, expected output — not a leak). | must have |
| REQ-150-05 | Missing/malformed keyfile fails closed with a clear, non-panic error; zero network calls. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/150-agent-cli-reply-open-test-spec.md` exists (written first — 2026-07-01)
- [ ] Task 148 merged (`examples/agent-cli` package, dispatcher scaffold, keyfile JSON shape exist)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-150-01] TC-150-01: decrypts a real `ReplyAdapter.Report`-emitted envelope byte-for-byte; stdout is exactly the recovered plaintext (+ optional trailing newline).
- [ ] [REQ-150-01] TC-150-02: source/behavioral assertion that `signPub`/`recipPriv`/`senderPub` are assigned in the correct (reverse-of-`send`) roles.
- [ ] [REQ-150-02] TC-150-03: file-arg, stdin, and `--envelope` inputs all produce identical output; both file-arg and `--envelope` together is a usage error.
- [ ] [REQ-150-02] TC-150-04: tampered payload, wrong trusted signer, and wrong opening key all exit non-zero with a classified error and empty stdout; malformed JSON also fails with a distinct "malformed envelope" error.
- [ ] [REQ-150-02] TC-150-05: decrypting the same envelope file twice in two separate invocations both succeed identically (no persistent replay state).
- [ ] [REQ-150-03] TC-150-06: source inspection confirms no `net/http`/`getUpdates` reference anywhere in the `reply-open` code path.
- [ ] [REQ-150-04] TC-150-07: operator X25519 priv and orchestrator pub keys (hex/base64) absent from captured log output.
- [ ] [REQ-150-05] TC-150-08: missing/malformed keyfile exits non-zero, non-panic stderr, empty stdout.

## Verification plan

- **Highest level achievable now: L2/L3.** `reply-open` is a pure local decrypt — no
  network calls at all by design (REQ-150-03), so unit tests fully exercise the
  runtime-visible behavior. There is no live-bot L5/L6 step for this subcommand
  specifically (unlike `send`, there's no API call to observe); the meaningful "live"
  check is that a real orchestrator-emitted reply decrypts correctly, which the L6-lite
  runtime observation below exercises without needing a bot token.
- **Harness command:**
  ```
  go test -race -count=1 ./examples/agent-cli/...
  make check
  ```
  Expected: all TC-150-01..08 pass; `make check` → `All checks passed.`
- **Runtime observation (L6-lite, this host, no live bot needed):** generate a keyfile via
  task 148's `keygen`, construct a real `telegram.ReplyAdapter` in a small throwaway Go
  program (or reuse the unit test harness) to emit one real sealed/signed reply envelope
  to a file, then run `go run ./examples/agent-cli reply-open --keyfile <kf> <envelope-file>`
  and confirm the exact plaintext prints to stdout.

## Out of scope

- Polling `getUpdates` or any live-bot integration — explicitly excluded by design (see
  Context).
- Batch-decrypting multiple saved envelopes in one invocation.
- A persistent replay-rejection store across invocations (each invocation is a fresh,
  independent decrypt — see REQ-150-02 and TC-150-05).

## Dependencies

- **Blocks on:** task 148 (`examples/agent-cli` dispatcher + keyfile shape).
- **Blocks:** none. Independent of task 149 (`send`) — no shared code path beyond the
  task 148 scaffold; the two can be implemented in either order or in parallel once 148
  is merged.
