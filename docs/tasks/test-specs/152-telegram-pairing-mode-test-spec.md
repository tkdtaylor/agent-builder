# Test Spec 152: Telegram `pairing` mode — in-chat owner-approve flow

**Linked task:** [`docs/tasks/backlog/152-telegram-pairing-mode.md`](../backlog/152-telegram-pairing-mode.md)
**Governing ADR:** [`docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md`](../architecture/decisions/063-telegram-sender-id-channel-auth-modes.md)
**Written:** 2026-07-01
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-152-01 | TC-152-01, TC-152-02 | ✅ |
| REQ-152-02 | TC-152-03, TC-152-04 | ✅ |
| REQ-152-03 | TC-152-05, TC-152-06 | ✅ |
| REQ-152-04 | TC-152-07, TC-152-08 | ✅ |
| REQ-152-05 | TC-152-09 | ✅ |

## Test cases

### TC-152-01: unknown sender in `pairing` mode triggers `pairing_request` audit + pending reply + owner notification
- **Requirement:** REQ-152-01
- **Input:** adapter assembled with `AUTH_MODE=pairing`, `AGENT_BUILDER_TELEGRAM_OWNER_ID=1`, an empty approved store; an inbound plaintext update from sender ID `77` (unknown — not in the store, not the owner).
- **Expected output:** (1) an audit event `pairing_request` is emitted carrying the unknown sender's normalized ID; (2) the adapter's outbound `Reporter`/reply path sends the sender a "pending" message (exact wording is implementation-defined but MUST be distinguishable from a normal command result — assert it contains a recognizable "pending"/"awaiting approval" marker); (3) the OWNER's chat receives a distinct notification containing the unknown sender's ID and the literal `approve <id>` / `deny <id>` instruction text. No `supervisor.Message` is derived for this update (it never reaches `deriveMessage`).
- **Assertions:** unit test uses a fake `Reporter`/outbound sink capturing all sent messages, asserts one message went to the unknown sender (pending marker) and a second, distinct message went to the owner chat (containing the sender's ID and the approve/deny instruction), and asserts the audit stub recorded exactly one `pairing_request` event with the correct sender ID in `Detail`.
- **Edge cases:** a second message from the SAME still-unapproved sender before the owner responds re-triggers the pending reply (idempotent — no crash, no duplicate owner spam suppression required by this task, though a follow-on de-dupe is out of scope).

### TC-152-02: a still-pending sender's ordinary command text does not leak into `deriveMessage`
- **Requirement:** REQ-152-01
- **Input:** the TC-152-01 unknown sender (`77`), but the plaintext is `"status"` (an otherwise-valid command verb) instead of free text.
- **Expected output:** identical to TC-152-01 — the pairing/pending branch intercepts BEFORE any command-verb routing; `"status"` is never interpreted as a status query and no `supervisor.Message` is derived.
- **Assertions:** unit test asserts no `MsgStatus` (or any) message is derived and the same pending/owner-notify behavior as TC-152-01 occurs.
- **Edge cases:** none.

### TC-152-03: owner's `approve <id>` adds the sender to the persisted store
- **Requirement:** REQ-152-02
- **Input:** the TC-152-01 state (sender `77` pending); an inbound update from the OWNER's sender ID (`1`) with plaintext `"approve 77"`.
- **Expected output:** sender ID `77` is added to the approved-sender store (`authz.Store.Contains(77)` becomes true) and `Persist()`ed to disk; an audit event records the approval (owner ID, approved ID); the owner receives a confirmation reply. No `supervisor.Message` is derived from the owner's `"approve 77"` text itself (it is consumed by the approve/deny grammar, never routed as a new goal or any other command — see TC-152-07 for the ordering assertion).
- **Assertions:** unit test asserts `Contains(77)` is true after processing, asserts the on-disk store file (if a path is configured) reflects the addition after a fresh `Load()`, asserts an audit event was recorded, and asserts no `supervisor.Message` was returned by `Next()` for this update.
- **Edge cases:** `"approve"` with no ID argument, or a non-numeric ID argument, is a malformed-grammar case: rejected without crashing, no store mutation, an audit event noting the malformed approve attempt (does not fall through to ordinary command routing either).

### TC-152-04: owner's `deny <id>` records refusal without approving
- **Requirement:** REQ-152-02
- **Input:** the TC-152-01 state (sender `77` pending); an inbound update from the OWNER (`1`) with plaintext `"deny 77"`.
- **Expected output:** sender ID `77` is NOT added to the store (`Contains(77)` remains false); an audit event records the denial; the owner receives a confirmation reply; the denied sender's subsequent messages continue to hit the pending/unknown-sender path (denial is not a permanent block-list distinct from "never approved" — a denied sender may re-request and be re-evaluated, per the ADR's flow, which does not specify a permanent deny-list).
- **Assertions:** unit test asserts `Contains(77)` is false after processing, asserts an audit event recorded the denial, and confirms a subsequent message from sender `77` re-enters the pending/pairing_request path (not silently dropped, not auto-approved).
- **Edge cases:** none beyond TC-152-03's malformed-grammar mirror (`"deny"` with no/bad ID is rejected without crashing or mutating state).

