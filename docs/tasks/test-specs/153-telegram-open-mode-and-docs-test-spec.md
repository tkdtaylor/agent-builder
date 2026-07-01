# Test Spec 153: Telegram `open` mode + mandatory startup WARNING + docs

**Linked task:** [`docs/tasks/backlog/153-telegram-open-mode-and-docs.md`](../backlog/153-telegram-open-mode-and-docs.md)
**Governing ADR:** [`docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md`](../architecture/decisions/063-telegram-sender-id-channel-auth-modes.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-153-01 | TC-153-01, TC-153-02 | ✅ |
| REQ-153-02 | TC-153-03, TC-153-04 | ✅ |
| REQ-153-03 | TC-153-05 | ✅ |
| REQ-153-04 | TC-153-06 | ✅ |

## Test cases

### TC-153-01: `open` mode accepts plaintext from ANY sender ID, including one never seen before
- **Requirement:** REQ-153-01
- **Input:** adapter assembled with `AUTH_MODE=open` (no approved store required — `open` never consults the store); an inbound plaintext `"status"` from a never-before-seen sender ID (e.g. `999999`).
- **Expected output:** the message is accepted, routed through `armor.Guard`/`ContentGuard.DecideContent` (retained per Decision 2, identical to `allowlist`), then `deriveMessage` → `MsgStatus` derived — no allowlist/owner/pairing check gates it at all.
- **Assertions:** unit test asserts a `supervisor.Message{Kind: MsgStatus}` is derived for the unknown sender, and that `ContentGuard.DecideContent` was invoked (armor not bypassed).
- **Edge cases:** a second, different unknown sender ID in the same test run is also accepted without any per-sender state change (no store read/write occurs in `open` mode).

### TC-153-02: `open` mode still enforces the retained SEC-001/002 size caps and audits rejections
- **Requirement:** REQ-153-01
- **Input:** `open` mode; an inbound update whose `Message.Text` exceeds `maxMessageBytes`.
- **Expected output:** rejected before armor (mirrors the existing `text_too_long` branch); an audit event is emitted; `ContentGuard.DecideContent` is never invoked. `open` does not weaken the retained-controls list from Decision 2 (armor + size caps + audit stay non-negotiable in every sender-ID mode).
- **Assertions:** unit test asserts no message derived, an audit event recorded with the size-cap reason, `ContentGuard` call count unchanged.
- **Edge cases:** none.

### TC-153-03: `open` is reachable ONLY via the exact literal value `open` (not the default, not a typo, not case-insensitive)
- **Requirement:** REQ-153-02
- **Input:** `AGENT_BUILDER_TELEGRAM_AUTH_MODE` set to each of: unset, `""`, `"Open"`, `"OPEN"`, `" open"` (leading space), `"open "` (trailing space).
- **Expected output:** unset/`""` ⇒ `envelope` (per task 151's TC-151-01, unaffected by this task); `"Open"`/`"OPEN"` ⇒ fail-fast unknown-mode config error (mode values are case-sensitive exact matches, consistent with the existing `AGENT_BUILDER_INBOUND` enum convention — no fuzzy matching that could let a typo accidentally either silently fall back to envelope or accidentally land on open); `" open"`/`"open "` (untrimmed whitespace) ⇒ fail-fast unknown-mode config error (no implicit trimming that could make a copy-paste error silently succeed as the highest-risk mode).
- **Assertions:** unit test table-drives all six input values and asserts the exact classification (envelope-default / fail-fast-unknown) for each; specifically asserts that none of the near-miss variants (`Open`, `OPEN`, padded) resolve to `open`.
- **Edge cases:** none — this pins "explicit opt-in value only" from the ADR's mode-matrix Notes column as a literal, non-fuzzy string match.

### TC-153-04: `disabled`/`envelope`/`allowlist`/`pairing` modes are unaffected by this task's changes (regression guard)
- **Requirement:** REQ-153-02
- **Input:** re-run the load-bearing assertions from TC-151-01 (envelope default), TC-151-06 (disabled rejects all), TC-151-03/04 (allowlist accept/reject), and TC-152-05/TC-152-08 (pairing stranger-cannot-self-approve, survives-restart) against the adapter as it exists AFTER this task's `open`-mode addition lands.
- **Expected output:** all listed prior assertions still hold byte-for-byte identically — adding `open` does not alter any other mode's branch.
- **Assertions:** this TC is satisfied by the full existing test suites for tasks 151/152 continuing to pass unmodified after task 153's diff lands (a regression run, not new test code specific to this task, though the harness may re-invoke the existing test functions explicitly to document the intent).
- **Edge cases:** none.

### TC-153-05: a mandatory stderr `WARNING` is emitted at assembly when (and ONLY when) mode is `open`
- **Requirement:** REQ-153-03
- **Input:** assemble the orchestrator/adapter with `AUTH_MODE=open`; separately, assemble with each of `envelope` (default), `allowlist`, `pairing`, `disabled`.
- **Expected output:** the `open`-mode assembly writes a single `WARNING`-prefixed line to stderr naming the concrete risk — the message MUST contain language equivalent to "any account that finds the bot can command it" (the exact risk framing from ADR 063 Decision 1's mode-matrix Notes column) — emitted unconditionally (not gated behind a verbosity/log-level flag) exactly once per assembly. NONE of the other four modes emit this (or any similarly-worded) warning line.
- **Assertions:** unit test captures stderr (or the logger's output sink) across all five assembly invocations and asserts: `open` → exactly one line matching `WARNING.*any account.*command` (or equivalent captured substring check for the risk phrase); the other four → zero matching lines.
- **Edge cases:** the warning is emitted even if `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` happens to also be set for `open` mode (the store is simply unused/unread in `open` — the warning fires regardless of unrelated config presence).

### TC-153-06: documentation footprint — mode matrix + tradeoff are documented, and each task's own spec entries are present
- **Requirement:** REQ-153-04
- **Input:** review `docs/spec/configuration.md`, `docs/spec/behaviors.md`, and (if the diagram changed) `docs/architecture/diagrams.md` after all three tasks (151/152/153) have landed.
- **Expected output:** `configuration.md` documents `AGENT_BUILDER_TELEGRAM_AUTH_MODE` (all five values, default, fail-fast-unknown), `AGENT_BUILDER_TELEGRAM_APPROVED_STORE`, `AGENT_BUILDER_TELEGRAM_OWNER_ID`, and (if introduced) `AGENT_BUILDER_TELEGRAM_APPROVED_IDS`, each under the existing `AGENT_BUILDER_TELEGRAM_*` family with a "required when" column consistent with neighboring rows. `behaviors.md` carries a new behavior entry (or an extension of an existing Telegram-inbound entry) describing the mode matrix, the plaintext lost/retained split (Decision 2), the owner-gated pairing flow, and the persistence-across-restart property — in present tense, no future-tense planned-work language (per `AGENTS.md`'s spec-authoring rule).
- **Assertions:** this TC is a documentation-completeness review, not a Go unit test — the spec-verifier/reviewer confirms each named doc file was touched with content matching the above description in the SAME commit as this task's code (or, for 151/152's own env vars, in THEIR respective feat commits per the same-commit rule — this task's own commit need only add the `open` value and finish the mode-matrix narrative, not re-document vars already documented by 151/152).
- **Edge cases:** none.

## Notes
Depends on task 152 (`pairing` mode + owner gate + persisted approvals). This is the smallest of the three tasks — a single new enum value, a warning-emission side effect, and doc closure. No live Telegram bot token is available; L5/L6 live-bot verification is a documented follow-on (see task file Verification plan). TC-153-04 is deliberately a regression re-run rather than new assertions, since `open`'s entire risk profile is "does it correctly NOT touch the other modes" plus "does it correctly warn" — both already covered structurally by TC-153-01..03/05.
