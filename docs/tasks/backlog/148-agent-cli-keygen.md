# Task 148: `agent-cli keygen` — operator + orchestrator keypair generation

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** backlog

## Goal

Stand up the new `cmd/agent-cli` entrypoint and its `internal/agentcli` package (the
operator's laptop-side Telegram client — see the feature brief for full context), starting
with the `keygen` subcommand: generate the operator's Ed25519 (signing) and X25519
(sealing) keypairs plus the orchestrator's Ed25519 and X25519 keypairs, emit a paste-ready
`AGENT_BUILDER_TELEGRAM_*` env block for the orchestrator side, and write a private,
0600-permissioned operator keyfile holding the secrets the `send` (task 149) and
`reply-open` (task 150) subcommands need.

This task also lays the dispatcher scaffold (`Main(Config)` mirroring `internal/cli`'s
shape) that tasks 149/150 add subcommands to — it is the foundation task for the whole
feature.

## Context

The Telegram inbound adapter (`internal/channel/telegram/adapter.go`) only accepts
commands that are Ed25519-signed and X25519+AEAD-sealed `envelope.Envelope`s. There is no
existing tooling to generate the four keypairs this scheme requires (operator
Ed25519+X25519, orchestrator Ed25519+X25519) or to hand them to the orchestrator's env-var
based configuration (`docs/spec/configuration.md` `AGENT_BUILDER_TELEGRAM_*` rows). This
task builds that tooling using `internal/envelope.GenerateKeyPair` (X25519) and stdlib
`crypto/ed25519.GenerateKey` (Ed25519) exclusively — no new crypto is written.

Reference: `internal/envelope/envelope.go`, `internal/envelope/confidentiality.go`,
`docs/spec/configuration.md` lines documenting `AGENT_BUILDER_TELEGRAM_SIGNING_KEY`,
`_X25519_PUB`, `_ORCH_PRIV`, `_ORCH_ED_PRIV`, `_OP_X25519_PUB`, `_BOT_TOKEN`, `_BASE_URL`,
`_CHAT_ID`.

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-148-01 | `GenerateKeys()` produces operator Ed25519 (32/64B), operator X25519 (32B/32B), orchestrator Ed25519 (32/64B), orchestrator X25519 (32B/32B) keypairs using `envelope.GenerateKeyPair` and `ed25519.GenerateKey(rand.Reader)` exclusively (no hand-rolled crypto). | must have |
| REQ-148-02 | `RenderEnvBlock(keys)` emits a paste-ready block of the seven `AGENT_BUILDER_TELEGRAM_*` variables the orchestrator consumes (`_SIGNING_KEY`, `_X25519_PUB`, `_ORCH_PRIV`, `_ORCH_ED_PRIV`, `_OP_X25519_PUB` populated from generated keys; `_BOT_TOKEN`, `_BASE_URL`, `_CHAT_ID` as placeholder lines), hex-encoded, and never includes operator PRIVATE key material. | must have |
| REQ-148-03 | `WriteKeyfile(path, keys)` writes a JSON keyfile (operator Ed25519 priv, operator X25519 priv, orchestrator Ed25519 pub, orchestrator X25519 pub) with file mode 0600; never includes orchestrator PRIVATE keys. | must have |
| REQ-148-04 | `agent-cli keygen --keyfile <path> [--force]` CLI subcommand: mandatory `--keyfile`, refuses to overwrite an existing file without `--force`, prints the env block to stdout and a one-line non-secret confirmation to stderr, exit 0 on success / exit 2 on usage error. | must have |
| REQ-148-05 | No secret material (operator or orchestrator private keys, hex or base64) appears ambiguously mixed into stdout/stderr; the orchestrator-paste env block is visually separated from the human confirmation line by a labeled banner. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/148-agent-cli-keygen-test-spec.md` exists (written first — 2026-07-01)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-148-01] TC-148-01: `GenerateKeys()` returns all four keypairs at correct byte lengths; two calls produce different key material.
- [ ] [REQ-148-01] TC-148-02: source inspection confirms only `envelope.GenerateKeyPair`/`ed25519.GenerateKey(rand.Reader)` are used.
- [ ] [REQ-148-02] TC-148-03: env block contains all seven variables with correct hex lengths; `_X25519_PUB` and `_OP_X25519_PUB` are byte-identical to `OperatorXPub`.
- [ ] [REQ-148-02] TC-148-04: env block never contains `OperatorEdPriv`/`OperatorXPriv` (hex or base64).
- [ ] [REQ-148-03] TC-148-05: keyfile has mode 0600; JSON contains exactly `OperatorEdPriv`, `OperatorXPriv`, `OrchEdPub`, `OrchXPub` at correct lengths; write to a missing parent dir fails cleanly.
- [ ] [REQ-148-03] TC-148-06: keyfile never contains `OrchEdPriv`/`OrchXPriv` (hex or base64).
- [ ] [REQ-148-04] TC-148-07: `agent-cli keygen --keyfile <path>` exits 0, stdout has the env block, keyfile exists at 0600; no `--keyfile` exits 2.
- [ ] [REQ-148-04] TC-148-08: second `keygen --keyfile <same path>` without `--force` exits non-zero and leaves the original file byte-unchanged; `--force` overwrites successfully.
- [ ] [REQ-148-05] TC-148-09: no private-key encoding appears anywhere in combined stdout+stderr; a labeled banner separates the paste-ready block from the confirmation line.

## Verification plan

- **Highest level achievable now: L2/L3.** `keygen` has no external network dependency — it
  is pure local key generation + file I/O, so unit tests fully exercise the runtime-visible
  behavior (stdout/stderr/exit code/file permissions). No live Telegram bot token is
  available for this task, so L5/L6 (live bot) is out of scope here and deferred to a
  follow-on once a token exists.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/agentcli/...
  make check
  ```
  Expected: all TC-148-01..09 pass; `make check` → `All checks passed.`
- **Runtime observation (L6-lite, this host, no live bot needed):** build and run
  `go run ./cmd/agent-cli keygen --keyfile /tmp/agent-cli-demo/operator.json` and paste the
  quoted stdout + `ls -l` of the keyfile into the task's verify commit.

## Out of scope

- `send` and `reply-open` subcommands — tasks 149/150.
- Key rotation or re-issuing keys for an already-provisioned bot.
- Distributing the orchestrator env block automatically (e.g. writing to a remote `.env`
  file, secret manager) — the operator pastes it manually.
- Multi-operator / multi-chat key management — v1 is single-operator, single-chat.

## Dependencies

- **Blocks on:** none — `internal/envelope` (task 096) is already complete.
- **Blocks:** task 149 (`send`), task 150 (`reply-open`) — both add subcommands to this
  task's dispatcher and read the keyfile shape this task defines.
