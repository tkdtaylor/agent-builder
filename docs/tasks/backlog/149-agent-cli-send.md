# Task 149: `agent-cli send` — seal + sign a command and POST it to Telegram

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** backlog

## Goal

Add the `send` subcommand to `agent-cli` (task 148's dispatcher): read the operator
keyfile, seal the plaintext command with X25519+AEAD (operator priv → orchestrator pub),
sign the resulting envelope with the operator's Ed25519 private key, and POST it to the
Telegram Bot API's `sendMessage` endpoint — the only way to produce a command the live
`internal/channel/telegram/adapter.go` will accept, since the stock Telegram app cannot
construct a signed/sealed envelope.

## Context

Inbound direction (operator → orchestrator) mirrors `ReplyAdapter.Report` reversed:
`Seal(cmd, operatorX25519Priv, orchestratorX25519Pub)` → build
`Envelope{From:"operator", To:"orchestrator", Nonce:hex(nonce), TS:NowRFC3339(),
Payload:hex(ciphertext)}` → `Sign(env, operatorEd25519Priv)` → marshal JSON → POST to
`<baseURL>/bot<token>/sendMessage` with body `{chat_id, text}` (plus
`reply_to_message_id` when `--reply-to` is given).

**Byte-compatibility warning (load-bearing):** the `Envelope.Payload` struct doc comment
says base64, but `envelope.VerifyAndOpen` calls `hex.DecodeString(env.Payload)` and
`ReplyAdapter.Report` hex-encodes both `Nonce` and `Payload`. `send` MUST hex-encode to
match the real code path the adapter executes, not the stale comment. This is asserted
directly by round-tripping a produced envelope through the real `envelope.VerifyAndOpen`
with the adapter's exact key-role assignment: `signPub`=operator Ed25519 pub,
`recipPriv`=orchestrator X25519 priv, `senderPub`=operator X25519 pub (see
`adapter.go`'s `VerifyAndOpen` call and its `Config` field names).

Reference: `internal/envelope/envelope.go`, `internal/channel/telegram/adapter.go`,
`internal/channel/telegram/reply.go` (mirror direction), `internal/channel/telegram/reply_test.go`
(`generateReplyKeys`/`stubSendMessageServer` patterns to replicate).

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-149-01 | `BuildEnvelope(keyfile, orchXPub, cmdText)` seals with `envelope.Seal(cmd, operatorXPriv, orchXPub)`, builds `Envelope{From:"operator",To:"orchestrator",...}` with hex-encoded `Nonce`/`Payload`, signs with `envelope.Sign(env, operatorEdPriv)`; the result round-trips through `envelope.VerifyAndOpen` with the adapter's exact key-role assignment, recovering `cmdText` byte-for-byte. | must have |
| REQ-149-02 | `send` POSTs `{chat_id, text}` (text = marshalled envelope JSON) to `<baseURL>/bot<token>/sendMessage`; a non-OK API response surfaces as a non-zero exit with a clear stderr message. | must have |
| REQ-149-03 | `--reply-to <msgID>` sets `reply_to_message_id` (positive integer) in the POST body when given; omitted entirely when not given; non-positive/non-integer values are a usage error (exit 2, no HTTP call made). | must have |
| REQ-149-04 | Bot token and operator private keys never appear in logs/stdout/stderr; the raw wire POST body never contains the plaintext command text unencrypted. | must have |
| REQ-149-05 | Keyfile read failures (missing file, malformed JSON, malformed hex field) fail closed with a clear, non-panic error and make zero HTTP calls. | must have |
| REQ-149-06 | Empty (or whitespace-only) command text is rejected before sealing/sending — usage error, exit 2, zero HTTP calls. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/149-agent-cli-send-test-spec.md` exists (written first — 2026-07-01)
- [ ] Task 148 merged (`internal/agentcli` package, dispatcher scaffold, keyfile JSON shape exist)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-149-01] TC-149-01: produced envelope round-trips through real `envelope.VerifyAndOpen` with the adapter's exact key roles, recovering the exact command text (including a multi-word goal-spec variant).
- [ ] [REQ-149-01] TC-149-02: `Payload`/`Nonce` are pinned as hex-encoded (not base64), matching `VerifyAndOpen`'s actual decode call.
- [ ] [REQ-149-02] TC-149-03: exactly one POST to `<baseURL>/bot<token>/sendMessage`, body `{chat_id, text}`, `text` parses as `Envelope{From:"operator",To:"orchestrator"}`; non-OK stub response surfaces as error + non-zero exit.
- [ ] [REQ-149-03] TC-149-04: `--reply-to 555` → body has `reply_to_message_id:555`; omitted → key absent entirely (not zero-valued).
- [ ] [REQ-149-03] TC-149-05: `--reply-to abc|-1|0` all exit 2, zero HTTP calls; `--reply-to 1` succeeds.
- [ ] [REQ-149-04] TC-149-06: token sentinel and operator private keys absent from combined stdout+stderr+logs; raw captured POST body bytes never contain the plaintext command text.
- [ ] [REQ-149-05] TC-149-07: missing/malformed keyfile and malformed hex field all fail closed, non-panic stderr, zero HTTP calls.
- [ ] [REQ-149-06] TC-149-08: empty/whitespace-only command text exits 2, zero HTTP calls.

## Verification plan

- **Highest level achievable now: L2/L3.** No live Telegram bot token is available for
  this task; the Bot API is stubbed with `httptest.NewServer` per the existing
  `reply_test.go` pattern. L5/L6 (a real bot token + live `sendMessage` call observed by
  the operator, and the live adapter accepting the produced envelope) is a follow-on once
  a token is provisioned — track it as a note in the verify commit, not a blocker for
  merging this task.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/agentcli/...
  make check
  ```
  Expected: all TC-149-01..08 pass; `make check` → `All checks passed.`
- **Follow-on L6 (deferred, needs a real bot token):** run `agent-cli send --keyfile
  <kf> --token <real> --chat-id <real> "status"` against the real Telegram API and confirm
  the message appears in the target chat as ciphertext; separately confirm the live
  orchestrator's `telegram.Adapter` (configured with the matching env block from task 148)
  accepts and processes it.

## Out of scope

- Live bot token acquisition/provisioning.
- Multi-message batching or rate-limit handling beyond surfacing the API's own error.
- Interactive/REPL mode — `send` is a single invocation, single command.

## Dependencies

- **Blocks on:** task 148 (`internal/agentcli` dispatcher + keyfile shape).
- **Blocks:** none. Independent of task 150 (`reply-open`) — no shared code path beyond
  the task 148 scaffold.
