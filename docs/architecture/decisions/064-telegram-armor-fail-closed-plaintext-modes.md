# ADR 064 — Telegram armor wiring: fail-open on `envelope`/`disabled`, fail-closed on plaintext modes

**Status:** accepted
**Date:** 2026-07-02

**Motivated by:** task 158's root-cause review found that `assembleTelegramInbound`
(`internal/cli/orchestrate.go`) unconditionally wired `allowAllContentGuard` — a no-op
that always allows — as the Telegram adapter's `ContentGuard`, for every auth mode, with
no env var anywhere to wire a real `armor.Guard`. This directly contradicted the existing
`docs/spec/configuration.md` claim (and ADR 063 Decision 2) that "`armor.Guard`... [is]
retained on every accepted plaintext path." The size caps and audit events genuinely were
retained; armor was not — it was a documented-but-unimplemented control.

## Context

`internal/armor/guard.go` already provides a complete, tested adapter —
`armor.NewGuard(armor.Config{Command: [...]})` returns an `armor.Guard` whose
`DecideContent` satisfies `telegram.ContentGuard` directly. It was wired only in test
code (`executorharness.NewArmorGuarded`'s callers); never on any production,
env-driven Telegram inbound path.

ADR 063 (the Telegram sender-ID auth modes) established a mode matrix: `envelope`
(default, cryptographically authenticated via Ed25519+X25519+replay-cache) and three
plaintext modes — `allowlist`, `pairing`, `open` — which accept unauthenticated
plaintext gated only by a sender-ID check (or, for `open`, no gate at all). ADR 063
Decision 2 states armor is retained on every accepted plaintext path as a non-negotiable
control, but did not specify what happens when no armor binary is actually configured —
that gap is what this ADR closes.

## Decision

Add `AGENT_BUILDER_TELEGRAM_ARMOR_BIN`, resolved via `exec.LookPath` mirroring
`resolveAuditBin`'s pattern (`internal/runtime/run.go`). When it resolves to an
executable, `armor.NewGuard(armor.Config{Command: [resolvedPath]})` is wired as the
adapter's `ContentGuard` for **every** auth mode — envelope, allowlist, pairing, and
open alike. A configured-but-unresolvable value is a fail-fast `errUsageConfig` error at
assembly, never a silent fallback.

When **no** armor binary is configured, the assembly-time behavior is **mode-dependent**,
not a single global default:

- **`envelope` / `disabled` modes: retain the fail-open `allowAllContentGuard`
  default (unchanged pre-task behavior).** The load-bearing trust gate on this path is
  the envelope-verify pipeline (Ed25519 signature verify + X25519 decrypt + replay-cache),
  which is always enforced regardless of armor configuration. Armor is an *additional*
  injection filter layered over plaintext that has already passed cryptographic
  authentication — its absence degrades that extra filter, not the authentication itself.
  `disabled` mode rejects all inbound traffic before any parse/armor step, so armor
  configuration is moot there.
- **`allowlist` / `pairing` / `open` modes: fail-fast `errUsageConfig` assembly error —
  the SAME treatment `EnvTelegramBotToken` and the other required crypto vars already
  receive in this function.** These modes have **no cryptographic authentication gate at
  all** (ADR 063 Decision 2's own framing: "plaintext modes forfeit the envelope's
  end-to-end confidentiality and per-message replay protection"). For them, armor is not
  an *additional* filter — it is the **only** content-level defense alongside the
  sender-ID gate. Silently fail-opening the one content-level control these modes have
  is the specific gap this ADR closes; assembling `orchestrate` with a plaintext mode
  resolved and no armor configured must be loud and immediate, not a quiet security
  regression discovered later.

This asymmetry is deliberate: the two branches of the mode matrix have different
trust foundations (cryptographic authentication vs. none), so "no armor configured"
carries a different risk profile in each, and the assembly-time behavior reflects that
difference rather than applying one blanket policy.

## Consequences

- **What becomes easier.** An operator who wires `AGENT_BUILDER_TELEGRAM_ARMOR_BIN` gets
  a genuinely enforced armor guard on every mode, closing the gap between the documented
  and actual behavior. An operator who misconfigures a plaintext mode without armor gets
  an immediate, actionable assembly error instead of a silently degraded deployment.
- **What becomes harder / the cost.** `envelope`/`disabled` mode retains a fail-open
  default when unconfigured — an operator relying on armor there must still explicitly
  configure it; this ADR does not change that this is an easy thing to forget on the
  cryptographically-authenticated path. Plaintext-mode operators who have not yet
  configured armor cannot start `orchestrate` at all — a deliberate tradeoff (loud
  failure over silent risk) that this ADR accepts as correct for a mode with no other
  content-level defense.
- **Reversibility.** Fully reversible: removing the env var and the mode-dependent
  branch restores the prior unconditional `allowAllContentGuard` default. No data
  migration; the change is additive config plumbing plus one assembly-time branch.

## Spec / doc footprint

- `docs/spec/configuration.md` — `AGENT_BUILDER_TELEGRAM_AUTH_MODE` row's armor sentence
  rewritten in place to describe the actual (now-true) fail-closed contract; new row for
  `AGENT_BUILDER_TELEGRAM_ARMOR_BIN`.
