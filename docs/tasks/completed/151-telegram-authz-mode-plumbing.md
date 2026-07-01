# Task 151: Telegram auth-mode config plumbing + `envelope`/`disabled`/`allowlist` modes + persisted approved-sender store

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** backlog

## Goal

Add an opt-in sender-ID auth-mode selector to the Telegram adapter, alongside the existing
crypto-envelope default, per ADR 063. This task lands the config plumbing
(`AGENT_BUILDER_TELEGRAM_AUTH_MODE`, `AGENT_BUILDER_TELEGRAM_APPROVED_STORE`), a new
`internal/channel/telegram/authz` sub-unit owning a persisted `0600` JSON store of
approved sender IDs (load/persist/normalize/membership), and three of the five modes:
`envelope` (unchanged default), `disabled` (reject all), and `allowlist` (accept plaintext
only from statically-seeded approved IDs). `pairing` and `open` are follow-on tasks
(152, 153).

## Context

Read `docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md` in full
before starting — it is authoritative for every design decision in this task. Do not
re-decide anything it settles.

Today `internal/channel/telegram/adapter.go`'s `Next()` unconditionally runs every inbound
update through `envelope.VerifyAndOpen` (`adapter.go:178`) — the sender's Telegram identity
is never consulted. This task adds a mode-check branch ahead of that call: `envelope` mode
preserves the existing call untouched; `disabled` rejects everything before any parsing;
`allowlist` accepts **plaintext** (skipping `VerifyAndOpen` entirely) only from sender IDs
present in the new `authz` store, then runs the SAME armor + size-cap + audit pipeline the
envelope path already uses before deriving the message.

**Isolation invariant (ADR 063 Decision 5):** `internal/channel/telegram/authz` imports
stdlib only (plus, if needed to distinguish envelope-shaped payloads,
`internal/envelope` on the same channel side of the seam) — never `internal/supervisor`.
F-003 (supervisor isolation) and F-007 (envelope leaf isolation) must stay green.

**Retained controls (ADR 063 Decision 2 — non-negotiable):** on every plaintext-accepted
path, `armor.Guard`/`ContentGuard.DecideContent` still runs, the SEC-001/002 size caps
still apply, and every accept/reject decision still emits an audit event. Sender-ID
acceptance is never a reason to skip armor.

