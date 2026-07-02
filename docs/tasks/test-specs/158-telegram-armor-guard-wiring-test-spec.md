# Test Spec 158: wire a real armor guard on the Telegram inbound path

**Linked task:** [`docs/tasks/backlog/158-telegram-armor-guard-wiring.md`](../backlog/158-telegram-armor-guard-wiring.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

`assembleTelegramInbound` (`internal/cli/orchestrate.go:1242-1392`) unconditionally
sets:

```go
var guard telegram.ContentGuard = allowAllContentGuard{}   // line 1347
```

with `allowAllContentGuard` (lines 1394-1407) always returning
`ingestion.DecisionAllow`. There is no env knob anywhere to wire a real
`armor.Guard`. This contradicts ADR 063 Decision 2 and
`docs/spec/configuration.md`'s own documented claim: "**Plaintext modes
(`allowlist`/`pairing`/`open`) forfeit the envelope's end-to-end confidentiality and
per-message replay protection** — `armor.Guard`, the SEC-001/002 size caps, and audit
events are retained on every accepted plaintext path (ADR 063 Decision 2)." The size
caps and audit events genuinely ARE retained (`processPlaintext`,
`internal/channel/telegram/adapter.go:352-397`); `armor.Guard` is not — it is
hardwired to a no-op allow-all regardless of mode.

`internal/armor/guard.go` already provides a real, working adapter:
`armor.NewGuard(armor.Config{Command: [...]})` returns an `armor.Guard` whose
`DecideContent(ctx, candidate) (ingestion.Decision, error)` satisfies
`telegram.ContentGuard` directly (no adapter shim needed). It is currently wired ONLY
in test code (`tests/executorharness/armor_wiring_test.go`,
`tests/executor/claude_cli_test.go`) via `executorharness.NewArmorGuarded` — never in
any production env-driven path.

**The fix:**
1. Add an env var (`AGENT_BUILDER_TELEGRAM_ARMOR_BIN`, mirroring the existing
   `resolveAuditBin`/`EnvAuditBin` single-executable-path pattern in
   `internal/runtime/run.go:1015-1032`) that, when set, resolves to an executable and
   wires `armor.NewGuard(armor.Config{Command: []string{resolvedPath}})` as the
   adapter's `ContentGuard`.
2. Decide and implement fail-closed behavior when no armor binary is configured:
   - **`envelope` mode (and `disabled`, which never reaches this code path):** retain
     the existing fail-open `allowAllContentGuard` default. Rationale (already
     documented at lines 1338-1346 and NOT overturned by this task): the load-bearing
     trust gate on this path is the envelope-verify pipeline
     (Ed25519 verify + X25519 decrypt + replay-cache), always enforced; armor is an
     ADDITIONAL injection filter over already-authenticated operator plaintext.
   - **Plaintext modes (`allowlist`/`pairing`/`open`):** these modes have NO
     cryptographic authentication gate at all (ADR 063 Decision 2's own framing —
     "plaintext modes forfeit the envelope's end-to-end confidentiality"). For these
     modes, armor is not an ADDITIONAL filter — it is the ONLY content-level defense
     alongside the sender-ID gate. Assembling `orchestrate` with a plaintext mode
     resolved and NO armor binary configured is a fail-fast `errUsageConfig` assembly
     error — the SAME treatment `EnvTelegramBotToken`/`EnvTelegramChatID`/etc. already
     receive in this exact function, not a silent fail-open degrade.
3. Update `docs/spec/configuration.md`'s existing "armor RETAINED on every accepted
   plaintext path" claim so it is actually TRUE after this task (it is currently
   describing intended, not actual, behavior).

**Module boundaries touched:** `internal/cli` (`assembleTelegramInbound` gains armor
resolution + the fail-closed assembly check) and `internal/armor` (reused as-is, no
change — `armor.NewGuard`/`armor.Config` already support this construction).

---

## Requirements coverage

