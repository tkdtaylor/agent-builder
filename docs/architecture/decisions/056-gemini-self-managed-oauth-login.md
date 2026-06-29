# ADR 056 — Gemini executor accepts self-managed OAuth login (cached via CLI)

**Status:** Accepted
**Date:** 2026-06-29
**Relates to:** ADR 033 (subscription OAuth as executor credential alternative), task 132 (Gemini subscription/OAuth auth path)

## Context

ADR 033 established that executors may accept a subscription OAuth token as an alternative to a metered API key — specifically, `CLAUDE_CODE_OAUTH_TOKEN` for Claude, minted by `claude setup-token` and injected into the subprocess environment by the executor. This unblocked the orchestrator to run on a subscription account without API credit constraints.

The Gemini executor (`internal/executor/gemini_cli.go`, task 090) initially supported only API-key authentication via `GEMINI_API_KEY`. A paid Gemini subscription authenticates through the `gemini` CLI's OAuth login, but the login mechanism is **self-managed by the CLI** — credentials are cached in `~/.gemini` by the CLI itself during `gemini auth login`, and the CLI reads them at runtime. This is distinct from Claude's pattern where the executor injects the OAuth token into the environment.

## Problem

When an operator has a Gemini subscription but no API key, the executor fails with `ErrGeminiSecretNotFound` before the subprocess even starts. The operator cannot use the existing Gemini executor to route work to a paid subscription.

## Decision

**The Gemini executor accepts an empty `SecretRef` as a signal for subscription/OAuth mode.** When `entry.SecretRef == ""`:
- The executor does NOT call `secretSource.NamedProviderToken`.
- The executor does NOT inject `GEMINI_API_KEY` into the subprocess environment.
- The executor strips any pre-existing `GEMINI_API_KEY` from the base environment (to force OAuth over stray keys).
- The executor sets `GEMINI_MODEL` (as before) and preserves `HOME` so the `gemini` CLI reads its cached login.
- The subprocess runs with inherited environment, relying on the CLI to use `~/.gemini`.

This mirrors ADR 033's pattern (accepting a credential alternative) but deviates in **implementation**: the executor does not inject the credential; instead, it trusts the CLI's self-managed login and stays out of its way.

The same `entry.SecretRef == ""` signal is used by `ConfigFromEnv` (line 243–260 in `internal/runtime/run.go`) to skip cloud-credential enforcement when all enabled entries are local. Gemini subscription entries now fall into this category alongside translation-proxy entries (`local`, `local-qwen`, `local-ollama`): they have empty `SecretRef` and require no cloud API key in the operator's environment.

## Consequences

- The Gemini executor now supports two authentication modes: API-key (`SecretRef != ""`) and subscription/OAuth (`SecretRef == ""`). The executor branches on `SecretRef` early in `run()` to resolve the key before subprocess creation (API-key mode) or skip resolution entirely (subscription mode).
- The registry loader's `localHarnessEntries` map is extended to include `"gemini"` (alongside `"local-qwen"`, `"local"`, `"local-ollama"`), allowing Gemini subscription entries to have empty `SecretRef` without triggering a loader error.
- An operator with a Gemini subscription can now register it with `AGENT_BUILDER_REGISTRY_GEMINI_ENABLED=true`, `AGENT_BUILDER_REGISTRY_GEMINI_SECRET_REF=""` (empty), and optionally invoke `gemini setup-token` to mint an environment-injectable token (an alternative to interactive `gemini auth login`, for headless automation). The executor respects either approach.
- The `docs/spec/configuration.md` and `docs/spec/interfaces.md` are updated to document the Gemini subscription entry configuration and the two executor auth modes.
- This decision is independent of Claude's subscription pattern (ADR 033) in its authentication *mechanism* — the CLI manages credentials — but shares the same *configuration pattern* (empty `SecretRef` signals local/self-managed auth).

## Related decisions

- **ADR 033** — subscription OAuth as an executor credential, specifically for Claude and its `CLAUDE_CODE_OAUTH_TOKEN`.
- **ADR 031** — L6 live-mode probes, which unblocked ADR 033 when Claude API credit ran out and revealed the Gemini subscription as the next priority.
