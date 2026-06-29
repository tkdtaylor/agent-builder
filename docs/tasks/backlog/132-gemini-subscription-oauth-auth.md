# Task 132 — Gemini subscription/OAuth auth path

**Status:** backlog
**Spec:** `docs/tasks/test-specs/132-gemini-subscription-oauth-auth-test-spec.md`
**Relates to:** ADR 033 (subscription OAuth as an alternative to the metered API key — Claude);
task 090 (the `GeminiCLI` executor, API-key-only, L6-deferred).

## Goal

Make the Gemini executor usable with a **paid Gemini subscription** (OAuth login), not just a
metered `GEMINI_API_KEY`. This is the immediate priority: the multi-LLM router has Claude + local
built; Gemini is the missing third brain, and the operator has a subscription.

## Context (verified)

`internal/executor/gemini_cli.go` (`run`) always resolves `GEMINI_API_KEY` via
`entry.SecretRef → secretSource.NamedProviderToken` and injects it (`geminiEnv`), erroring
`ErrGeminiSecretNotFound` with no key. A subscription authenticates the `gemini` CLI via OAuth
login (Google account; creds cached in `~/.gemini`) — no API key. Mirror **ADR 033**: an
**empty `SecretRef`** signals the no-cloud-key path, and `ConfigFromEnv` (`internal/runtime/run.go`
~243–260) already skips the cloud-credential requirement when all enabled entries have empty
`SecretRef`. Each executor interprets its own empty-`SecretRef` entry — Claude uses the local
translation-proxy; **Gemini interprets it as subscription/OAuth (let the CLI use its cached login).**

## Scope

- **`internal/executor/gemini_cli.go`:**
  - `run()` branches on `entry.SecretRef == ""` (subscription) vs non-empty (API-key).
  - **Subscription mode:** do NOT call `NamedProviderToken`; do NOT inject `GEMINI_API_KEY`; strip
    any pre-existing `GEMINI_API_KEY` from the base env (force OAuth); set `GEMINI_MODEL`; run
    `gemini --model <ModelID> <prompt>` with inherited env (preserve `HOME` → CLI uses `~/.gemini`).
    Add a `geminiSubscriptionEnv(base, modelID)` helper (strip key, set model) alongside the
    existing `geminiEnv`.
  - **API-key mode:** unchanged from task 090 (do not regress).
  - Confirm `sanitizeGeminiOutput(out, err, "")` is a safe no-op on the key-redaction step.
- **Config/registry:** verify (test) a Gemini subscription entry (`SecretRef == ""`) loads, passes
  `ConfigFromEnv` with no cloud key, and routes through `buildExecutorForEntry` to `*GeminiCLI`.
- **Spec (same commit):** `docs/spec/configuration.md` (Gemini subscription entry — empty SecretRef,
  no key, OAuth via `gemini` CLI login); `docs/spec/interfaces.md` (executor auth modes: API key /
  subscription-OAuth / local).
- **ADR:** reference ADR 033. Write a new ADR (next free number — check
  `docs/architecture/decisions/`) ONLY if you judge the gemini self-managed-credential mechanism a
  materially distinct decision from ADR 033 (it plausibly is: Claude injects a token env var;
  gemini relies on its own cached login and we inject nothing). Otherwise document as an ADR-033
  extension in the spec.

## Out of scope

- Installing the `gemini` CLI and the OAuth login (operator prerequisites, done by the user).
- The live L6 run (separate, operator-gated step after this lands).
- Router cost/capability tuning for Gemini (later).

## Verification plan

- **Highest level achievable now: L3.** `go test -race -count=1 ./internal/executor/...
  ./internal/runtime/...` + `make check` green; all TCs in the spec hard-assert exact values.
- **Producer→consumer trace:** registry entry (`SecretRef == ""`) → `ConfigFromEnv` exemption →
  `buildExecutorForEntry` → `NewGeminiCLI` → `run()` subscription branch → `gemini` subprocess env
  with no `GEMINI_API_KEY`.
- **L6 (operator, deferred to the live run):** CLI installed + logged in → subscription entry →
  scoped goal routed to Gemini → gate-passing branch produced via the subscription (no API key).

## Boundaries

- Do not regress the API-key path (task 090).
- Do not inject an empty/placeholder `GEMINI_API_KEY` in subscription mode (it could shadow OAuth).
- Test-spec-first; spec files updated in the same commit; commit at 🟡 on the task branch.
