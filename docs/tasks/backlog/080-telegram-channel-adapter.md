# Task 080: Telegram channel adapter + Ed25519 envelope + armor guard

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Build the orchestrator's secure goal-source channel: a Telegram bot adapter that
polls (or handles webhooks from) the Telegram Bot API, applies an app-layer Ed25519
signing+encryption envelope so Telegram's servers carry ciphertext they cannot read,
and routes the decrypted payload through the armor block before delivering a goal to
the orchestrator. The adapter satisfies the `GoalSource` seam interface from task 076.

## Context

ADR 042 selects Telegram + app-layer Ed25519 envelope as the human↔orchestrator
channel. The security model: Telegram is a dumb, untrusted transport; our envelope
layer provides E2E encryption+authentication owned by the ecosystem. armor (already
adopted) is wired on the inbound path to guard against prompt injection.

**Blocked by Cluster A (tasks 076–079).** The `GoalSource` seam interface must be
stable before this adapter can be written against it. Detailed task shape is refined
once Cluster A lands.

## Requirements

| Req ID     | Description                                                                                                                           | Priority  |
|------------|---------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-080-01 | Telegram bot adapter polls `getUpdates` (or handles webhooks); correctly advances the offset on consume. | must have |
| REQ-080-02 | App-layer Ed25519 envelope: messages from unknown keys are rejected before armor; replayed nonces are rejected. | must have |
| REQ-080-03 | armor guard is wired on the decrypted payload before goal delivery; prompt-injection payloads are blocked. | must have |
| REQ-080-04 | The adapter satisfies the `GoalSource` interface from `internal/recipe`. | must have |
| REQ-080-05 | Bot token and Ed25519 private key are not logged or persisted in plaintext. | must have |

## Readiness gate

- [x] Test spec `080-telegram-channel-adapter-test-spec.md` exists (written first)
- [ ] Task 076–079 merged (recipe seam + GoalSource interface stable)
- [ ] agent-mesh block Ed25519 envelope API surveyed
- [ ] armor channel-mode wiring confirmed with armor block maintainer
- [ ] Open questions in test spec resolved (key distribution model; replay window;
  agent-mesh API: library vs binary)

## Acceptance criteria

- [ ] [REQ-080-01] TC-080-01: Stub Telegram server delivers well-formed encrypted message → adapter decrypts, delivers goal, advances offset
- [ ] [REQ-080-02] TC-080-02: Unknown key → message dropped, no goal delivered, audit event emitted
- [ ] [REQ-080-02] TC-080-03: Replayed nonce → message dropped, error logged, no goal delivered
- [ ] [REQ-080-03] TC-080-04: Prompt-injection payload (armor stub returns block) → goal not delivered, audit event emitted
- [ ] [REQ-080-04] TC-080-05: `var _ recipe.GoalSource = &telegram.Adapter{}` compiles
- [ ] [REQ-080-05] TC-080-06: Debug logs contain no bot token or private key bytes

## Verification plan

- **Highest level achievable:** L2 (unit tests with stub Telegram HTTP server and
  stub armor process). An L6 live bot test is a follow-on.
- **Harness command:**
  ```
  go test -count=1 ./internal/channel/telegram/... ./internal/envelope/...
  make check
  ```
  Expected:
  - Unit tests → `ok` on both packages
  - `make check` → `All checks passed.`

## Out of scope

- Orchestrator core logic (task 081).
- Supporting Signal or Matrix transports (future swappable transports).
- Key rotation.

## Dependencies

- Task 076 (recipe type + selector — `GoalSource` interface)
- Task 077 (runtime assembles from recipe)
- Task 078 (gate-existence assertion)
- Task 079 (seam proven with two recipes)
- Informs: task 081 (orchestrator core needs this channel adapter)
