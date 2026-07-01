# Task 153: Telegram `open` mode + mandatory startup WARNING + docs

**Project:** agent-builder
**Created:** 2026-07-01
**Status:** complete (🟡 code merged; awaiting spec-verifier)

## Goal

Add the final `open` auth mode (plaintext from any sender, no gate at all) on top of tasks
151/152, gated behind requiring the exact literal env value `open` and a mandatory stderr
`WARNING` emitted at assembly naming the risk. Close out the documentation footprint for
the whole ADR 063 mode matrix (configuration.md / behaviors.md / diagrams.md, as applicable)
that tasks 151/152 did not already cover in their own feat commits.

## Context

Read `docs/architecture/decisions/063-telegram-sender-id-channel-auth-modes.md` in full
before starting.

`open` is the intentional footgun mode: it accepts plaintext commands from **any** sender
ID, with no allowlist/pairing/owner gate whatsoever. It is reachable only by the exact,
explicit env value `open` (case-sensitive, no whitespace tolerance) — never the default,
never reachable by a typo that happens to also be invalid (a typo must fail closed to a
config error, not silently fail open). Because this mode has no sender-ID gate, it never
reads or writes the `authz` store from tasks 151/152.

Per ADR 063 Decision 2, the retained controls (armor, SEC-001/002 size caps, audit events)
still apply unconditionally on the plaintext-accepted path — `open` does not weaken those,
only the sender-ID gate.

Reference: `internal/channel/telegram/adapter.go`, task 151's mode-decision seam and enum
validation, task 152's owner-gate pattern (for contrast — `open` deliberately has none).

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-153-01 | `open` mode accepts plaintext from any sender ID (no store lookup, no owner gate); armor, size caps, and audit events are retained exactly as in `allowlist`/`pairing`. | must have |
| REQ-153-02 | `open` is reachable only by the exact literal, case-sensitive, whitespace-exact string `"open"` — near misses (wrong case, leading/trailing whitespace) are fail-fast unknown-mode config errors, never a silent fallback to `envelope` and never an accidental match to `open`. All other modes (`envelope`/`disabled`/`allowlist`/`pairing`) are unaffected by this task's changes (regression-confirmed). | must have |
| REQ-153-03 | A mandatory `WARNING`-prefixed line is written to stderr at assembly time whenever (and only whenever) the resolved mode is `open`, naming the concrete risk ("any account that finds the bot can command it" or equivalent), emitted unconditionally (not gated behind a verbosity flag), exactly once per assembly. | must have |
| REQ-153-04 | `docs/spec/configuration.md` documents the `open` value in the `AGENT_BUILDER_TELEGRAM_AUTH_MODE` row; `docs/spec/behaviors.md` carries a complete, present-tense description of the full mode matrix (all five modes), the plaintext lost/retained tradeoff, the owner-gated pairing flow, and the persistence-across-restart property, closing out any narrative not already written by tasks 151/152's own feat commits. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/153-telegram-open-mode-and-docs-test-spec.md` exists (written first — 2026-07-01)
- [x] ADR 063 read in full
- [x] Task 152 merged (`pairing` mode, owner gate, persisted approvals)
- [x] `make check` green on `main` before branching

## Acceptance criteria

- [x] [REQ-153-01] TC-153-01: `open` mode accepts plaintext from a never-before-seen sender ID; `ContentGuard.DecideContent` is invoked; `MsgStatus`/etc. derived correctly.
- [x] [REQ-153-01] TC-153-02: `open` mode still rejects oversized plaintext before armor, with an audit event; `ContentGuard` never invoked for that update.
- [x] [REQ-153-02] TC-153-03: unset/`""` still resolves to `envelope`; `"Open"`/`"OPEN"`/padded-whitespace variants all fail assembly as unknown-mode errors (none resolve to `open`).
- [x] [REQ-153-02] TC-153-04: the full existing test suites for tasks 151/152 (envelope default, disabled-rejects-all, allowlist accept/reject, pairing stranger-cannot-self-approve, pairing survives-restart) still pass unmodified after this task's diff lands.
- [x] [REQ-153-03] TC-153-05: assembling with `AUTH_MODE=open` emits exactly one `WARNING`-prefixed stderr line containing the named risk phrase; assembling with any of the other four modes emits zero such lines.
- [x] [REQ-153-04] TC-153-06: `configuration.md` and `behaviors.md` (and `diagrams.md`, only if the diagram's shape changed) reviewed and confirmed to carry the complete mode-matrix narrative in present tense, with no future-tense "planned" language.

## Verification plan

- **Highest level achievable now: L2/L3.** No live Telegram bot token is available; the
  warning-emission and mode-gate assertions are fully exercisable as unit tests against the
  assembly/config-validation function and a captured stderr/logger sink. `make check`
  confirms the full suite (including tasks 151/152's tests) stays green.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/channel/telegram/... ./internal/cli/...
  make check
  ```
  Expected: all TC-153-01..06 pass (TC-153-04's regression re-run is the prior tasks' test
  functions, unmodified); `make check` → `All checks passed.`
- **Runtime observation — ACHIEVED at assembly level (L6), residual at the live-bot level.**
  The `WARNING` stderr line is an assembly-time side effect that does not require a live
  Telegram bot token to observe: built the real `cmd/agent-builder` binary and ran
  `orchestrate` against a full valid env (`AGENT_BUILDER_TELEGRAM_AUTH_MODE=open` + all
  required crypto/task-root/publish vars, a fake bot token, and an unroutable base URL so
  the eventual `getUpdates` call fails harmlessly after assembly). Observed the exact live
  stderr line printed exactly once before the (expected) connection-refused error:
  `WARNING: AGENT_BUILDER_TELEGRAM_AUTH_MODE=open — any account that finds the bot can
  command it (no sender-ID gate, no allowlist, no pairing approval). Plaintext only;
  armor/size-caps/audit are still enforced, but there is no gate on WHO can send commands.`
  Re-ran the same binary with the mode unset (`envelope` default) and confirmed 0
  `WARNING` lines. **Residual, not claimed here:** confirming a message from an arbitrary
  never-configured Telegram account is accepted and answered end-to-end still requires a
  live bot token — that live-bot observation remains a documented follow-on.

## Out of scope

- Any additional footgun-mitigation UX beyond the mandatory warning (e.g. a confirmation
  prompt, a time-boxed auto-expiry of `open` mode) — not specified by the ADR, a possible
  future hardening, not required here.
- A dedicated `make fitness` check enforcing "envelope is the default" or the `authz`
  stdlib-leaf isolation — proposed by the ADR as a follow-on (`docs/spec/fitness-functions.md`
  note), not required by this task.

## Dependencies

- **Blocks on:** task 152 (`pairing` mode, owner gate, persisted approvals).
- **Blocks:** none — this closes out the ADR 063 task chain.
