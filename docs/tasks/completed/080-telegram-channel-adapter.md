# Task 080: Telegram channel adapter + armor guard

**Project:** agent-builder
**Created:** 2026-06-27
**Revised:** 2026-06-27 — ADR 045 accepted; open questions resolved; scoped to consume
  `internal/envelope` (task 096); GoalSource interface corrected to `supervisor.GoalSource`
  per ADR 044; dependency on task 096 added.
**Status:** backlog

## Goal

Build the orchestrator's secure goal-source channel: a Telegram bot adapter
(`internal/channel/telegram`) that polls the Telegram Bot API for updates, applies the
app-layer envelope from `internal/envelope` (Ed25519 sign-then-verify for authenticity,
X25519+AEAD for confidentiality), routes the decrypted plaintext through `armor.Guard`,
and delivers a validated goal over the `supervisor.GoalSource` seam.

This task **consumes** `internal/envelope` (task 096). It does **not** define envelope
primitives — those live in the shared leaf, which both this task and task 083 use.

## Context

ADR 042 selects Telegram + app-layer cryptographic envelope as the human↔orchestrator
channel. ADR 045 (accepted 2026-06-27) resolves how the envelope is implemented and
how armor is wired on the channel path:

- `internal/envelope` (task 096) provides `Sign`/`Verify` (Ed25519) and `Seal`/`Open`
  (X25519+AEAD) and `ReplayCache`. Task 080 wires them into the channel path.
- armor runs on the **decrypted plaintext**, after envelope verification and decryption,
  and before any `supervisor.GoalSource` delivery. A `Block`/`Quarantine`/fail-closed
  decision drops the goal and emits an audit event. `armor.Guard` is reused directly —
  no new armor adapter is needed.
- The `GoalSource` seam interface is `supervisor.GoalSource` (relocated to
  `internal/supervisor` by ADR 044). `recipe.GoalSource` no longer exists at that path.

**Channel path (exact order — ADR 045 §4):**

```
Telegram getUpdates → adapter pulls opaque blob
  → envelope.Verify (Ed25519 sig over ciphertext; unknown key → audit + drop)
  → ReplayCache.Check (stale/replayed → audit + drop)
  → envelope.Open (X25519+AEAD → plaintext goal)
  → armor.Guard.DecideContent(plaintext)
        ├─ Allow    → deliver over supervisor.GoalSource; advance offset
        └─ Block/Quarantine/fail-closed → DROP + audit event + advance offset
```

**Blocked by Cluster A (tasks 076–079) and task 096.** The `supervisor.GoalSource`
seam interface and `internal/envelope` must both be stable before this adapter can be
written against them.

## Requirements

| Req ID     | Description                                                                                                                                                 | Priority  |
|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-080-01 | Telegram bot adapter polls `getUpdates` (or handles webhooks); correctly advances the offset on consume. | must have |
| REQ-080-02 | Envelope verification: messages from unknown Ed25519 keys are rejected before armor; replayed nonces (per `ReplayCache`) are rejected. Both rejections emit an audit event and advance the offset. | must have |
| REQ-080-03 | armor guard (`armor.Guard`) is wired on the **decrypted plaintext** before `supervisor.GoalSource` delivery; prompt-injection payloads are blocked and dropped (audit event emitted). | must have |
| REQ-080-04 | The adapter satisfies `supervisor.GoalSource` (NOT `recipe.GoalSource` — that interface does not exist at `internal/recipe` as of ADR 044). | must have |
| REQ-080-05 | Bot token, Ed25519 private key, and X25519 private key are not logged or persisted in plaintext. | must have |

## Readiness gate

- [x] Test spec `080-telegram-channel-adapter-test-spec.md` exists (written first — 2026-06-27)
- [x] ADR 045 accepted (2026-06-27) — all open design questions resolved
- [ ] Task 076 merged (recipe type + `supervisor.GoalSource` interface stable)
- [ ] Task 077 merged (runtime assembles from recipe)
- [ ] Task 078 merged (gate-existence assertion)
- [ ] Task 079 merged (second proof recipe)
- [ ] **Task 096 merged** (`internal/envelope` leaf implemented — this is the new hard dependency)
- [ ] `make check` green before branching

## Acceptance criteria

- [ ] [REQ-080-01] TC-080-01: Stub Telegram server delivers a well-formed encrypted+signed envelope → adapter decrypts, delivers `supervisor.Task{Goal: "..."}` matching original plaintext, advances offset to `update_id + 1`.
- [ ] [REQ-080-02] TC-080-02: Unknown Ed25519 key → goal not delivered; `armor.Guard` invocation count == 0; one audit rejection event; offset advanced.
- [ ] [REQ-080-02] TC-080-03: Replayed nonce (same envelope accepted once, then replayed) → second call produces no goal; audit rejection event with `"replay"` or `"nonce"` in reason; offset advanced.
- [ ] [REQ-080-03] TC-080-04: Prompt-injection plaintext (armor stub returns `Block`) → goal not delivered; `armor.Guard` was invoked exactly once (on the decrypted plaintext); audit event with armor block reason; offset advanced.
- [ ] [REQ-080-04] TC-080-05: `var _ supervisor.GoalSource = &telegram.Adapter{}` compiles.
- [ ] [REQ-080-05] TC-080-06: Debug logs do not contain the bot token sentinel or private key bytes (hex or PEM).

## Verification plan

- **Highest level achievable:** L2 (unit tests with stub Telegram HTTP server and stub armor process). An L6 live bot test requires a real Telegram bot token and is a follow-on.
- **Harness command:**
  ```
  go test -count=1 ./internal/channel/telegram/...
  make check
  ```
  Expected:
  - `ok github.com/tkdtaylor/agent-builder/internal/channel/telegram`
  - `All checks passed.`

## Out of scope

- Orchestrator core logic (task 081).
- Supporting Signal or Matrix transports (future swappable transports).
- Key rotation.
- `internal/envelope` implementation (task 096 — this task depends on it).

## Dependencies

- Task 076 (recipe type + `supervisor.GoalSource` seam interface stable — ADR 044)
- Task 077 (runtime assembles from recipe)
- Task 078 (gate-existence assertion)
- Task 079 (second proof recipe)
- **Task 096** (`internal/envelope` leaf — Ed25519 sign/verify, X25519+AEAD seal/open, ReplayCache)
- Informs: task 081 (orchestrator core needs this channel adapter)
