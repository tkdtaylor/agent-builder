# ADR 033 — Executor accepts a subscription OAuth token as an alternative to the API key

**Status:** Accepted
**Date:** 2026-06-17
**Relates to:** ADR 031 (L6 live-mode probes), the CLAUDE.md egress invariant ("executor tokens are independently revocable + fast to rotate")

## Context

The Claude Code CLI executor ([internal/executor/claude_cli.go](../../../internal/executor/claude_cli.go))
authenticated with exactly one credential: `ANTHROPIC_API_KEY`. `validate()` failed before
subprocess start when it was blank, and `claudeEnv()` injected it while wiping `HOME`,
`XDG_CONFIG_HOME`, and `XDG_CACHE_HOME` so the CLI reads no host-home credential files. That
isolation is deliberate — it keeps the credential to a single, scoped, env-injected value rather
than a host login session.

Two problems surfaced when driving the live Phase-0 probes (022/028/032):

1. **API credit is a hard wall.** The provided `ANTHROPIC_API_KEY` account ran out of credit
   (`Credit balance is too low`), so no real agent run could complete regardless of code.
2. **Subscription is the primary real-world use case.** The operator runs Claude on a Pro/Max
   subscription, not a metered API key. The CLI supports a subscription-backed headless credential
   — `CLAUDE_CODE_OAUTH_TOKEN`, minted by `claude setup-token` — that bills against the
   subscription rather than API credit.

A raw interactive `claude /login` session was rejected because it stores credentials in the host
home (`~/.claude/.credentials.json`) — mounting that would break the isolation invariant and is
not independently revocable per-credential. The OAuth token does not have that problem: it is an
env-injectable, independently revocable value.

## Decision

**The executor accepts either `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`.** Both satisfy
the load-bearing invariant — env-injected, scoped, independently revocable — so neither weakens
the security model.

Concretely:
- `ClaudeCLIConfig` carries the token **and** its kind. `CLAUDE_CODE_OAUTH_TOKEN`, when present,
  is **preferred** over `ANTHROPIC_API_KEY`.
- `claudeEnv()` injects **exactly one** auth variable — the chosen one — and strips the other from
  the subprocess environment. Passing both to the CLI lets `ANTHROPIC_API_KEY` silently win and
  bill the wrong account; we prevent that by never injecting both.
- The `HOME`/XDG wipe is unchanged. Both credentials are env vars; isolation is preserved.
- `ConfigFromEnv` requires **at least one** of the two credentials and fails loudly when both are
  absent. When both are set, the OAuth token wins and the API key is not injected (logged-by-design
  precedence, not a silent surprise).
- Output sanitization redacts whichever token is in use.

## Consequences

- The orchestrator runs on a Claude subscription with no API credit — the realistic primary path,
  and it unblocks live probes 022/028/032 without per-call billing.
- The auth contract is now "one of two named env credentials," documented in `docs/spec/configuration.md`
  and asserted by unit tests on the env-construction and precedence logic.
- Precedence is explicit and one-directional (OAuth > API key). A future third mode would extend
  the same select-exactly-one rule rather than adding more implicit fallthrough.
- The token must be minted on a subscription-logged-in host (`claude setup-token`) and placed in the
  gitignored `.env`; it is never committed (protect-secrets hook guards this).