Reference: `internal/channel/telegram/adapter.go`, `internal/channel/telegram/adapter_test.go`,
`internal/secrets/disk_oauth_source.go` (the graceful-absence + explicit-path-env pattern to
mirror for the store), `internal/audit/audit.go` (the closed `AuditAction` enum — add new
actions here if the mode-decision rejections need a distinct action beyond the existing
`ActionChannelReject`, keeping the enum closed and explicit rather than inventing
untyped reason strings for the mode-gate specifically).

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-151-01 | `AGENT_BUILDER_TELEGRAM_AUTH_MODE` unset (or `"envelope"`) reproduces today's adapter behavior byte-for-byte: `VerifyAndOpen` runs unchanged, sender ID is never consulted, plaintext (non-envelope-shaped) input is still rejected exactly as before. | must have |
| REQ-151-02 | `allowlist` mode accepts plaintext commands only from sender IDs present in the `authz` store (seeded at startup from a static config list); armor, SEC-001/002 size caps, and audit events are retained on this path exactly as on the envelope path. | must have |
| REQ-151-03 | `disabled` mode rejects all inbound traffic (envelope-shaped or plaintext) before any parsing/armor/authz work; an unrecognized `AGENT_BUILDER_TELEGRAM_AUTH_MODE` value is a fail-fast `ExitUsage`-class configuration error at assembly time, never a nil-adapter panic at first `Next()`. | must have |
| REQ-151-04 | A new `internal/channel/telegram/authz` package provides a `0600`-permissioned, plain-text JSON store of normalized numeric sender IDs with `Load`/`Persist`/`Contains`/`Add`/`Remove`; the store round-trips across independent load/reload cycles (the persistence proof); a missing file is graceful absence, a malformed file is a fail-fast error on `Load`. `AGENT_BUILDER_TELEGRAM_APPROVED_STORE` is required (fail-fast if blank) whenever the selected mode consults sender-ID approval (`allowlist`; `pairing` in task 152). | must have |
| REQ-151-05 | `allowlist` mode seeds a statically configured approved-ID list into the persisted store at startup (union/additive — never destructively overwrites IDs already present in an existing store file, since task 152's `pairing` mode grows the same store in-chat). | must have |
| REQ-151-06 | Sender IDs are normalized to a canonical numeric form on every write and every membership check, so a formatting difference (leading zeros, whitespace, string-vs-int) can never bypass the gate or create a duplicate approval entry; a non-numeric ID is rejected with an error, never silently coerced. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/151-telegram-authz-mode-plumbing-test-spec.md` exists (written first — 2026-07-01)
- [ ] ADR 063 read in full
- [ ] Task 150 merged (per the ADR's explicit sequencing — this feature lands after the
      local operator CLI exists)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-151-01] TC-151-01: unset/`"envelope"` mode produces identical `supervisor.Message` output to the pre-task adapter for a real signed envelope; no `authz` call occurs.
- [ ] [REQ-151-01] TC-151-02: `envelope` mode still rejects plaintext input exactly as today (parse-failure audit reason, no message derived).
- [ ] [REQ-151-02] TC-151-03: `allowlist` mode accepts plaintext from an approved sender, invokes `ContentGuard.DecideContent`, and derives the correct message.
- [ ] [REQ-151-02] TC-151-04: `allowlist` mode rejects plaintext from an unapproved sender, emits an audit rejection, and never invokes `ContentGuard`.
- [ ] [REQ-151-02] TC-151-05: `allowlist` mode still enforces the size caps (rejected before armor, audited).
- [ ] [REQ-151-03] TC-151-06: `disabled` mode rejects both an envelope-shaped and a plaintext update, with distinct audit events, and never invokes `VerifyAndOpen` or `ContentGuard`.
- [ ] [REQ-151-03] TC-151-07: an unrecognized `AUTH_MODE` value fails assembly with an error (not a panic); all five recognized enum values pass the config-layer validation.
- [ ] [REQ-151-04] TC-151-08: a store written by one `authz.Store` instance and reloaded by an independent second instance from the same path has an identical approved set; file is `0600`; missing file → graceful empty store; malformed file → `Load` error.
- [ ] [REQ-151-04] TC-151-09: `allowlist`/`pairing`-mode assembly with a blank `APPROVED_STORE` path fails fast; `envelope`/`disabled` with a blank path succeed.
- [ ] [REQ-151-05] TC-151-10: `allowlist` seeding writes the static list to a fresh store file; a second assembly against the same path with a different (or empty) static list does not remove previously-seeded IDs.
- [ ] [REQ-151-06] TC-151-11: adding `42`, `"42"`, `"042"`, `" 42 "` all normalize to one stored entry; cross-format `Contains` checks succeed both directions; non-numeric IDs are rejected.
- [ ] [REQ-151-06] TC-151-12: `docs/architecture/diagrams.md` updated in the same commit if-and-only-if the inbound flow diagram's shape changed.

## Verification plan

- **Highest level achievable now: L2/L3.** No live Telegram bot token is available in this
  environment; the adapter's `getUpdates` HTTP call is already abstracted behind
  `httpClient`/stub servers in existing tests (`adapter_test.go`), so the mode-branch logic,
  the `authz` store, and the fail-fast config validation are all fully exercisable with unit
  tests. `make fitness` covers F-003/F-007 isolation.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
  make check
  ```
  Expected: all TC-151-01..12 pass; `make check` → `All checks passed.`
- **Runtime observation (documented follow-on, no live bot token available):** once a real
  bot token exists, drive `orchestrate` with `AGENT_BUILDER_TELEGRAM_AUTH_MODE=allowlist`
  against a real Telegram chat and confirm a plaintext command from an approved chat ID is
  accepted and answered. Record this as L5/L6 residual in the verify commit, not claimed
  here.

## Out of scope

- `pairing` mode (in-chat owner-approve flow) — task 152.
- `open` mode + startup warning — task 153.
- A `make fitness` target enforcing the `authz` stdlib-leaf isolation invariant — proposed
  by the ADR as a follow-on, not required by this task (the unit tests assert the import
  boundary directly instead).
- Multi-operator / per-chat approval scoping — v1 is a single flat approved-ID set, matching
  the existing single-chat `AGENT_BUILDER_TELEGRAM_CHAT_ID` design.

## Dependencies

- **Blocks on:** task 150 (`agent-cli reply-open`) — no code dependency; sequenced after
  per ADR 063's explicit ordering (the local operator CLI on-ramp lands before its
  lower-security alternative).
- **Blocks:** task 152 (`pairing` mode, builds on this task's `authz` store and
  mode-decision seam).
