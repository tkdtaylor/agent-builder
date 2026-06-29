# Test spec — Task 117: Telegram wiring (message-aware)

**Linked task:** `docs/tasks/backlog/117-telegram-message-wiring.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §2/§Recommended-decomposition (Telegram `Adapter` emits typed
`Message`s; `ReplyAdapter` carries acks/status/results; wired into `assembleOrchestrate`
behind config — last because it depends on the message protocol, status, info, and cancel).

## Context

ADR 054 §2 notes Telegram already implements both seams but is not wired in: the live
orchestrate path hardcodes `newEnvGoalSource` + `newLogReporter`. This task is the **last** in
the decomposition: make the Telegram `Adapter` emit typed `Message`s (task 113's
`supervisor.MessageSource`), keep `ReplyAdapter` as the outbound `Reporter` for
acks/status/results, and wire both into `assembleOrchestrate` behind config. It is **wiring +
message-type mapping**, not a from-scratch build — both adapters already exist
(`internal/channel/telegram/adapter.go` `Adapter`, `reply.go` `ReplyAdapter`).

### Grounded current state (verified against code)

- `telegram.Adapter.Next()` today returns `(supervisor.Task, bool, error)` — a `GoalSource`.
  This task changes/augments it to satisfy the new `supervisor.MessageSource`
  (`Next() (Message, bool, error)`), deriving `MessageKind`/`GoalID` from the message text or
  reply-to **at the adapter edge** — the control plane only ever sees `Message.GoalID` (ADR
  054 §2).
- `telegram.ReplyAdapter.Report(ctx, text)` already implements `supervisor.Reporter` — reused
  as-is for acks/status/results.
- `assembleOrchestrate` (`internal/cli/orchestrate.go`) hardcodes the env source + log
  reporter; this task selects Telegram behind config (e.g. `AGENT_BUILDER_INBOUND=telegram`)
  so the **env/stdin path stays the default** for local tests.

### Per-message goal IDs (ADR 054 §Recommended-decomposition — load-bearing)

The adapter derives a per-message `GoalID` from the Telegram chat/message ID (or a reply-to /
threaded goalID), so independent goal tracking holds across concurrent goals. A `new-goal`
gets a fresh goalID from its message identity; a `status`/`info`/`cancel` reply-to threads the
**existing** goalID. The mapping happens at the adapter edge; the control plane sees only
`Message.GoalID`.

### Security invariants preserved (existing telegram + ADR 054 §6)

The existing Telegram envelope verification (Ed25519) + decryption (X25519+AEAD) + armor
ingestion pipeline (tasks 080/097/098) is **unchanged** — message-kind derivation happens
**after** envelope verify + armor, on already-trusted plaintext. The control plane's gates
(policy fail-closed, self-repo bright line, audit chain) fire downstream regardless of the
inbound channel. Wiring Telegram does not add a path around any gate.

## Requirements coverage

| Req ID      | Description                                                                                                                  | Test cases             |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-117-01  | `telegram.Adapter` satisfies `supervisor.MessageSource`; `Next()` emits typed `Message`s (kind derived at the adapter edge) | TC-117-01              |
| REQ-117-02  | `MessageKind`/`GoalID` derivation maps a bare text → `MsgNewGoal` (fresh goalID); `status`/`info`/`cancel` text/reply-to → the matching kind + threaded goalID | TC-117-02 |
| REQ-117-03  | `ReplyAdapter` (Reporter) carries acks/status/results outbound, reused unchanged                                            | TC-117-03              |
| REQ-117-04  | `assembleOrchestrate` selects Telegram inbound+outbound behind config; env/stdin stays the **default**                      | TC-117-04              |
| REQ-117-05  | Per-message goal IDs are derived from chat/message identity so concurrent goals track independently (no ID collision across messages) | TC-117-05 |
| REQ-117-06  | The envelope-verify + armor ingestion pipeline is unchanged; kind derivation runs only on verified plaintext                | TC-117-06              |

---

## Test cases

### TC-117-01 — `Adapter` satisfies `MessageSource`; `Next()` emits a typed `Message` (L2)

- **Requirement:** REQ-117-01
- **Level:** L2 (compile-time + unit with a stub getUpdates server)

**Input:** Build the package; stand up the existing stub `getUpdates` httptest server (as in
`adapter_test.go`) returning one verified update carrying the plaintext `"add rate limiting"`.

**Expected output (assertions):**
- Compile-time `var _ supervisor.MessageSource = (*telegram.Adapter)(nil)`.
- `Adapter.Next()` returns a `Message{Kind: MsgNewGoal, Goal.Spec: "add rate limiting"}`,
  `ok=true`, after the existing envelope-verify + armor pipeline.

---

### TC-117-02 — Kind/GoalID derivation at the adapter edge (L2)

- **Requirement:** REQ-117-02
- **Level:** L2 (table-driven unit test)

**Input → Expected (table — each fed as a verified Telegram update plaintext / reply-to):**

| Update plaintext / reply-to                  | `Kind`        | `GoalID`                          |
|----------------------------------------------|---------------|-----------------------------------|
| `add rate limiting` (no reply-to)            | `MsgNewGoal`  | fresh, derived from chat/message id |
| `status` (reply-to a known goal's message)   | `MsgStatus`   | the threaded goalID               |
| `info also handle retries` (reply-to goal)   | `MsgInfo`     | the threaded goalID; `Text == "also handle retries"` |
| `cancel` (reply-to goal)                     | `MsgCancel`   | the threaded goalID               |

- A bare `status` with no reply-to and no goalID → `MsgStatus` with **empty** GoalID (fleet).
- The derivation never panics on a malformed/partial command; it falls back to `MsgNewGoal`
  or a graceful kind (assert no panic; assert it does not silently drop the message).

---

### TC-117-03 — `ReplyAdapter` carries acks/status/results unchanged (L2)

- **Requirement:** REQ-117-03
- **Level:** L2 (unit with the existing stub `sendMessage` server)

**Input:** Construct `ReplyAdapter`; call `Report(ctx, "goal-7: Dispatching (1/2 sub-goals)")`
(a status-style result).

**Expected output (assertions):**
- Exactly one POST to `sendMessage` (reusing the existing reply test harness); the body
  carries the sealed envelope (not plaintext) — i.e. `ReplyAdapter` is used **as-is**, no
  regression to the envelope-sealing behavior verified in task 098.

---

### TC-117-04 — `assembleOrchestrate` selects Telegram behind config; env default preserved (L2)

- **Requirement:** REQ-117-04
- **Level:** L2 (unit on the assembler)

**Input A — `AGENT_BUILDER_INBOUND` unset / `env`:** assemble the orchestrate config.

**Expected A:**
- The inbound `MessageSource` is the env/stdin source and the Reporter is the log reporter
  (the existing default — unchanged for local tests).

**Input B — `AGENT_BUILDER_INBOUND=telegram` (+ the required telegram config/secrets):**
assemble the orchestrate config.

**Expected B:**
- The inbound `MessageSource` is the `*telegram.Adapter` and the Reporter is the
  `*telegram.ReplyAdapter`.
- Missing required telegram config (e.g. bot token / keys) → a clear assembly error
  (fail-fast), not a nil-adapter panic at first `Next()`.

---

### TC-117-05 — Per-message goal IDs derived from chat/message identity (L2)

- **Requirement:** REQ-117-05
- **Level:** L2 (unit test)

**Input:** Two **distinct** verified updates from different chats/messages, each a `new-goal`.

**Expected output (assertions):**
- The two emitted `Message`s carry **distinct** `GoalID`s (derived from their distinct
  chat/message identities) — confirming concurrent goals from Telegram track independently,
  no collision.
- A follow-up `info` reply-to the first goal's message threads the **first** goalID (not the
  second), so the info routes to the correct goal under task 113's router.

---

### TC-117-06 — Envelope-verify + armor pipeline unchanged; kind derivation on plaintext only (L2)

- **Requirement:** REQ-117-06
- **Level:** L2 (unit test, regression)

**Input:** An update that **fails** envelope verification (tampered signature) and a separate
update that armor **rejects** (injection content), each carrying a plaintext that *would* be a
valid `cancel <goalID>` command if it reached derivation.

**Expected output (assertions):**
- Both are dropped by the existing verify/armor pipeline **before** kind derivation — the
  emitted result is **not** an `MsgCancel` (the message never reaches the control plane). The
  existing task-080/097/098 rejection behavior is unchanged (regression: the same audit-emit
  / drop path fires).
- Kind derivation is only ever applied to **verified, armor-passed plaintext** — assert the
  derivation function is not reached for the rejected updates (spy or coverage on the
  derivation path).

---

## Verification plan

- **Highest level achievable: L6** — drive new-goal/status/info/cancel over a real Telegram
  bot and observe each end to end on the live binary. L2 (adapter `MessageSource` + derivation
  + assembler-selection unit tests) is the CI ceiling (a real bot token is not CI-automatable).
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (operator-run, dev host):** set `AGENT_BUILDER_INBOUND=telegram` with a real bot token
  + the SEC envelope keys; run `agent-builder orchestrate`; from the chat send a goal, then a
  `status` reply, an `info` reply, and a `cancel` reply to that goal; observe each routed and
  answered (new goal starts; status answers; info folds; cancel tears down). Record the four
  round-trips in the verify commit.

## Out of scope

- The `Message`/`MessageKind`/`MessageSource` types — task 113 (this task makes the Telegram
  adapter **emit** them).
- The status/info/cancel **handler bodies** — tasks 114/115/116 (this task is the inbound/
  outbound wiring + kind mapping; the handlers already exist when 117 runs — that is why 117
  is last).
- The async control loop + registry + semaphore — task 112.
- Changing the Telegram envelope/armor/audit pipeline (tasks 080/097/098) — preserved
  unchanged; derivation runs only on verified plaintext.
- A from-scratch Telegram client — both adapters already exist; this is wiring + mapping.

## Dependencies note

This task depends on **112, 113, 114, 115, and 116 all existing** — the four message kinds
and their handlers must be in place before Telegram can drive them end to end. It is **last**.
