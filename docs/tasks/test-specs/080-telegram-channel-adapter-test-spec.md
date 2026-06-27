# Test spec — Task 080: Telegram channel adapter + Ed25519 envelope + armor guard

**Linked task:** `docs/tasks/backlog/080-telegram-channel-adapter.md`
**Written:** 2026-06-27
**Status:** stub — blocked by Cluster A (tasks 076–079)

## Context

The orchestrator's goal-source channel: a Telegram bot adapter that receives
messages, applies an Ed25519 envelope layer for app-level E2E encryption (so
Telegram carries ciphertext it cannot read), and routes the decrypted payload
through armor before handing it to the orchestrator goal-intake.

This task is **blocked by Cluster A** (tasks 076–079 must land first so the
`GoalSource` seam interface is stable and the recipe type the Telegram adapter will
implement is fully defined).

**Detailed task shape is deferred** pending Cluster A delivery. Shape parameters
that are known now:
- New package `internal/channel/telegram` — Telegram Bot API polling + webhook.
- New package `internal/envelope` — Ed25519 sign/verify wrapper over the agent-mesh
  block's envelope format (Ed25519 + replay prevention).
- `armor` guard is wired on the inbound message path before goal delivery
  (armor is already adopted; this task wires it to the channel, not the executor).
- The adapter must satisfy the `GoalSource` seam interface defined in task 076.

## Requirements coverage (preliminary)

| Req ID     | Description                                                          | Test cases |
|------------|----------------------------------------------------------------------|------------|
| REQ-080-01 | Telegram bot receives messages and polls/webhooks correctly          | TC-080-01  |
| REQ-080-02 | Ed25519 envelope: messages signed by wrong key are rejected          | TC-080-02  |
| REQ-080-03 | Replay prevention: replayed message ID is rejected                   | TC-080-03  |
| REQ-080-04 | armor guard rejects prompt-injection on the channel payload          | TC-080-04  |
| REQ-080-05 | adapter satisfies the GoalSource interface (task 076 seam)           | TC-080-05  |
| REQ-080-06 | Bot token and private key are not logged or persisted in plaintext   | TC-080-06  |

## Pre-implementation checklist

- [ ] Task 076 merged (GoalSource seam interface stable)
- [ ] Task 077 merged (runtime assembler reads recipes)
- [ ] agent-mesh block API surveyed for Ed25519 envelope reuse
- [ ] armor block API for channel-mode invocation confirmed
- [ ] All test cases below refined into full inputs/expected-outputs

---

## Test cases (stubs — to be expanded before implementation begins)

### TC-080-01 — Telegram adapter receives a well-formed encrypted message and delivers a goal

- **Requirement:** REQ-080-01, REQ-080-02
- **Level:** L2 (unit test with stubbed Telegram HTTP server)
- **Status:** stub

**Input:** A stub Telegram getUpdates response carrying a correctly-signed,
correctly-encrypted payload from the known client key.

**Expected output:**
- The adapter decrypts and verifies the envelope.
- Delivers the plaintext goal to the orchestrator's `GoalSource` read path.
- Marks the update as consumed (offset advanced).

---

### TC-080-02 — A message signed by an unknown key is rejected before armor

- **Requirement:** REQ-080-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** A Telegram update with a payload signed by a key not in the trusted key set.

**Expected output:**
- Envelope verification fails; the payload is dropped.
- No goal is delivered to the orchestrator.
- An audit event is emitted for the rejected message.

---

### TC-080-03 — A replayed message nonce/ID is rejected

- **Requirement:** REQ-080-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** A valid, correctly-signed message replayed a second time (same nonce).

**Expected output:**
- The replay check fires; the message is dropped.
- Error is logged; no goal delivered.

---

### TC-080-04 — armor rejects a prompt-injection payload

- **Requirement:** REQ-080-04
- **Level:** L2 (unit test with fake armor process)
- **Status:** stub

**Input:** A validly-signed, correctly-encrypted payload whose plaintext contains a
known prompt-injection pattern (e.g. "IGNORE PREVIOUS INSTRUCTIONS").

**Expected output:**
- armor blocks the payload before it reaches orchestrator goal-intake.
- The channel adapter surfaces the rejection (drops the goal, emits audit event).

---

### TC-080-05 — Telegram adapter compiles against GoalSource interface

- **Requirement:** REQ-080-05
- **Level:** L2 (compile-time)
- **Status:** stub

**Input:** Type-assertion in the package test:
`var _ recipe.GoalSource = &telegram.Adapter{}`.

**Expected output:** Compiles without error.

---

### TC-080-06 — Bot token and private key are not leaked in logs

- **Requirement:** REQ-080-06
- **Level:** L2 (unit test — log scrub)
- **Status:** stub

**Input:** Enable debug logging; perform a normal message receive cycle.

**Expected output:**
- Log output does not contain the bot token string.
- Log output does not contain the Ed25519 private key bytes (hex or PEM).

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 with a stub Telegram HTTP server (no real bot
  required for the unit tests; a live bot test is L6 and optional for this task).
- **L2 harness command (to be confirmed post-Cluster A):**
  ```
  go test -count=1 ./internal/channel/telegram/... ./internal/envelope/...
  ```
  Expected: `ok` on both packages.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Open questions (to resolve before starting implementation)

1. Does the agent-mesh block expose a Go library for Ed25519 envelope operations,
   or does it require an out-of-process call? This determines whether `internal/envelope`
   wraps the block binary (like `internal/audit`) or links against its Go package.
2. What is the key-distribution model? Shared symmetric key vs asymmetric keypair?
   The ADR says Ed25519 (asymmetric signing), but key distribution to the human's
   Telegram client is unspecified.
3. Replay prevention window: time-based (reject messages older than N seconds) or
   nonce-set-based (reject any seen nonce)? Affects memory-guard scope.

## Out of scope

- Orchestrator core logic (task 081) — this task is only the channel adapter.
- Supporting messengers other than Telegram (Signal/Matrix are noted alternatives
  in ADR 042; they are future swappable transports, not in scope here).
- Key rotation (a follow-on once the envelope layer ships).
