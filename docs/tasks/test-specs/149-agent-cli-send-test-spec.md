# Test Spec 149: `agent-cli send` — seal + sign a command and POST it to Telegram

**Linked task:** [`docs/tasks/backlog/149-agent-cli-send.md`](../backlog/149-agent-cli-send.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-149-01 | TC-149-01, TC-149-02 | ✅ |
| REQ-149-02 | TC-149-03 | ✅ |
| REQ-149-03 | TC-149-04, TC-149-05 | ✅ |
| REQ-149-04 | TC-149-06 | ✅ |
| REQ-149-05 | TC-149-07 | ✅ |
| REQ-149-06 | TC-149-08 | ✅ |

## Test cases

### TC-149-01: sealed+signed envelope round-trips through the production `envelope.VerifyAndOpen` (load-bearing byte-compatibility assertion)
- **Requirement:** REQ-149-01
- **Input:** a keyfile (as written by task 148's `WriteKeyfile`) holding `OperatorEdPriv`, `OperatorXPriv`, `OrchEdPub`, `OrchXPub`; command text `"status tg-42-7"`; call the package function `BuildEnvelope(keyfile, orchXPub, cmdText)` directly (no HTTP).
- **Expected output:** the returned `envelope.Envelope` has non-empty `Nonce`, `TS`, `Payload`, `Sig`. Feeding it through `envelope.VerifyAndOpen(env, operatorEdPub, envelope.NewReplayCache(60*time.Second), orchXPriv, operatorXPub)` — the EXACT verification the live `telegram.Adapter` performs per its `Config` wiring (`TrustedSigningKey`=operator Ed25519 pub, `OrchestratorPriv`=orchestrator X25519 priv, `TrustedX25519Pub`=operator X25519 pub) — returns `plaintext == "status tg-42-7"` byte-for-byte, nil error.
- **Assertions:** unit test generates a full keypair set (mirrors `generateReplyKeys` pattern in `reply_test.go`), builds the envelope via the CLI's own code path, and round-trips it through the real `envelope.VerifyAndOpen` with the adapter's exact argument roles. This is the single assertion that proves the CLI is wire-compatible with `internal/channel/telegram/adapter.go`, not just self-consistent.
- **Edge cases:** repeat with a multi-word goal-spec command (e.g. a bare new-goal string with spaces and punctuation) — plaintext must still match byte-for-byte (no re-encoding/trimming of the command text).

### TC-149-02: `Payload` and `Nonce` are hex-encoded on the wire, matching `VerifyAndOpen`'s actual decode path (not the stale base64 doc comment)
- **Requirement:** REQ-149-01
- **Input:** the `envelope.Envelope` produced by `BuildEnvelope` in TC-149-01.
- **Expected output:** `env.Payload` and `env.Nonce` both decode successfully via `hex.DecodeString` (NOT `base64.StdEncoding.DecodeString` — the `Envelope.Payload` struct comment says base64, but `envelope.VerifyAndOpen` line-for-line calls `hex.DecodeString(env.Payload)` and `ReplyAdapter.Report`/`hex.EncodeToString` both hex-encode; the CLI must match the real code path, not the doc comment).
- **Assertions:** unit test asserts `hex.DecodeString(env.Payload)` succeeds and `base64.StdEncoding.DecodeString(env.Payload)` either fails or (if it happens to parse as valid base64 by coincidence) decodes to different bytes than the hex decode — proving hex is the actual, intentional encoding, not an accidental one. Primary assertion is TC-149-01's full round-trip through the real library function, which is definitive; this TC additionally documents/pins the encoding explicitly so a future refactor cannot silently flip it.
- **Edge cases:** none — this is a pinning test.

### TC-149-03: `send` POSTs to `<baseURL>/bot<token>/sendMessage` with `{chat_id, text}` where `text` is the marshalled envelope JSON
- **Requirement:** REQ-149-02
- **Input:** `httptest.NewServer` stub capturing the request path and body (mirrors `stubSendMessageServer` in `reply_test.go`); invoke the CLI's `runSend` (or equivalent) with `--keyfile`, `--token`, `--base-url <stub-url>`, `--chat-id 12345`, and a command string.
- **Expected output:** exactly one POST to `<stubURL>/bot<token>/sendMessage`; request body JSON `{"chat_id":"12345","text":"<envelope JSON>"}`; the `text` field parses as a valid `envelope.Envelope` with `From:"operator"`, `To:"orchestrator"`.
- **Assertions:** unit test unmarshals the captured body, asserts path/method/fields; asserts `From`/`To` values.
- **Edge cases:** a non-2xx / `{"ok":false,"description":"..."}` stub response causes `send` to return a non-nil error surfaced to stderr and a non-zero exit code.

### TC-149-04: `--reply-to <msgID>` threads `reply_to_message_id` in the POST body
- **Requirement:** REQ-149-03
- **Input:** same stub as TC-149-03, invoked with `--reply-to 555`.
- **Expected output:** the captured POST body includes `"reply_to_message_id":555` (numeric, matching Telegram Bot API's `sendMessage` field name/type) alongside `chat_id`/`text`.
- **Assertions:** unit test unmarshals the body into a struct with a `ReplyToMessageID int64 \`json:"reply_to_message_id,omitempty"\`` field and asserts it equals 555.
- **Edge cases:** omitting `--reply-to` produces a body with NO `reply_to_message_id` key at all (asserted via `json.Unmarshal` into `map[string]interface{}` and checking key absence), not a zero-value key — Telegram treats a present-but-zero reply ID as invalid.

### TC-149-05: `--reply-to` accepts only a positive integer
- **Requirement:** REQ-149-03
- **Input:** `--reply-to abc`, `--reply-to -1`, `--reply-to 0`.
- **Expected output:** all three exit 2 (usage error) with a clear stderr message; no HTTP request is made (assert stub call count is 0).
- **Assertions:** unit test table-drives the three invalid inputs and asserts exit code + zero stub calls for each.
- **Edge cases:** `--reply-to 1` (smallest valid positive ID) succeeds.

### TC-149-06: token, operator private keys, and orchestrator public keys never appear in logs or stdout/stderr
- **Requirement:** REQ-149-04
- **Input:** the TC-149-03 invocation with a sentinel bot token (e.g. `"SEND_TOKEN_SENTINEL_149"`) and a logger writing to a captured buffer (mirrors `TestTC098_08_BotTokenAndKeysAbsentFromLogs`).
- **Expected output:** the sentinel token string never appears in combined stdout+stderr+logs; `OperatorEdPriv`/`OperatorXPriv` (hex and base64) never appear; the plaintext command text is allowed to appear in a local echo/confirmation line (it is not secret — only the wire ciphertext + keys are), but the RAW HTTP REQUEST BODY captured by the stub must not contain the plaintext command text unencrypted (mirrors TC-098-02's "plaintext must not appear in raw POST body" assertion).
- **Assertions:** unit test string-searches combined output for the token sentinel and both private-key encodings; separately asserts the captured raw POST body (bytes, not decoded JSON) does not contain the plaintext command string.
- **Edge cases:** none.

### TC-149-07: keyfile read failure (missing file, malformed JSON, truncated hex field) fails closed with a clear error
- **Requirement:** REQ-149-05
- **Input:** `send --keyfile /nonexistent/path ...`; `send --keyfile <path-to-malformed-json> ...`; `send --keyfile <path-to-json-with-odd-length-hex-field> ...`.
- **Expected output:** all three exit non-zero (2 for missing/usage, 1 for parse/decode failure) with a stderr message naming the failure category (not a raw panic/stack trace); no HTTP request is made in any case.
- **Assertions:** unit test table-drives the three cases, asserts exit code != 0, asserts stub call count 0, asserts stderr non-empty and does not contain a Go panic trace (`strings.Contains(stderr, "panic:")` is false).
- **Edge cases:** none.

### TC-149-08: empty command text is rejected before sealing/sending
- **Requirement:** REQ-149-06
- **Input:** `send --keyfile <valid> --token t --chat-id 1` with no trailing command text (empty positional args).
- **Expected output:** exit code 2 (usage error); stub call count 0.
- **Assertions:** unit test asserts exit code and zero HTTP calls.
- **Edge cases:** command text consisting only of whitespace is also rejected (trimmed to empty).

## Notes
Depends on task 148 (`internal/agentcli` package + `WriteKeyfile`/keyfile JSON shape + dispatcher `Main`/`Config` scaffold) — `send` is registered as a second subcommand in the same dispatcher, reusing the keyfile-reading helper. TC-149-01 is the single most load-bearing test in this feature: it proves the CLI's envelope is byte-compatible with the LIVE `internal/channel/telegram/adapter.go` verification path, not merely internally self-consistent. Framework: stdlib `net/http/httptest`, `testing`; no new dependency.