| Req ID     | Description                                                                                                                | Test cases            |
|------------|--------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-158-01 | A new env var (`AGENT_BUILDER_TELEGRAM_ARMOR_BIN`) resolves an executable path via the same `exec.LookPath` pattern `resolveAuditBin` uses; an unresolvable configured value is a fail-fast `errUsageConfig` error at assembly | TC-158-01               |
| REQ-158-02 | When the armor binary resolves, `assembleTelegramInbound` wires a real `armor.NewGuard(armor.Config{Command: [resolvedPath]})` as the adapter's `ContentGuard`, for EVERY auth mode (envelope, allowlist, pairing, open) | TC-158-02               |
| REQ-158-03 | When no armor binary is configured and the resolved auth mode is `envelope` (or `disabled`), assembly succeeds with the existing fail-open `allowAllContentGuard` — unchanged pre-task behavior | TC-158-03               |
| REQ-158-04 | When no armor binary is configured and the resolved auth mode is a plaintext mode (`allowlist`, `pairing`, or `open`), assembly fails fast with `errUsageConfig` (ExitUsage) — never a silent fail-open guard | TC-158-04               |
| REQ-158-05 | An armor-configured `Guard` that BLOCKS a candidate on a plaintext-mode message produces the SAME `armor_blocked` audit reason and dropped-message behavior `processPlaintext` already implements for the envelope path — proven end-to-end through `Adapter.Next` with a real (fake-runner) `armor.Guard` | TC-158-05               |
| REQ-158-06 | `docs/spec/configuration.md`'s "armor RETAINED on every accepted plaintext path" claim is updated to describe the ACTUAL post-task behavior (env var name, fail-closed-on-plaintext-modes decision) | TC-158-06 (doc review)  |
| REQ-158-07 | Pre-existing `internal/cli` and `internal/channel/telegram` suites continue to pass unchanged in behavior for `envelope`/`disabled` mode assembly (no armor configured) | TC-158-07               |

---

## Pre-implementation checklist

- [x] Task 157 merged (`Adapter`/`assembleTelegramInbound` shutdown-context wiring
  landed first — this task's edits to the same function come after, avoiding
  merge conflicts)
- [x] `internal/armor/guard.go` (`armor.NewGuard`, `armor.Config`, `armor.Guard.DecideContent`)
  already exists and is unit-tested (task 025)
- [ ] `make check` green before branching

---

## Test cases

### TC-158-01 — Armor binary resolution mirrors `resolveAuditBin`'s pattern

- **Requirement:** REQ-158-01
- **Level:** L2 (unit test)
- **Test file:** `internal/cli/orchestrate_158_test.go` (new)

**Setup:** (a) `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` unset. (b) set to a resolvable
executable (e.g. `/bin/true` or a test fixture script). (c) set to a nonexistent path.

**Step:** Call the new resolution helper (or `assembleTelegramInbound` directly) for
each case, with `AUTH_MODE=envelope` (so the fail-closed plaintext-mode gate from
REQ-158-04 does not also fire).

**Expected output:** (a) resolves to no configured armor (nil/empty), no error. (b)
resolves successfully, no error. (c) fails fast with `errUsageConfig`, mirroring
`resolveAuditBin`'s unresolvable-binary error shape.

---

### TC-158-02 — A configured armor binary is wired as the adapter's `ContentGuard` in every mode

- **Requirement:** REQ-158-02
- **Level:** L2 (unit test, one sub-test per auth mode)
- **Test file:** `internal/cli/orchestrate_158_test.go`

**Setup:** Build the full `tc153FullTelegramEnv`-style env with
`AGENT_BUILDER_TELEGRAM_ARMOR_BIN` set to a resolvable fixture command, for each of
`envelope`, `allowlist`, `pairing`, `open` (with each mode's other required vars —
`APPROVED_STORE`/`OWNER_ID` — also set as needed).

**Step:** Call `assembleTelegramInbound`, then feed the resulting `Adapter` a scripted
update whose content the fixture armor command is configured to BLOCK.

**Expected output:** For all four modes, the message is dropped and an
`armor_blocked` (or equivalent) audit reason is recorded — proving the REAL armor
guard (not `allowAllContentGuard`) is wired regardless of mode.

---

### TC-158-03 — No armor configured + `envelope`/`disabled` mode: fail-open unchanged

