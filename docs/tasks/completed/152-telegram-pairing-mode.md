# Task 152: Telegram `pairing` mode — in-chat owner-approve flow

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** backlog

## Goal

Add the `pairing` auth mode on top of task 151's `authz` store and mode-decision seam: an
unknown sender triggers an owner-notified pending-approval flow, and the configured owner
can approve/deny in-chat. Approvals persist to the same `0600` store from task 151, so they
survive a process restart — the specific hole ADR 063 identifies in OpenClaw's `dmPolicy`.

## Context

Read `docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md` in full
(Decision 3 and Decision 4 are the direct source for this task) before starting.

Flow for an unknown sender in `pairing` mode (ADR 063 Decision 3):

1. Unknown sender messages the bot.
2. The adapter emits a `pairing_request` audit event, replies to the sender that access is
   pending, and notifies the owner's chat with an `approve <id>` / `deny <id>` prompt.
3. The owner replies `approve <id>` or `deny <id>`. **Only messages whose sender ID equals
   the configured `AGENT_BUILDER_TELEGRAM_OWNER_ID` are honored** for approve/deny.
4. On `approve <id>`, `<id>` is added to the persisted approved-sender store from task 151.
   On `deny <id>`, the request is dropped (audited); the sender is not permanently
   blocked and may be re-evaluated on a future message.

**Load-bearing ordering rule (ADR 063 Decision 3, do not violate):** the approve/deny
grammar MUST be parsed and owner-gated **before** `deriveMessage`'s command-verb routing
(`status`/`info`/`cancel`/`confirm`/new-goal). If a stranger could reach `deriveMessage`
with the text `approve <own-id>`, they could self-approve. The gate is: (sender ID ==
configured owner ID) AND (text matches the `approve`/`deny <numeric-id>` grammar) — checked
as a distinct branch ahead of the normal command-verb switch, on the sender-ID identity,
not on the command grammar alone. A non-owner sending `approve …` text is ordinary
(unapproved) input and gets the pending/pairing_request path, never the approval path.

Reference: `internal/channel/telegram/adapter.go` (`deriveMessage`, `adapter.go:268`),
task 151's `authz` store and mode-decision seam, `internal/audit/audit.go` (add a
`pairing_request`-shaped action to the closed enum if one does not already exist after
task 151 — task 151 may or may not have introduced generic mode-reject actions; this task
needs a request/approve/deny-specific action or Detail.Reason values, whichever keeps the
enum closed and typed).

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-152-01 | An unknown sender's message in `pairing` mode emits a `pairing_request` audit event, sends the sender a distinguishable "pending" reply, and notifies the owner's chat with the sender's ID and the `approve <id>`/`deny <id>` instruction; no `supervisor.Message` is derived for that update. | must have |
| REQ-152-02 | The owner's `approve <id>` adds `<id>` to the persisted store (task 151's `authz.Store`) and confirms to the owner; the owner's `deny <id>` records the refusal (audited) without approving, and does not permanently block future re-requests from that ID. Malformed approve/deny grammar (missing/non-numeric ID) is rejected without crashing and without mutating the store. | must have |
| REQ-152-03 | Only messages whose sender ID equals `AGENT_BUILDER_TELEGRAM_OWNER_ID` are honored for approve/deny; a non-owner sender's `approve <any-id>` (including its own ID) is treated as ordinary unapproved input, routed through the pending/pairing_request path, and never mutates the store. An already-approved sender's plaintext continues to route normally (task 151's accepted-plaintext pipeline), bypassing the pairing/pending machinery entirely. | must have |
| REQ-152-04 | The approve/deny grammar check runs strictly before `deriveMessage`'s verb-routing switch, gated on sender-ID identity (not just text shape); the owner's own ordinary commands (e.g. a later `status`) still route normally when they do not match the approve/deny grammar. An approval made in one adapter/store instance is readable by an independently constructed second instance sharing only the store file path (the restart-survival property). | must have |
| REQ-152-05 | `AGENT_BUILDER_TELEGRAM_OWNER_ID` is required (fail-fast config error) whenever `AUTH_MODE=pairing`; its value is normalized to the same canonical numeric form as approved-store entries (task 151's normalization rule). | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/152-telegram-pairing-mode-test-spec.md` exists (written first — 2026-07-01)
- [ ] ADR 063 read in full
- [ ] Task 151 merged (`authz` store, mode-decision seam, `envelope`/`disabled`/`allowlist` modes)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-152-01] TC-152-01: unknown sender triggers `pairing_request` audit + pending reply to sender + distinct owner notification (sender ID + approve/deny instruction); no message derived.
- [ ] [REQ-152-01] TC-152-02: an unknown sender's command-verb-shaped text (`"status"`) still hits the pending path, never `deriveMessage`.
- [ ] [REQ-152-02] TC-152-03: owner's `approve <id>` adds the ID to the store, persists it, audits it, confirms to owner, and derives no message.
- [ ] [REQ-152-02] TC-152-04: owner's `deny <id>` does not add the ID, audits the denial, confirms to owner, and a later message from that ID still re-enters the pending path.
- [ ] [REQ-152-03] TC-152-05: a stranger's `approve <own-id>` does NOT self-approve — routes as ordinary pending input, store unchanged, no "approved" reply ever sent to the stranger. (LOAD-BEARING)
- [ ] [REQ-152-03] TC-152-06: an already-approved sender's plaintext commands route normally, without invoking the pairing/pending machinery.
- [ ] [REQ-152-04] TC-152-07: the owner's `approve <id>` is consumed by the grammar (never reaches `deriveMessage`); the owner's separate, later `"status"` message still routes normally as `MsgStatus`.
- [ ] [REQ-152-04] TC-152-08: an approval made by one adapter/store instance is visible to an independently constructed second instance sharing only the store file path (restart-survival). (LOAD-BEARING)
- [ ] [REQ-152-05] TC-152-09: `pairing` mode with `OWNER_ID` unset/blank/non-numeric fails assembly; a valid numeric value succeeds.

## Verification plan

- **Highest level achievable now: L2/L3.** No live Telegram bot token is available; the
  owner-notify and sender-reply paths are exercised via the same fake `Reporter`/outbound
  sink pattern already used in `reply_test.go`/`adapter_test.go`. `make fitness` re-confirms
  F-003/F-007 isolation is unaffected.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
  make check
  ```
  Expected: all TC-152-01..09 pass, including the two LOAD-BEARING cases (TC-152-05,
  TC-152-08); `make check` → `All checks passed.`
- **Runtime observation (documented follow-on, no live bot token available):** once a real
  bot token exists, exercise the full pairing flow against a real Telegram chat: an unknown
  test account messages the bot, the owner approves in-chat, the orchestrator process is
  restarted, and the approved account's next message is still accepted. Record as L5/L6
  residual in the verify commit, not claimed here.

## Out of scope

- `open` mode + startup warning — task 153.
- A permanent deny-list distinct from "not yet approved" — a denied sender may re-request.
- Rate-limiting or de-duplicating repeated pending-notifications to the owner from the same
  unapproved sender spamming the bot — a documented follow-on, not required by this task.
- Multi-owner support — v1 is a single configured owner ID.

## Dependencies

- **Blocks on:** task 151 (`authz` store, mode-decision seam, `envelope`/`disabled`/`allowlist` modes).
- **Blocks:** task 153 (`open` mode + docs).
