# Test Spec 148: `agent-cli keygen` — operator + orchestrator keypair generation

**Linked task:** [`docs/tasks/backlog/148-agent-cli-keygen.md`](../backlog/148-agent-cli-keygen.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-148-01 | TC-148-01, TC-148-02 | ✅ |
| REQ-148-02 | TC-148-03, TC-148-04 | ✅ |
| REQ-148-03 | TC-148-05, TC-148-06 | ✅ |
| REQ-148-04 | TC-148-07, TC-148-08 | ✅ |
| REQ-148-05 | TC-148-09 | ✅ |

## Test cases

### TC-148-01: keygen generates all four keypairs at correct sizes
- **Requirement:** REQ-148-01
- **Input:** call the package-level `GenerateKeys()` function (no I/O) directly.
- **Expected output:** returns a struct with `OperatorEdPub`/`OperatorEdPriv` (`ed25519.PublicKeySize`=32 / `ed25519.PrivateKeySize`=64), `OperatorXPub`/`OperatorXPriv` (`[32]byte`), `OrchEdPub`/`OrchEdPriv` (32/64 bytes), `OrchXPub`/`OrchXPriv` (`[32]byte`). No error.
- **Assertions:** unit test checks `len()` of every field against the expected constant.
- **Edge cases:** two consecutive calls to `GenerateKeys()` produce different key material (non-deterministic randomness — assert no field is byte-equal across the two calls).

### TC-148-02: `envelope.GenerateKeyPair`/`ed25519.GenerateKey` are the only randomness source
- **Requirement:** REQ-148-01
- **Input:** source inspection of the `keygen` implementation file.
- **Expected output:** key generation calls `envelope.GenerateKeyPair()` for X25519 pairs and `ed25519.GenerateKey(rand.Reader)` for Ed25519 pairs — no custom/reimplemented crypto.
- **Assertions:** unit test greps the implementation file for `envelope.GenerateKeyPair` and `ed25519.GenerateKey`; fails if a hand-rolled key derivation is found instead.
- **Edge cases:** none.

### TC-148-03: emitted env block contains all seven orchestrator-side variables with correct hex encodings
- **Requirement:** REQ-148-02
- **Input:** `RenderEnvBlock(keys)` given a `GenerateKeys()` result.
- **Expected output:** a string containing, each on its own `KEY=value` line: `AGENT_BUILDER_TELEGRAM_SIGNING_KEY` (hex, 64 chars = 32 bytes = OperatorEdPub), `AGENT_BUILDER_TELEGRAM_X25519_PUB` (hex, 64 chars = OperatorXPub), `AGENT_BUILDER_TELEGRAM_ORCH_PRIV` (hex, 64 chars = OrchXPriv), `AGENT_BUILDER_TELEGRAM_ORCH_ED_PRIV` (hex, 128 chars = 64 bytes = OrchEdPriv), `AGENT_BUILDER_TELEGRAM_OP_X25519_PUB` (hex, 64 chars = OperatorXPub — same value as `_X25519_PUB`, both required by the adapter/reply-adapter config surface per `docs/spec/configuration.md`). `AGENT_BUILDER_TELEGRAM_BOT_TOKEN`, `_BASE_URL`, `_CHAT_ID` appear as placeholder lines (e.g. `AGENT_BUILDER_TELEGRAM_BOT_TOKEN=<fill in>`) since keygen has no bot token to emit.
- **Assertions:** unit test parses the emitted block line-by-line into a map, asserts each hex value round-trips via `hex.DecodeString` to the expected byte length, and asserts the two `_X25519_PUB` / `_OP_X25519_PUB` values are byte-identical to each other and to `keys.OperatorXPub`.
- **Edge cases:** the block must not contain any TAB characters or shell-unsafe quoting that would break `export $(cat block)` or copy-paste into a `.env` file.

### TC-148-04: emitted env block never places operator Ed25519 private key material in the orchestrator block
- **Requirement:** REQ-148-02
- **Input:** `RenderEnvBlock(keys)` output.
- **Expected output:** the operator's Ed25519 private key (`OperatorEdPriv`) and the operator's X25519 private key (`OperatorXPriv`) never appear (hex or base64) anywhere in the env block — the orchestrator only needs operator PUBLIC keys plus its own private keys.
- **Assertions:** unit test hex/base64-encodes `OperatorEdPriv` and `OperatorXPriv` and asserts `strings.Contains(envBlock, ...)` is false for both encodings.
- **Edge cases:** none.

### TC-148-05: keyfile contains operator secrets + orchestrator public keys, written with 0600 permissions
- **Requirement:** REQ-148-03
- **Input:** `WriteKeyfile(path, keys)` to a temp file (`t.TempDir()`).
- **Expected output:** the file is created with mode `0600` (owner read/write only); its JSON contents include `OperatorEdPriv` (hex, 128 chars), `OperatorXPriv` (hex, 64 chars), `OrchEdPub` (hex, 64 chars), `OrchXPub` (hex, 64 chars) — i.e. exactly the fields the `send`/`reply-open` subcommands (tasks 149/150) need to act as the operator and to verify/open orchestrator-signed/sealed replies.
- **Assertions:** unit test calls `os.Stat` on the written file, asserts `info.Mode().Perm() == 0600`; unmarshals the JSON and asserts each of the four fields round-trips via hex decode to the expected length and byte-equals the corresponding `GenerateKeys()` field.
- **Edge cases:** `WriteKeyfile` on a path whose parent directory does not exist returns a non-nil error (fail-fast, no partial file left with wrong permissions — assert `os.Stat` on the path returns an error afterward).

### TC-148-06: keyfile omits orchestrator private keys
- **Requirement:** REQ-148-03
- **Input:** the keyfile JSON written in TC-148-05.
- **Expected output:** `OrchEdPriv` and `OrchXPriv` (hex or base64 encodings) never appear in the keyfile — the laptop-side keyfile is operator-secrets-plus-orchestrator-PUBLIC-keys only, matching the outbound-reply verification/open requirement (verify with orch Ed25519 PUB, open with orch X25519 PUB + operator X25519 priv).
- **Assertions:** unit test hex-encodes `OrchEdPriv`/`OrchXPriv` and asserts the keyfile bytes do not contain either encoding.
- **Edge cases:** none.

### TC-148-07: CLI end-to-end — `agent-cli keygen --keyfile <path>` prints the env block to stdout and writes the keyfile
- **Requirement:** REQ-148-04
- **Input:** `examples/agent-cli keygen --keyfile /tmp/x/operator.json` invoked via the dispatcher with injected stdout/stderr buffers (mirrors `cli.Config` pattern from `internal/cli`).
- **Expected output:** exit code 0; stdout contains the seven `AGENT_BUILDER_TELEGRAM_*` lines from TC-148-03; stderr is empty except for a one-line human-readable confirmation ("keyfile written to /tmp/x/operator.json (mode 0600)") which itself contains no secret material; the keyfile exists at the given path with mode 0600.
- **Assertions:** unit-level dispatcher test using a temp dir; assert exit code, stdout content, and file existence/permissions.
- **Edge cases:** `examples/agent-cli keygen` with no `--keyfile` flag exits 2 (usage error) — the keyfile path is mandatory since losing it loses the operator's only copy of their private keys.

### TC-148-08: `--keyfile` targeting an existing file refuses to overwrite without `--force`
- **Requirement:** REQ-148-04
- **Input:** run `keygen --keyfile <path>` twice against the same path without `--force`.
- **Expected output:** the second invocation exits non-zero (2), prints an error naming the existing path, and does NOT overwrite the original file (original keys still readable afterward, byte-identical to before the second call). `keygen --keyfile <path> --force` on the same path succeeds and overwrites.
- **Assertions:** unit test captures the keyfile bytes before/after the no-`--force` second call and asserts byte-equality (unchanged); asserts the `--force` call produces different key material and exit 0.
- **Edge cases:** none beyond the two paths above.

### TC-148-09: secret material segregation — operator privates never leak; orchestrator privates confined to env block only
- **Requirement:** REQ-148-05
- **Input:** the same invocation as TC-148-07, with output captured into buffers.
- **Expected output:** 
  - **(a)** Operator's Ed25519/X25519 private keys (hex encodings) NEVER appear anywhere in combined stdout+stderr.
  - **(b)** Orchestrator's Ed25519/X25519 private keys (hex encodings) appear ONLY within the labeled env-block region on stdout (on the `_ORCH_PRIV`/`_ORCH_ED_PRIV` lines), and NEVER in the stderr confirmation line. *(Note: REQ-148-02 and TC-148-07 REQUIRE these to be in the env block, so absence-from-all-output would contradict the spec. This property corrects the initial TC-148-09 text.)*
  - **(c)** The orchestrator env block is printed distinctly from the human-readable confirmation line by a labeled banner (e.g., `--- paste into orchestrator environment ---`).
- **Assertions:** unit test obtains the REAL generated `KeyMaterial` and encodes every private-key field in hex; asserts `OperatorEdPriv` and `OperatorXPriv` hex are absent from stdout AND stderr; asserts `OrchEdPriv` and `OrchXPriv` hex do NOT appear in stderr, and DO appear in stdout only within the env block (after the banner); asserts the banner-separator string is present in stderr.
- **Edge cases:** none.

## Notes
Placement per ADR 062: all client code — entrypoint and logic — lives together under the new top-level `examples/agent-cli/` directory (its own package(s)), NOT under `cmd/` and NOT with an `internal/agentcli` package. This keeps it liftable as a single directory and keeps the operator-side trust boundary visible. Entrypoint `examples/agent-cli/main.go` plus the client's package(s) under that directory; task 148 stubs the dispatcher `Main(Config)` shape, mirroring `internal/cli.Main`/`cli.Config`, so tasks 149/150 add subcommands to the same dispatcher rather than each owning their own `main`. Reuses `internal/envelope.GenerateKeyPair` and stdlib `crypto/ed25519`/`crypto/rand` exclusively — no new crypto. `examples/agent-cli` imports `internal/envelope` and stdlib only; the orchestrator (`cmd/agent-builder`, `internal/**`) must never import it (one-way edge; a `make fitness-agentcli-boundary` check may enforce it later — out of scope for this task). Task 148's implementation commit also corrects the stale base64→hex doc comment in `internal/envelope/envelope.go:59` (ADR 062).
