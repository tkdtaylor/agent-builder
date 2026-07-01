# ADR 063 — Telegram sender-ID channel-auth modes (opt-in easy on-ramp alongside the envelope default)

**Status:** proposed
**Date:** 2026-07-01

**Motivated by:** the operator on-ramp gap that surfaces once the laptop-side
`examples/agent-cli` (ADR 062, tasks 148–150) exists. That client makes the strong
envelope path *usable*, but it is still the only way to command the bot: every inbound
message must be an Ed25519-signed + X25519+AEAD-sealed `envelope.Envelope`
(`internal/channel/telegram/adapter.go:178` → `envelope.VerifyAndOpen`). An operator who
does not want to run a key-holding companion CLI has no way in at all. OpenClaw solves the
same problem with a `dmPolicy` model (accept plaintext DMs from allowlisted / paired /
any senders), but ships a hole: its pairings do not persist across restarts, so every
process bounce re-opens the approval flow. This ADR records an **opt-in, lower-security
sender-ID auth mode** for the Telegram adapter that mirrors `dmPolicy` and **fixes the
persistence hole**, while keeping the strong envelope path as the untouched default.

This is a step toward remote control of the agent and is deliberately sequenced **after**
the local operator CLI is built and tested (tasks 148–150). Status is therefore
`proposed`; the implementation tasks below belong in `docs/tasks/backlog/`, downstream of
148–150.

## Context

Today the Telegram adapter is transport-untrusting by construction. Every update is parsed
as an `envelope.Envelope` and run through `envelope.VerifyAndOpen`, which enforces
verify → replay-check → open in a fixed order (ADR 045). The sender's Telegram identity is
**irrelevant** — authenticity comes entirely from the Ed25519 signature, and
confidentiality from the X25519+AEAD seal. Only *after* the envelope passes and armor
clears does `deriveMessage` (`adapter.go:268`) map the plaintext to a typed
`supervisor.Message` (new-goal / status / info / cancel / confirm).

That is the right **default** — it treats Telegram/Meta's servers as an untrusted relay
carrying ciphertext they cannot read, with per-message replay protection. But it forces
every operator to run a private-key-holding client. For lower-stakes deployments (a single
operator, a trusted phone, a throwaway target repo), that ceremony is a barrier, and the
absence of any plaintext path pushes people toward disabling the channel entirely or
forking the adapter — both worse than a governed, opt-in easy mode.

Two design constraints shape this ADR:

1. **The security-first default must not move.** The envelope path is the shipped default
   and the strong-security answer; the easy mode must be unreachable unless explicitly
   configured, and adding it must not change any existing behavior or break existing
   telegram/envelope tests. F-007 (`internal/envelope` leaf isolation) and the supervisor
   isolation invariant (F-003) stay green.
2. **Plaintext is a real, permanent tradeoff — not a footnote.** Any sender-ID mode
   accepts plaintext, which forfeits the envelope's end-to-end sealing and its
   per-message replay/nonce protection. That cost is the reason `envelope` stays default
   and must be stated plainly (§Decision 2 below), because it is exactly what a reviewer
   will check.

## Decision

Add a single **mode selector** on the Telegram channel — a new env var
`AGENT_BUILDER_TELEGRAM_AUTH_MODE`, aligned with the existing `AGENT_BUILDER_TELEGRAM_*`
family in `docs/spec/configuration.md`. Five values, `envelope` the default:

| Mode | Accepts | Sender-ID gate | Confidentiality / replay | Notes |
|------|---------|----------------|--------------------------|-------|
| `envelope` **(default, unset ⇒ this)** | Signed+sealed `envelope.Envelope` only | none (identity irrelevant) | **full** — X25519+AEAD sealing + nonce/timestamp replay | **Exactly today's behavior. Zero change when unset. MUST NOT be weakened.** |
| `allowlist` | **plaintext** commands | only from statically configured approved sender IDs | **lost** (see Decision 2) | Static approved set seeded into the persisted store at startup. |
| `pairing` | **plaintext** commands | approved senders; unknown senders enter an owner-approved in-chat flow | **lost** | Owner-gated approve/deny; approvals persist (Decision 3, 4). |
| `open` | **plaintext** from **any** sender | none | **lost** | **Footgun.** Explicit opt-in value only; emits a startup `WARNING` to stderr. Any account that finds the bot can command it. |
| `disabled` | nothing | — | — | Rejects all inbound; the channel is inert. |

