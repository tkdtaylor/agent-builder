# Task 158: wire a real armor guard on the Telegram inbound path

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Replace the hardwired fail-open `allowAllContentGuard` in `assembleTelegramInbound`
with a real, env-configurable `armor.Guard` when an armor binary is available, and
fail assembly fast (rather than silently fail-open) when a PLAINTEXT auth mode
(`allowlist`/`pairing`/`open`) is resolved with no armor configured — closing the gap
between ADR 063 Decision 2 / `docs/spec/configuration.md`'s documented "armor RETAINED
on every accepted plaintext path" claim and actual behavior.

## Context

**Root cause (full-project review, verified 2026-07-02):**
`assembleTelegramInbound` (`internal/cli/orchestrate.go:1347`) unconditionally sets
`var guard telegram.ContentGuard = allowAllContentGuard{}` — a no-op that always
returns `ingestion.DecisionAllow`, defined at lines 1394-1407. There is NO env var
anywhere that wires a real `armor.Guard` into this path, for ANY auth mode. This
directly contradicts `docs/spec/configuration.md`'s existing claim: "`armor.Guard`,
the SEC-001/002 size caps, and audit events are retained on every accepted plaintext
path (ADR 063 Decision 2)." The size caps and audit events genuinely are retained;
armor is not.