- **Requirement:** REQ-158-03
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/cli/orchestrate_158_test.go`

**Step:** Call `assembleTelegramInbound` with `AUTH_MODE=envelope` (and separately
`disabled`) and `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` unset.

**Expected output:** Assembly succeeds with no error (pre-task behavior preserved);
the resulting adapter's `ContentGuard` allows content through exactly as
`allowAllContentGuard` did before this task (verified by feeding it a candidate that a
real armor guard WOULD block, and confirming it is still allowed here).

---

### TC-158-04 — No armor configured + a plaintext mode: fail-fast assembly error

- **Requirement:** REQ-158-04
- **Level:** L2 (unit test, one sub-test per plaintext mode)
- **Test file:** `internal/cli/orchestrate_158_test.go`

**Setup:** For each of `allowlist`, `pairing`, `open`, build a full valid env (all
OTHER required vars set correctly) with `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` unset.

**Step:** Call `assembleTelegramInbound`.

**Expected output:** All three return a non-nil `errUsageConfig`-classified error
(`isUsageConfig(err)` true) — assembly fails BEFORE any adapter is constructed, never
a silently fail-open guard. The error message names the missing armor configuration
requirement (assertable via a substring check).

---

### TC-158-05 — A real (fake-runner) armor guard blocks a plaintext-mode message end-to-end

- **Requirement:** REQ-158-05
- **Level:** L2/L5 (real `Adapter.Next` driven with a fake `armor.Runner` behind `armor.NewGuard`)
- **Test file:** `internal/cli/orchestrate_158_test.go` or `internal/channel/telegram/adapter_158_test.go`

**Setup:** Construct an `armor.Guard` via `armor.NewGuard(armor.Config{Runner:
fakeBlockingRunner{}})` (an in-process fake runner, avoiding a real subprocess
dependency in the test) directly injected as `telegram.Config.ContentGuard` (bypassing
env-var resolution for this specific assertion, to isolate the guard's wiring effect
from the env-var resolution tested in TC-158-01/02). Auth mode = `open` (or
`allowlist`, an approved sender).

**Step:** Feed the adapter one scripted plaintext update; call `Next()`.

**Expected output:** `Next()` returns `(supervisor.Message{}, false, nil)` for that
poll (message dropped, adapter re-polls per task 157's fix rather than terminating),
and the audit sink records `armor_blocked` — the SAME behavior `processPlaintext`
already implements for the envelope path (task 097), now proven reachable when armor
is genuinely wired on a plaintext-mode path.

---

### TC-158-06 — `docs/spec/configuration.md` accurately describes the fix

- **Requirement:** REQ-158-06
- **Level:** L1 (documentation review, not a Go test)

**Step:** Read `docs/spec/configuration.md`'s `AGENT_BUILDER_TELEGRAM_AUTH_MODE` row
(and any new row for `AGENT_BUILDER_TELEGRAM_ARMOR_BIN`).

**Expected output:** The existing "armor.Guard... retained on every accepted
plaintext path (ADR 063 Decision 2)" sentence is either (a) left as-is because it is
now TRUE, with a new row documenting `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` and the
fail-closed-on-plaintext-modes-without-armor behavior, or (b) rewritten in place (per
`AGENTS.md`'s "stale spec entries are rewritten in place" rule) to precisely describe
the new fail-closed contract. No future-tense "planned" language.

---

### TC-158-07 — Full regression: pre-existing envelope-mode assembly unaffected

- **Requirement:** REQ-158-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/armor/...
make check
```

**Expected output:** All packages `ok`; every pre-existing task-080/097/098/151/152/153/157
test continues to pass with unchanged assertions (their env maps do not set
`AGENT_BUILDER_TELEGRAM_ARMOR_BIN`, so envelope-mode assembly is unaffected by
REQ-158-03; any pre-existing test that assembles a PLAINTEXT mode without an armor
binary configured is found and updated to either configure one or assert the new
fail-fast error, per REQ-158-04). `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2/L5 — a real `Adapter.Next` driven with a real
  `armor.Guard` (backed by a fake in-process `armor.Runner`, avoiding a subprocess
  dependency) proves the wiring is genuinely live end-to-end, not merely
  type-compatible. A live external armor binary (L6) adds confidence about the
  `ProcessRunner`/subprocess invocation path, already covered by task 025's existing
  `armor.ProcessRunner` tests — not new surface this task introduces.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC158
  ```
  Expected: TC-158-01..04 pass.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... ./internal/channel/telegram/... -run TestTC158
  ```
  Expected: TC-158-05's end-to-end block proven via the fake-runner armor guard.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Wiring armor on the coding-agent (Claude ingestion) path — that path
  (`executorharness.NewArmorGuarded`) is a SEPARATE, already-existing seam, currently
  also unwired to any production env var; wiring it is a distinct task if pursued
  (not part of this review's Fix 3 evidence, which is scoped to the Telegram inbound
  path only).
- Any change to the pairing-mode owner-seeding gap (task 159) or the
  idle/reject-terminates-the-loop gap (task 157, already fixed, merged first).
- Multi-argument armor commands (`AGENT_BUILDER_TELEGRAM_ARMOR_BIN` resolves ONE
  executable path with no extra argv, mirroring `resolveAuditBin`'s scope).