### Decision 1 — one mode selector, `envelope` the default, never weakened

`AGENT_BUILDER_TELEGRAM_AUTH_MODE` is read once at `orchestrate` assembly time (alongside
the rest of the `AGENT_BUILDER_TELEGRAM_*` block, which is validated fail-fast today).
Unset or `envelope` reproduces today's adapter exactly — the `VerifyAndOpen` pipeline is
untouched and the sender ID is never consulted. An unrecognized value is a fail-fast
`ExitUsage` configuration error, consistent with the existing `AGENT_BUILDER_INBOUND`
handling (never a nil-adapter panic at first `Next()`).

**Invariant: the opt-in easy mode never changes behavior unless explicitly configured; the
shipped default remains the strong envelope.** The `envelope` branch is the security-first
answer and this ADR does not permit weakening it — a sender-ID mode is *added alongside*,
selectable only by an explicit env value.

### Decision 2 — the plaintext tradeoff (the crux)

In `allowlist` / `pairing` / `open` modes the adapter accepts **plaintext** commands, so it
**LOSES**, relative to `envelope`:

- **End-to-end confidentiality.** Commands *and* results transit Telegram/Meta servers in
  cleartext. Anyone who can read that transport (Telegram, Meta, a compelled operator, a
  network position on the reply path) sees the goal text and the agent's output. The
  envelope's X25519+AEAD seal is gone.
- **Per-message replay / nonce protection.** There is no `envelope.ReplayCache` guard on a
  plaintext message. Telegram's deliver-once semantics plus the adapter's offset-advance
  (`adapter.go:150`) give only **weak** replay mitigation: a given `update_id` is consumed
  once, but there is no cryptographic nonce binding and no freshness window.

What is **RETAINED** in every sender-ID mode (these are non-negotiable and must not be
bypassed):

- **`armor.Guard` on the plaintext.** The ingestion/injection guard
  (`contentGuard.DecideContent`, `adapter.go:224`) runs on the accepted plaintext exactly
  as it does on decrypted envelope plaintext today. Sender-ID acceptance is **not** a
  reason to skip armor — a trusted sender can still forward an injected payload.
- **Size caps** (SEC-001 message-length, SEC-002 body-limit; task 097) — `adapter.go:161`,
  `adapter.go:396`.
- **Audit events.** Every accept/reject decision emits an `audit.ActionChannelReject` (or
  an accept-side event) via the existing `audit.Sink`, never silently.

This lost/retained split is the inherent cost of "easy mode" and is the reason `envelope`
stays the default. An operator choosing a sender-ID mode is choosing convenience over
end-to-end sealing with eyes open; the ADR names the cost so that choice is informed.

### Decision 3 — `pairing` = owner-approves-in-chat, owner-gated *before* command routing

Bootstrap the operator's own Telegram sender ID as `owner` via config: a new
`AGENT_BUILDER_TELEGRAM_OWNER_ID` (numeric Telegram sender ID). Flow for an **unknown**
sender in `pairing` mode:

1. Unknown sender messages the bot.
2. The adapter emits a `pairing_request` audit event, replies to the sender that access is
   **pending**, and notifies the **owner's** chat:
   `User <id> requests access — reply "approve <id>" / "deny <id>"`.
3. The owner replies `approve <id>` or `deny <id>`. **Only messages whose sender ID equals
   the configured owner ID are honored** for approve/deny.
4. On `approve <id>`, `<id>` is added to the persisted approved-sender store (Decision 4).
   On `deny <id>`, the request is dropped (audited).

**The approve/deny grammar MUST be parsed and gated to the owner BEFORE normal command
routing** (`status` / `info` / `cancel` / `confirm` / new-goal in `deriveMessage`). If a
stranger could reach `deriveMessage` with the text `approve <own-id>`, they could
self-approve — so approve/deny is a distinct, owner-only pre-routing branch that runs on
the sender-ID check, not on the command grammar. A non-owner sending `approve …` is treated
as ordinary (unapproved) input and gets the pending/deny path, never the approval path.
This ordering is the load-bearing anti-self-approval control and is a required assertion in
the pairing task's test spec.

### Decision 4 — persistence across restarts (the load-bearing fix)

Approved sender IDs live in a **durable, plain-text, `0600` JSON store** on the
orchestrator host, loaded at startup so approvals **survive restarts** — this is the
specific hole OpenClaw's `dmPolicy` leaves open and the reason this mode is worth building
rather than just adopting.

