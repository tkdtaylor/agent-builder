# Test Spec 151: Telegram auth-mode config plumbing + `envelope`/`disabled`/`allowlist` modes + persisted approved-sender store

**Linked task:** [`docs/tasks/backlog/151-telegram-authz-mode-plumbing.md`](../backlog/151-telegram-authz-mode-plumbing.md)
**Governing ADR:** [`docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md`](../architecture/decisions/063-telegram-sender-id-channel-auth-modes.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-151-01 | TC-151-01, TC-151-02 | ✅ |
| REQ-151-02 | TC-151-03, TC-151-04, TC-151-05 | ✅ |
| REQ-151-03 | TC-151-06, TC-151-07 | ✅ |
| REQ-151-04 | TC-151-08, TC-151-09 | ✅ |
| REQ-151-05 | TC-151-10 | ✅ |
| REQ-151-06 | TC-151-11, TC-151-12 | ✅ |

## Test cases

### TC-151-01: unset `AGENT_BUILDER_TELEGRAM_AUTH_MODE` reproduces today's `envelope` behavior byte-for-byte
- **Requirement:** REQ-151-01
- **Input:** assemble a `telegram.Adapter` via the normal `Config`/env-assembly path with `AGENT_BUILDER_TELEGRAM_AUTH_MODE` unset; feed it a real signed+sealed `envelope.Envelope` (built the same way `adapter_test.go`'s existing envelope-path tests do) as the `Message.Text`.
- **Expected output:** the envelope is verified and opened via `envelope.VerifyAndOpen` exactly as before; the resulting `supervisor.Message` matches what the pre-task adapter would have produced; no `authz` package call is made on this path (sender ID is never consulted).
- **Assertions:** unit test asserts the returned `supervisor.Message` fields and that behavior is identical to the existing (pre-task) envelope-path test fixture, run both with the field explicitly set to `"envelope"` and with it entirely unset — both produce the identical message.
- **Edge cases:** none — this is the default-preservation pin required by ADR 063 Decision 1.

### TC-151-02: `envelope` mode still rejects plaintext (no accidental widening)
- **Requirement:** REQ-151-01
- **Input:** mode unset (or `"envelope"`); `Message.Text` is a plain, non-envelope-shaped string (e.g. `"status"`).
- **Expected output:** the update is rejected exactly as today (`envelope_parse_failed` / JSON unmarshal failure), `Next()` returns `(Message{}, false, nil)` for that update, and an audit event with the existing `envelope_parse_failed`-class reason is emitted — sender ID is never consulted, and the plaintext path (armor, authz store) is never reached.
- **Assertions:** unit test feeds plaintext through the assembled `envelope`-mode adapter and asserts no `supervisor.Message` is derived and the FakeSink/audit stub records the parse-failure reason, not an authz-mode reason.
- **Edge cases:** none.

### TC-151-03: `allowlist` mode accepts a plaintext command from an approved sender ID
- **Requirement:** REQ-151-02
- **Input:** adapter assembled with `AUTH_MODE=allowlist` and a statically configured approved-ID list (e.g. `AGENT_BUILDER_TELEGRAM_APPROVED_IDS=42`) seeded into the store at startup; an inbound update whose `Message.Chat.ID`/sender ID is `42` and `Message.Text` is a bare plaintext command (e.g. `"status"`).
- **Expected output:** `Next()` returns a derived `supervisor.Message{Kind: MsgStatus, ...}` — the plaintext is accepted, routed through `armor.Guard`/`ContentGuard.DecideContent`, and then `deriveMessage`, exactly like the post-envelope-open plaintext path today; an accept-side audit event is emitted.
- **Assertions:** unit test asserts the returned message kind/goalID and that the injected fake `ContentGuard.DecideContent` was actually invoked (call count ≥ 1) with the plaintext as the candidate content — proving armor is NOT bypassed on the accepted plaintext path.
- **Edge cases:** normalized numeric sender ID matches even if the wire representation differs (see TC-151-11).

### TC-151-04: `allowlist` mode rejects a plaintext command from an unapproved sender ID
- **Requirement:** REQ-151-02
- **Input:** same adapter as TC-151-03; an inbound update from sender ID `99` (not in the approved set) with plaintext `"status"`.
- **Expected output:** `Next()` returns `(Message{}, false, nil)` for that update (no goal, no derived message); an audit event is emitted classifying the rejection (e.g. `sender_not_approved`); `ContentGuard.DecideContent` is NEVER invoked for this update (armor only runs on content the sender-ID gate has already accepted — an unapproved sender's payload is rejected before armor, per the mode-decision seam in ADR 063 Decision 5).
- **Assertions:** unit test asserts no message derived, audit stub records the rejection reason, and the fake `ContentGuard` call count is unchanged (0 additional calls) versus before the update.
- **Edge cases:** an unapproved sender with a well-formed envelope-shaped payload is still rejected in `allowlist` mode — plaintext-only modes do not opportunistically fall back to envelope verification.

### TC-151-05: `allowlist` mode oversized plaintext is rejected with SEC-001/002 caps retained
- **Requirement:** REQ-151-02
- **Input:** the TC-151-03 approved sender, but `Message.Text` exceeds `maxMessageBytes`.
- **Expected output:** the update is rejected before armor/authz routing (mirrors the existing `text_too_long` branch), with an audit event emitted; `ContentGuard.DecideContent` is never invoked.
- **Assertions:** unit test asserts no message derived, an audit event with the size-cap reason is recorded, and `ContentGuard` call count is unchanged. Confirms Decision 2's "RETAINED" list (size caps) applies identically on the plaintext sender-ID path, not just the envelope path.
- **Edge cases:** none.

### TC-151-06: `disabled` mode rejects all inbound traffic
- **Requirement:** REQ-151-03
- **Input:** adapter assembled with `AUTH_MODE=disabled`; feed it (a) a real signed envelope, and (b) a plaintext command from a sender ID that would be approved under `allowlist`.
- **Expected output:** both (a) and (b) are rejected — `Next()` returns `(Message{}, false, nil)` for each — and each emits a distinct audit rejection event (e.g. `channel_disabled`); the `envelope.VerifyAndOpen` pipeline is never invoked (mode-branch happens before any envelope parse attempt) and armor/`ContentGuard` is never invoked.
- **Assertions:** unit test asserts zero derived messages for both inputs, audit events recorded for both, and the fake envelope-verify hook / `ContentGuard` call counts are both zero.
- **Edge cases:** none — `disabled` is a full-stop channel-inert mode per the ADR's mode matrix.

### TC-151-07: unknown `AUTH_MODE` value fails fast at assembly time
- **Requirement:** REQ-151-03
- **Input:** `AGENT_BUILDER_TELEGRAM_AUTH_MODE=bogus-mode` at orchestrate assembly time.
- **Expected output:** a fail-fast `ExitUsage`-class configuration error at assembly, consistent with the existing `AGENT_BUILDER_INBOUND` unknown-value handling — never a nil-adapter panic at first `Next()` call.
- **Assertions:** unit test calls the assembly/config-validation function with the bogus value and asserts a returned error (not a panic), and that no `Adapter` is constructed/returned. A second assertion confirms the five recognized values (`envelope`, `allowlist`, `pairing`, `open`, `disabled`) all pass validation at this task's config layer (pairing/open acceptance is validated structurally here even though their runtime behavior lands in tasks 152/153 — REQ-151-03 only requires the enum + fail-fast gate, not the pairing/open runtime paths).
- **Edge cases:** empty string is treated as unset (⇒ `envelope`), not as an unknown value.

### TC-151-08: the approved-sender store round-trips across a reload (the persistence proof)
- **Requirement:** REQ-151-04
- **Input:** construct an `authz.Store` backed by a temp file path, `Add(42)` and `Add(1001)`, `Persist()`. Construct a SECOND, independent `authz.Store` instance pointed at the SAME file path and `Load()`.
- **Expected output:** the second store's membership check (`Contains(42)`, `Contains(1001)`) returns true for both IDs — the reloaded store has the exact same approved set as the one that wrote it, with no data loss or corruption. The file is JSON, human-readable, and `0600`-permissioned after `Persist()`.
- **Assertions:** unit test performs the write-then-reload-from-disk sequence with a fresh instance (not the same in-memory object) and asserts membership equality; asserts `os.Stat(path).Mode().Perm() == 0600`; asserts the on-disk bytes parse as JSON.
- **Edge cases:** a missing file on `Load()` is graceful absence (empty store, no error) — mirrors `DiskOAuthSecretSource`'s missing-file behavior; a malformed JSON file on `Load()` is a fail-fast error (unlike the graceful-absence secret source, since a corrupted approval store silently treated as empty would fail-closed acceptably, but the task's Store must distinguish "absent" from "corrupt" so an operator notices — assert `Load()` on a malformed file returns a non-nil error, and on a genuinely missing file returns nil error + empty store).

### TC-151-09: `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` unset in `allowlist`/`pairing` mode is a fail-fast config error
- **Requirement:** REQ-151-04
- **Input:** `AUTH_MODE=allowlist` (or `pairing`) with `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` unset/blank.
- **Expected output:** fail-fast configuration error at assembly (the persisted store is load-bearing for any mode that consults sender-ID approval; a blank path only makes sense for `envelope`/`disabled`/`open`, none of which need a persisted approved set — `open` accepts everyone unconditionally and never reads the store).
- **Assertions:** unit test asserts assembly returns an error for `allowlist`+blank path, and succeeds for `envelope`+blank path and `disabled`+blank path.
- **Edge cases:** `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` set but pointing at a not-yet-existing file is NOT an error (the file is created on first `Persist()`, mirroring the `0600`-created-if-absent convention from ADR 063 Decision 4) — only an existing-but-unwritable path (e.g. read-only parent dir) is a fail-fast error at assembly.

### TC-151-10: `allowlist` mode seeds the statically configured approved IDs into the persisted store at startup
- **Requirement:** REQ-151-05
- **Input:** `AUTH_MODE=allowlist`, `AGENT_BUILDER_TELEGRAM_APPROVED_STORE=<empty/nonexistent path>`, a static approved-ID config value (e.g. `AGENT_BUILDER_TELEGRAM_APPROVED_IDS=42,1001`) at assembly time.
- **Expected output:** after assembly, the store file exists at `0600` and contains exactly `{42, 1001}` (normalized numeric form) — seeding happens once at startup, not on every `Next()` call. A second assembly run against the SAME store path with a DIFFERENT static list does not remove previously-approved IDs already on disk (seeding is additive/union, not a destructive overwrite) — this anticipates `pairing` mode (task 152) growing the same store via in-chat approval without a later `allowlist`-mode restart wiping it.
- **Assertions:** unit test asserts the store file's parsed contents after assembly match the static list; a second assertion re-assembles against the same path with an empty static list and confirms the previously-seeded IDs are still present (union semantics, not overwrite).
- **Edge cases:** a malformed static ID (non-numeric, e.g. `"abc"`) in the list is a fail-fast config error at assembly, not a silently-skipped entry.

### TC-151-11: sender-ID normalization prevents a trivial bypass or duplicate-entry split
- **Requirement:** REQ-151-06
- **Input:** `Store.Add` called with the numeric ID `42`, then with the string-formatted variants `"42"`, `"042"`, `" 42 "` (whitespace-padded) via the same normalize-then-add path the seeding/pairing flows use; separately, a `Contains` check against an inbound sender ID formatted as `"042"` when the store holds `42`.
- **Expected output:** all variants normalize to the single canonical numeric form `42` — the store never contains two distinct entries for what is semantically the same sender ID, and `Contains("042")` returns true when `42` is stored (and vice versa). This is the concrete anti-bypass check: an attacker cannot use a differently-formatted sender ID to either evade an allowlist-miss or create a duplicate approval record.
- **Assertions:** unit test adds all four variants and asserts the store's final membership size is 1 (not 4); asserts cross-format `Contains` checks succeed in both directions.
- **Edge cases:** a non-numeric, non-normalizable sender ID (e.g. containing letters) is rejected at the normalize step with an error, not silently coerced to `0` or accepted as a wildcard.

### TC-151-12: architecture/diagram note only if the inbound flow diagram gains the plaintext branch
- **Requirement:** REQ-151-06
- **Input:** review of `docs/architecture/diagrams.md`'s existing Telegram inbound flow entry.
- **Expected output:** if this task's `feat:` commit adds a new decision branch (mode-check → `authz` box → plaintext armor path) to the diagrammed flow, `diagrams.md` is updated in the SAME commit with a date bump at the top, per `AGENTS.md`'s same-commit diagram rule. This TC is a documentation-completeness check, not a runtime behavior assertion.
- **Assertions:** spec-verifier / reviewer confirms `diagrams.md` was touched in the feat commit if-and-only-if the inbound flow diagram's shape actually changed.
- **Edge cases:** none.

## Notes
This is the foundation task for ADR 063 (task 152 `pairing` and task 153 `open` build on the `authz` store and mode-decision seam this task introduces). Depends on task 150 (`agent-cli reply-open`) per the ADR's explicit sequencing — no code dependency, but task ordering is deliberate (the operator-CLI on-ramp lands before its lower-security alternative). The `internal/channel/telegram/authz` package MUST import stdlib only (isolation invariant, ADR 063 Decision 5) — a fitness-style source-grep assertion belongs in this task's acceptance criteria (see task file REQ-151-06 / TC-151-11's neighbor checks) even though a dedicated `make fitness` target is a documented follow-on, not required by this task.
