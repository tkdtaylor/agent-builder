# Test Spec 150: `agent-cli reply-open` — decrypt + verify a sealed outbound reply envelope

**Linked task:** [`docs/tasks/backlog/150-agent-cli-reply-open.md`](../backlog/150-agent-cli-reply-open.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-150-01 | TC-150-01, TC-150-02 | ✅ |
| REQ-150-02 | TC-150-03, TC-150-04, TC-150-05 | ✅ |
| REQ-150-03 | TC-150-06 | ✅ |
| REQ-150-04 | TC-150-07 | ✅ |
| REQ-150-05 | TC-150-08 | ✅ |

## Test cases

### TC-150-01: decrypts a real `ReplyAdapter`-shaped envelope byte-for-byte
- **Requirement:** REQ-150-01
- **Input:** construct a real `telegram.ReplyAdapter` (as in `reply_test.go`'s `generateReplyKeys` + `stubSendMessageServer` pattern) with a known orchestrator Ed25519/X25519 keypair and operator X25519 pub; call `adapter.Report(ctx, "Approve plan? 2 sub-goals: docs-fix, coding-agent")` against the stub server; capture the emitted envelope JSON from the stub's request body (`outer.Text` field, exactly as `TestTC098_03_RoundTrip` extracts it). Feed that envelope JSON into the CLI's `reply-open` via a temp file argument, with a keyfile holding the operator's X25519 priv + the orchestrator's Ed25519 pub + X25519 pub (the fields task 148's `WriteKeyfile` already persists).
- **Expected output:** exit code 0; stdout is exactly `"Approve plan? 2 sub-goals: docs-fix, coding-agent"` (plus at most a trailing newline) — no extra banner/prefix mixed into the same line as the plaintext (so it can be piped/captured cleanly).
- **Assertions:** unit test builds the real `ReplyAdapter`, extracts the real emitted envelope, and asserts the CLI's own decrypt path recovers the identical plaintext produced by `Report`'s input. This is the load-bearing round-trip proving `reply-open` is compatible with the LIVE `ReplyAdapter.Report` wire format, not a hand-built envelope.
- **Edge cases:** plaintext containing embedded newlines is preserved exactly (no additional trimming beyond a single optional trailing `\n` the CLI itself adds for terminal display).

### TC-150-02: verifies with orchestrator Ed25519 PUB and opens with operator X25519 priv + orchestrator X25519 pub (correct key-role assignment)
- **Requirement:** REQ-150-01
- **Input:** the same envelope as TC-150-01; source-level assertion on the `reply-open` implementation's call to `envelope.VerifyAndOpen(env, signPub, cache, recipPriv, senderPub)`.
- **Expected output:** `signPub` = orchestrator Ed25519 pub (from keyfile), `recipPriv` = operator X25519 priv (from keyfile), `senderPub` = orchestrator X25519 pub (from keyfile) — the exact mirror of `ReplyAdapter`'s signer/sealer roles (orchestrator signs+seals outbound; operator opens). This is the reverse of the `send` path's role assignment (task 149), and getting it backwards silently fails every decrypt.
- **Assertions:** unit test constructs two independent keypair sets — one "correct role" and one "swapped role" (e.g. accidentally passing operator Ed25519 pub as `signPub`) — and asserts the correct-role call succeeds (TC-150-01) while a swapped-role call is a negative case folded into TC-150-04 (wrong-key fails closed). This TC's own assertion is a positive one: given ONLY the four keyfile-shaped fields (no other envelope-adjacent keys), `reply-open` must select the right three without operator error.
- **Edge cases:** none beyond the swap covered in TC-150-04.

### TC-150-03: accepts envelope JSON from a file argument, from stdin, and from an inline `--envelope` flag
- **Requirement:** REQ-150-02
- **Input:** the same valid envelope JSON as TC-150-01, provided three ways: (a) `reply-open --keyfile <kf> <path-to-envelope.json>`, (b) `echo '<envelope-json>' | reply-open --keyfile <kf>` (stdin, no positional arg), (c) `reply-open --keyfile <kf> --envelope '<envelope-json>'`.
- **Expected output:** all three produce the identical plaintext on stdout and exit code 0.
- **Assertions:** unit test invokes the dispatcher three times with each input mode and asserts identical stdout + exit 0 for each.
- **Edge cases:** both a positional file arg AND `--envelope` given simultaneously is a usage error (ambiguous input source) — exit 2.

### TC-150-04: bad signature / wrong key / tampered ciphertext all fail closed with a clear error (never a partial/garbage plaintext)
- **Requirement:** REQ-150-02
- **Input:** (a) the TC-150-01 envelope with one byte of `Payload` flipped (mirrors `TestTC098_04_TamperedEnvelopeFailsVerify` Input A); (b) the same envelope verified against an unrelated orchestrator Ed25519 pub (mirrors Input B); (c) the same envelope opened with the wrong operator X25519 priv (a freshly generated, unrelated key).
- **Expected output:** all three exit non-zero (1) with a stderr message that names the failure category (e.g. "signature verification failed" / "decryption failed") via `errors.Is` classification against `envelope.ErrBadSignature`/`ErrUnknownKey`, mirroring the adapter's own classification in `adapter.go`; stdout is empty in all three cases (no partial plaintext ever printed).
- **Assertions:** unit test table-drives the three tamper/wrong-key cases, asserts exit code, asserts stdout is empty, asserts stderr contains a recognizable category string.
- **Edge cases:** a syntactically invalid JSON blob (not an envelope at all) also exits non-zero with a "malformed envelope JSON" class of error, not a crypto error.

### TC-150-05: replay-cache behavior is a single-shot decrypt, not a persistent replay guard
- **Requirement:** REQ-150-02
- **Input:** decrypt the same valid envelope twice in two separate `reply-open` invocations (two separate process-lifetime `ReplayCache` instances, since this is a one-shot CLI, not a long-running server).
- **Expected output:** BOTH invocations succeed and print the identical plaintext — `reply-open` must NOT persist replay state across invocations (there is no cross-process replay cache; each invocation constructs a fresh `envelope.NewReplayCache` per the mandatory-ordering doc in `envelope.go`, satisfying `VerifyAndOpen`'s required argument without falsely rejecting a legitimately re-read message, e.g. an operator re-opening a saved reply file later).
- **Assertions:** unit test invokes the CLI twice against the same envelope file and asserts both succeed with identical stdout.
- **Edge cases:** none — this pins the "no persistent replay state" design choice explicitly so a future change doesn't accidentally add stateful replay rejection that breaks re-reading a saved reply.

### TC-150-06: `reply-open` never polls Telegram `getUpdates` (design-constraint check, not a live network assertion)
- **Requirement:** REQ-150-03
- **Input:** source inspection of the `reply-open` implementation file and its imports.
- **Expected output:** the implementation makes no `net/http` calls at all — no `baseURL`, `botToken`, or `getUpdates` reference anywhere in the `reply-open` code path; its only inputs are the keyfile and the envelope JSON (file/stdin/flag).
- **Assertions:** unit test greps the `reply-open` implementation file's source for `http.Get`, `http.Client`, `getUpdates`, and asserts none are present; asserts the function signature takes no `baseURL`/`botToken`-shaped parameters.
- **Edge cases:** none — this is a static design-constraint pin, not a runtime behavior test (avoids two pollers on one bot conflicting, per the feature brief).

### TC-150-07: token/keys never appear in logs or stdout/stderr
- **Requirement:** REQ-150-04
- **Input:** the TC-150-01 invocation with a logger writing to a captured buffer.
- **Expected output:** `OperatorXPriv` (hex/base64) and `OrchEdPub`/`OrchXPub` (hex/base64, even though public, kept out of casual log noise per the project's blanket "never log key material" convention) never appear in logs; the recovered plaintext appearing on stdout is expected and correct (it is the intended output of this subcommand, unlike `send` where plaintext must stay off the wire).
- **Assertions:** unit test string-searches captured log buffer (separate from stdout) for the private key encodings.
- **Edge cases:** none.

### TC-150-08: missing/malformed keyfile fails closed with a clear error
- **Requirement:** REQ-150-05
- **Input:** `reply-open --keyfile /nonexistent <envelope.json>`; `reply-open --keyfile <malformed-json-file> <envelope.json>`.
- **Expected output:** both exit non-zero with a stderr message naming the failure (no panic trace); stdout empty.
- **Assertions:** unit test table-drives both cases, asserts exit code != 0, stdout empty, no `"panic:"` substring in stderr.
- **Edge cases:** none.

## Notes
Depends on task 148 (`examples/agent-cli` package, keyfile JSON shape, dispatcher scaffold, placed per ADR 062). Independent of task 149 at the implementation level (different subcommand, opposite key-role direction) but both register into the same `examples/agent-cli` dispatcher from task 148, so task 148 is a hard prerequisite for both 149 and 150; 149 and 150 have no dependency on each other and can be implemented in either order or in parallel. TC-150-01/02 are the load-bearing byte-compatibility assertions, mirroring TC-149-01 but in the reverse (outbound reply) direction, reusing the exact `generateReplyKeys`/`stubSendMessageServer` test helpers already established in `internal/channel/telegram/reply_test.go` (replicate the pattern locally in `examples/agent-cli`, since `agent-cli` cannot import telegram's `_test.go` helpers across package boundaries — replicate, don't cross-import test files).