- **Location / config.** Follow the repo's existing explicit-path env convention
  (`AGENT_BUILDER_AUDIT_RECORD`, `AGENT_BUILDER_RUN_RECORD` are direct file paths, blank =
  disabled). Propose `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` = a direct path to the JSON
  store file. Do **not** invent a parallel "state dir" mechanism — no such convention
  exists in the repo; the direct-path knob matches how audit/run records are already
  configured. The file is created `0600` if absent (matching the `0600` discipline in
  `internal/secrets` and `internal/audit`, e.g. `disk_oauth_source_test.go`,
  `verify_test.go:291`). It holds only **public** sender IDs — no keys, no secrets — so it
  is plain text by design and readable for debugging.
- **Format.** A small JSON document (approved sender IDs; may carry per-ID metadata such as
  approval timestamp/owner for audit). Plain text, human-inspectable, per the project's
  "plain text where possible" principle.
- **Load at startup.** Read once when the adapter is assembled; approvals persist across
  process restarts. A missing/empty file is graceful absence (no approvals yet), matching
  the `DiskOAuthSecretSource` graceful-absence pattern.
- **`allowlist` seeds the same store.** In `allowlist` mode the statically configured
  approved IDs are seeded into this same store at startup, so `allowlist` and `pairing`
  share one persisted approved-set representation (`allowlist` = static seed, no in-chat
  flow; `pairing` = seed + owner-approved growth).
- **Normalize on write and compare.** Sender IDs are canonicalized to a numeric form
  before storing and before every membership check, so a formatting difference (leading
  zeros, string vs int, whitespace) can never bypass the gate or create a duplicate
  approval. This normalization is a required test-spec assertion.

### Decision 5 — module boundary and isolation

The persisted store + the sender-ID policy (normalize, membership check, owner gate,
approve/deny parse) are a **distinct responsibility** from the adapter's poll loop —
Unix-philosophy one-thing-well. Recommend a small sub-unit **`internal/channel/telegram/authz`**
(or similarly named) that owns:

- the approved-sender store (load / persist / normalize / membership), and
- the auth-mode policy decision (`decide(senderID, text, mode) → {accept plaintext |
  route as envelope | pending | owner approve/deny | reject}`).

The adapter's `Next()` consults this unit at the point where it currently jumps straight to
`VerifyAndOpen`, and the store is a plain leaf (stdlib only). This keeps the crypto path
(`envelope` mode) and the plaintext path (sender-ID modes) as two clearly separated
branches rather than smearing sender-ID logic through the envelope pipeline.

**Isolation invariants preserved:** the change must not break `envelope` mode or existing
telegram/envelope tests; **F-007** (`internal/envelope` leaf isolation) and the
**supervisor isolation** invariant (**F-003**) stay green — the new `authz` unit imports
stdlib only (and, where it must construct/skip envelope handling, `internal/envelope` on
the same channel side of the `GoalSource`/`MessageSource` seam), never the supervisor. No
crypto or transport enters the supervisor graph.

## Options considered (why this shape and not the alternatives)

- **Status quo — envelope only, no easy mode.** Rejected as the *sole* answer: it is the
  right default (and is kept as the default), but it leaves no on-ramp for operators
  unwilling to run a key-holding CLI, which is a real adoption barrier and pushes people to
  fork the adapter or disable the channel. The ADR keeps this behavior *as the default*
  rather than removing it.
- **Adopt OpenClaw's `dmPolicy` verbatim.** Rejected: `dmPolicy`'s pairings do not persist
  across restarts, re-opening the approval flow on every bounce. Decision 4 is precisely
  the fix; adopting the model without the persistence fix would ship the known hole.
- **A separate low-security bot/binary instead of a mode on the same adapter.** Rejected:
  it duplicates the poll loop, armor wiring, size caps, and audit path, and splits the
  operator's mental model across two surfaces. A single mode selector on one adapter keeps
  one code path with a documented, opt-in branch, and keeps the strong default in the same
  place a reader already looks.
- **Reuse a sender-ID allowlist to *skip* armor for "trusted" senders.** Rejected outright
  (Decision 2): a trusted sender can still forward injected content. armor stays on every
  plaintext path unconditionally.

## Recommended task decomposition (for the task-planner)