### TC-152-05: a stranger sending `approve <own-id>` CANNOT self-approve (owner-gate holds) — LOAD-BEARING
- **Requirement:** REQ-152-03
- **Input:** sender ID `77` (a stranger, NOT the configured owner `1`), pending in the store as unknown, sends the plaintext `"approve 77"` (attempting to approve itself).
- **Expected output:** the message is treated as ORDINARY unapproved input — it re-triggers the pending/`pairing_request` path exactly as in TC-152-01 (owner notified, sender told "pending") — it does NOT match the approve/deny grammar's owner branch at all, because the sender-ID check (sender == configured owner) gates entry to that grammar before the text is even inspected for `"approve"`/`"deny"` verbs. `Contains(77)` remains false afterward.
- **Assertions:** unit test asserts `Contains(77)` is false after processing the stranger's `"approve 77"` message, asserts the SAME pending-reply + owner-notification behavior as TC-152-01 fires (proving the text was routed through the ordinary unknown-sender path, not the owner grammar), and asserts no "approval confirmed" reply was ever sent to sender `77`.
- **Edge cases:** a stranger sending `"deny 77"` (attempting to deny ITS OWN pending request, which would be a no-op even if honored) is likewise routed as ordinary unapproved input, not the owner grammar — same assertion pattern.