`internal/armor/guard.go` already provides a complete, tested adapter:
`armor.NewGuard(armor.Config{Command: [...]})` returns an `armor.Guard` whose
`DecideContent(ctx, candidate) (ingestion.Decision, error)` method satisfies
`telegram.ContentGuard` directly — no shim needed. It is currently wired ONLY in test
code (`executorharness.NewArmorGuarded`'s test callers); never on any production
env-driven path.

**The fix:**
1. A new env var `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` resolves an executable path
   (mirroring `resolveAuditBin`'s `exec.LookPath` pattern,
   `internal/runtime/run.go:1015-1032`); when resolvable, `assembleTelegramInbound`
   wires `armor.NewGuard(armor.Config{Command: []string{resolvedPath}})` in place of
   `allowAllContentGuard`, for every auth mode.
2. **Fail-closed decision (this task's design call, stated here so the test spec can
   assert against it):** when NO armor binary is configured —
   - `envelope`/`disabled` modes: retain the existing fail-open
     `allowAllContentGuard` default (unchanged). Rationale, already documented at
     lines 1338-1346: the load-bearing gate on this path is the envelope-verify
     pipeline (Ed25519 + X25519 + replay-cache), always enforced; armor is an
     additional filter over already-authenticated plaintext.
   - `allowlist`/`pairing`/`open` modes: fail-fast `errUsageConfig` assembly error —
     the SAME treatment `EnvTelegramBotToken` etc. already receive in this exact
     function. These modes have NO cryptographic authentication gate at all
     (ADR 063 Decision 2's own framing), so for them armor is not an "additional"
     filter — it is the only content-level defense alongside the sender-ID gate, and
     silently fail-opening it there is the specific gap this task closes.
3. `docs/spec/configuration.md`'s existing "retained on every accepted plaintext path"
   claim is rewritten in place (per `AGENTS.md`'s spec-freshness rule) so it is
   actually true after this task, documenting the new env var and the fail-closed
   behavior for plaintext modes.

**Reference:**
- `internal/cli/orchestrate.go:1242-1407` (`assembleTelegramInbound`, `allowAllContentGuard`)
- `internal/armor/guard.go` (`NewGuard`, `Config`, `Guard.DecideContent` — reused as-is)
- `internal/runtime/run.go:1015-1032` (`resolveAuditBin` — the single-executable-path
  resolution pattern this task mirrors)
- `docs/spec/configuration.md`'s `AGENT_BUILDER_TELEGRAM_AUTH_MODE` row (the existing,
  currently-inaccurate "armor RETAINED" claim)
- ADR 063 Decision 2

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-158-01 | `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` resolves an executable path via `resolveAuditBin`'s `exec.LookPath` pattern; an unresolvable configured value is a fail-fast `errUsageConfig` error. | must have |
| REQ-158-02 | A resolved armor binary is wired as a real `armor.NewGuard(...)` `ContentGuard` for EVERY auth mode (envelope, allowlist, pairing, open). | must have |
| REQ-158-03 | No armor configured + `envelope`/`disabled` mode: assembly succeeds with the existing fail-open `allowAllContentGuard` (unchanged pre-task behavior). | must have |
| REQ-158-04 | No armor configured + a plaintext mode (`allowlist`/`pairing`/`open`): assembly fails fast with `errUsageConfig` — never a silent fail-open guard. | must have |
| REQ-158-05 | A configured armor guard that blocks a candidate produces the same `armor_blocked` audit reason and drop-message behavior `processPlaintext` already implements, proven end-to-end with a fake-runner-backed real `armor.Guard`. | must have |
| REQ-158-06 | `docs/spec/configuration.md`'s armor-retention claim is rewritten in place to accurately describe the post-task behavior. | must have |
| REQ-158-07 | Pre-existing `internal/cli`/`internal/channel/telegram` suites continue to pass unchanged for envelope/disabled-mode assembly (no armor configured). | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/158-telegram-armor-guard-wiring-test-spec.md` exists (written first)
- [x] Task 157 merged (this task edits `assembleTelegramInbound`/`adapter.go` AFTER
  157's shutdown-context change, to avoid merge conflicts, per the review's ordering note)
- [x] `internal/armor` (task 025) already provides the reused `Guard`/`Config`/`NewGuard`
- [ ] `make check` green before branching

## Acceptance criteria

- [ ] [REQ-158-01] TC-158-01: armor binary resolution mirrors `resolveAuditBin`'s unset/resolvable/unresolvable cases.
- [ ] [REQ-158-02] TC-158-02: a configured armor binary is wired for all four auth modes.
- [ ] [REQ-158-03] TC-158-03: unconfigured armor + envelope/disabled mode still assembles with fail-open behavior unchanged.
- [ ] [REQ-158-04] TC-158-04: unconfigured armor + any plaintext mode fails assembly fast with `errUsageConfig`.
- [ ] [REQ-158-05] TC-158-05: a real (fake-runner) armor guard blocking a plaintext-mode message is proven end-to-end via `Adapter.Next` + the `armor_blocked` audit reason.
- [ ] [REQ-158-06] TC-158-06: `docs/spec/configuration.md` accurately reflects the fix (reviewed, no future-tense language).
- [ ] [REQ-158-07] TC-158-07: `go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/armor/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L2/L5 — a real `Adapter.Next` driven with a real
  `armor.Guard` backed by a fake in-process `armor.Runner` proves the wiring is
  genuinely live end-to-end. A live external armor binary (L6) adds no new confidence
  beyond task 025's existing `armor.ProcessRunner` subprocess coverage.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC158
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... ./internal/channel/telegram/... -run TestTC158
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md` — `AGENT_BUILDER_TELEGRAM_AUTH_MODE` row's armor
  sentence rewritten in place to be accurate; a new row added for
  `AGENT_BUILDER_TELEGRAM_ARMOR_BIN`.
- `docs/architecture/decisions/` — consider a short ADR note appending to ADR 063
  (or a new ADR) recording the fail-closed-on-plaintext-modes decision if the
  main session judges it significant enough (this task's design call is documented
  in this file's Context section either way).

## Out of scope

- Wiring armor on the coding-agent (Claude ingestion) path — a separate, pre-existing
  unwired seam (`executorharness.NewArmorGuarded`), not part of this review's Fix 3
  evidence.
- Task 157 (idle/reject termination) and task 159 (pairing-owner seeding) — sequenced
  around this task to avoid `orchestrate.go`/`adapter.go` merge conflicts.
- Multi-argument armor commands — `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` resolves one
  executable path, no extra argv, mirroring `resolveAuditBin`'s scope.

## Dependencies

- **Blocks on:** task 157 (must land first — same files).
- **Blocks:** task 159 (lands after this task — same files).