Backlog, **sequenced after tasks 148–150**. Each task is one responsibility; the persisted
store + static allowlist is the natural first cut, pairing builds on it, `open` + docs
last. Dependency order is strict (b depends on a; c depends on b).

- **(a) Auth-mode config plumbing + `envelope`/`disabled`/`allowlist` modes + persisted
  store.**
  Scope: add `AGENT_BUILDER_TELEGRAM_AUTH_MODE` (fail-fast on unknown value; unset ⇒
  `envelope`) and `AGENT_BUILDER_TELEGRAM_APPROVED_STORE`; introduce the
  `internal/channel/telegram/authz` store (load/persist `0600` JSON, normalize, membership)
  and the mode-decision seam; wire the adapter to branch on mode — `envelope` unchanged,
  `disabled` rejects all, `allowlist` accepts plaintext only from statically seeded approved
  IDs (seeded into the store at startup) with armor + size caps + audit retained. No
  in-chat flow yet. Depends on: 150.
- **(b) `pairing` in-chat owner-approve flow on top of (a).**
  Scope: add `AGENT_BUILDER_TELEGRAM_OWNER_ID`; implement the owner-gated approve/deny
  grammar parsed **before** command routing; unknown-sender → `pairing_request` audit +
  pending reply + owner notification; `approve/deny <id>` from the owner only, mutating the
  persisted store. Test spec must assert (1) a stranger cannot self-approve, (2) approvals
  survive a restart, (3) approve/deny is evaluated before `deriveMessage`. Depends on: (a).
- **(c) `open` mode + startup warning + docs.**
  Scope: add the `open` value (plaintext from any sender), a mandatory startup `WARNING` to
  stderr documenting the risk ("any account that finds the bot can command it"), and the
  documentation of the whole mode matrix and its tradeoff. Depends on: (b).

## Spec / doc footprint (update WHEN implemented — not edited by this ADR)

- `docs/spec/configuration.md` — new env vars: `AGENT_BUILDER_TELEGRAM_AUTH_MODE`,
  `AGENT_BUILDER_TELEGRAM_OWNER_ID`, `AGENT_BUILDER_TELEGRAM_APPROVED_STORE`, alongside the
  existing `AGENT_BUILDER_TELEGRAM_*` family (§Configuration reference).
- `docs/spec/behaviors.md` — a new inbound-auth-modes behavior (the mode matrix, the
  plaintext lost/retained split, the owner-gated pairing flow, persistence across restart).
- `docs/spec/architecture.md` / `docs/architecture/diagrams.md` — only if the inbound flow
  diagram changes (the plaintext branch and the `authz` sub-unit are new boxes/edges).
- `docs/spec/fitness-functions.md` — consider a follow-on fitness check asserting the
  `internal/channel/telegram/authz` unit stays a stdlib(+`internal/envelope`) leaf and that
  `envelope` mode remains the default (proposed, not required by this ADR).

## Consequences

- **What becomes easier.** An operator can command the bot without running a key-holding
  CLI: `allowlist` for a known-ID deployment, `pairing` for owner-approved onboarding that
  *survives restarts*, `open` for a throwaway/demo (with a loud warning). The persistence
  fix removes OpenClaw's re-pair-on-restart papercut.
- **What becomes harder / the cost.** A new, permanently-available lower-security surface
  now exists in the codebase. Plaintext modes forfeit end-to-end confidentiality and
  per-message replay protection (Decision 2) — the operator carries that risk knowingly.
  There is a new persisted-state file to keep correct (`0600`, normalized, plain-text) and a
  new owner-gate ordering rule that a careless refactor of `deriveMessage` could break; the
  pairing test spec's "stranger cannot self-approve" assertion is the guard.
- **Default and invariants preserved.** `envelope` stays the shipped default and is
  byte-for-byte unchanged when the mode is unset; existing telegram/envelope tests stay
  green; F-003 (supervisor isolation) and F-007 (envelope leaf isolation) are untouched;
  armor stays load-bearing on every accepted plaintext.
- **Reversibility.** Reversible at bounded cost: the modes are additive and opt-in, so
  removing them is deleting the `authz` unit, the three env vars, and the adapter branch,
  and restoring the unconditional `VerifyAndOpen` call — no data migration, since the store
  holds only disposable public sender IDs. The one-way element is operator expectation
  (someone relying on `pairing` would have to re-adopt the CLI), which is a config change,
  not a code entanglement.