### TC-152-06: an already-approved sender's plaintext commands route normally (not through the pairing branch)
- **Requirement:** REQ-152-03
- **Input:** sender ID `77`, already present in the approved store (via prior `Add`/`Persist` or a prior approve flow), sends plaintext `"status"`.
- **Expected output:** normal `allowlist`-equivalent routing applies (task 151's accepted-plaintext path: armor → `deriveMessage` → `MsgStatus`) — the pairing/pending/owner-notify machinery is never invoked for an already-approved sender.
- **Assertions:** unit test asserts a `supervisor.Message{Kind: MsgStatus}` is derived, and that no `pairing_request` audit event or owner notification fired for this update.
- **Edge cases:** none.

### TC-152-07: approve/deny is evaluated BEFORE `deriveMessage`'s verb parsing — LOAD-BEARING ORDERING
- **Requirement:** REQ-152-04
- **Input:** the OWNER (`1`) sends `"approve 123"` where `123` is a sender ID that happens to look like it could also be a numeric "goalID" argument to a `status`-like grammar, AND separately the owner sends a message that is a legitimate new-goal-shaped free text alongside owner-only approve/deny availability (e.g. owner sends `"approve 123"` immediately followed by `"status"` in a second update).
- **Expected output:** the owner's `"approve 123"` is intercepted by the approve/deny branch and NEVER reaches `deriveMessage` as a `MsgNewGoal`/any other kind (it does not get treated as free-form goal text just because the owner is also a valid message sender for ordinary commands); the owner's SEPARATE, later `"status"` message DOES route normally through `deriveMessage` as `MsgStatus` — proving the pairing-branch check is a per-message, text-content-gated pre-filter (owner-ID AND `approve`/`deny`-shaped text), not a blanket "everything from the owner skips normal routing" rule.
- **Assertions:** unit test asserts `Next()` returns no message for the owner's `"approve 123"` update (consumed by the grammar) and DOES return `MsgStatus` for the owner's subsequent `"status"` update — both from the same owner sender ID, proving the grammar match (not just sender identity) determines interception, and that interception happens ahead of (not instead of, in general) the owner's normal command routing.
- **Edge cases:** the owner sending literal free text that happens to start with the word "approve" but is not `approve <numeric-id>` shaped (e.g. `"approve of this plan"`) does NOT match the grammar and falls through to normal `deriveMessage` routing (as a new-goal candidate, since it is not a recognized verb) — asserting the grammar match is structural (verb + numeric arg), not a bare string-prefix check.

### TC-152-08: an approval SURVIVES A SIMULATED RESTART — LOAD-BEARING (the crux fix over OpenClaw's `dmPolicy`)
- **Requirement:** REQ-152-04
- **Input:** construct adapter/store instance #1 backed by a real temp-file `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` path; drive the owner's `"approve 77"` flow to completion (TC-152-03); **discard instance #1 entirely** (no shared in-memory state — simulate a process restart) and construct a brand-new, independent adapter/store instance #2 pointed at the SAME store file path (fresh `Load()` at assembly, exactly as a restarted orchestrator process would do).
- **Expected output:** instance #2, with zero in-memory knowledge of instance #1's approval, accepts a plaintext `"status"` command from sender `77` and routes it normally (`MsgStatus` derived) — the approval persisted across the simulated restart, closing the exact hole named in ADR 063's motivation (OpenClaw's `dmPolicy` pairings do not survive a process bounce; this store does).
- **Assertions:** unit test explicitly constructs two separate `Adapter`/`Store` object graphs (not two calls on the same object) sharing only the file path, and asserts instance #2 derives `MsgStatus` for sender `77` without any setup step other than `Load()` from the shared path.
- **Edge cases:** if the store file does not exist yet at instance #2's construction (e.g. the approval was never actually persisted due to a bug), instance #2 must reject sender `77` as unknown/pending — this negative control proves the test is actually exercising on-disk persistence and not an accidental shared in-memory reference.

### TC-152-09: owner ID misconfiguration fails fast, not silently-inert
- **Requirement:** REQ-152-05
- **Input:** `AUTH_MODE=pairing` with `AGENT_BUILDER_TELEGRAM_OWNER_ID` unset or blank.
- **Expected output:** fail-fast configuration error at assembly (an owner-less pairing mode has no one who can ever approve anyone, which is a footgun the config layer should catch rather than silently shipping a channel that can never onboard a sender).
- **Assertions:** unit test asserts assembly returns an error for `pairing` mode with `OWNER_ID` unset/blank, and succeeds when it is set to a valid numeric ID.
- **Edge cases:** a non-numeric `OWNER_ID` value is also a fail-fast config error (normalization applies to the owner ID exactly as it does to approved-store entries — task 151's normalize rule, TC-151-11 — so the owner ID is stored/compared in the same canonical numeric form).

## Notes
Depends on task 151 (`authz` store, mode-decision seam, `envelope`/`disabled`/`allowlist` modes) — `pairing` mode extends the same store and reuses the same accept-plaintext→armor→`deriveMessage` pipeline for already-approved senders (TC-152-06 is effectively a regression check against task 151's `allowlist` behavior, now reached via the `pairing` mode branch). TC-152-05 and TC-152-08 are the two load-bearing assertions called out explicitly in ADR 063: the owner-gate anti-self-approval control, and the restart-survival persistence proof that is this whole feature's reason for existing over adopting OpenClaw's `dmPolicy` verbatim. No live Telegram bot token is available for this task; L5/L6 live-bot verification is a documented follow-on (see task file Verification plan).
