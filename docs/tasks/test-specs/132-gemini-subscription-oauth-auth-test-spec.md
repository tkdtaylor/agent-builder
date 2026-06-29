# Test spec — Task 132: Gemini subscription/OAuth auth path

**Task:** `docs/tasks/backlog/132-gemini-subscription-oauth-auth.md`
**Relates to:** ADR 033 (executor accepts a subscription OAuth credential as an alternative to the
metered API key — established for Claude); task 090 (the `GeminiCLI` executor, API-key-only).

## Context

Task 090 built `internal/executor/gemini_cli.go` but it is **API-key-only**: `run()` always
resolves `GEMINI_API_KEY` via `entry.SecretRef → secretSource.NamedProviderToken` and injects it,
erroring `ErrGeminiSecretNotFound` when no key is configured. A **paid Gemini subscription**
authenticates the `gemini` CLI via **OAuth login** (Google account; credentials cached by the CLI
in `~/.gemini`), not an API key — so the executor must gain a subscription mode, mirroring ADR 033
for Claude. The signal for subscription mode is an **empty `SecretRef`** (consistent with the
existing "all enabled entries local → skip cloud-credential check" exemption in
`ConfigFromEnv`, `internal/runtime/run.go`).

## Requirements

- **REQ-132-01** — When `entry.SecretRef == ""` (subscription mode), `GeminiCLI.run` does NOT call
  `secretSource.NamedProviderToken`, does NOT inject `GEMINI_API_KEY`, and does NOT error on a
  missing secret. It invokes `gemini --model <ModelID> <prompt>` with the inherited process
  environment (so `HOME` is preserved and the CLI uses its cached OAuth login).
- **REQ-132-02** — In subscription mode, any pre-existing `GEMINI_API_KEY` in the base environment
  is **stripped** from the subprocess env (force OAuth; a stray env key must not shadow the
  subscription). `GEMINI_MODEL` is still set to `entry.ModelID`.
- **REQ-132-03** — When `entry.SecretRef != ""` (API-key mode), behavior is **unchanged** from task
  090: resolve the key via `NamedProviderToken`, inject `GEMINI_API_KEY=<token>` and
  `GEMINI_MODEL`, and error `ErrGeminiSecretNotFound` (subprocess not invoked) when the key is
  missing.
- **REQ-132-04** — A registry containing only a Gemini **subscription** entry (`SecretRef == ""`)
  satisfies `ConfigFromEnv` with no cloud credential set (no `ANTHROPIC_API_KEY` /
  `CLAUDE_CODE_OAUTH_TOKEN` / `GEMINI_API_KEY`), and `buildExecutorForEntry` routes it to a
  `*GeminiCLI`.
- **REQ-132-05** — Output sanitization is safe in subscription mode (no API key to redact; no panic
  or accidental empty-string redaction).

## Test cases

- **TC-132-01** (REQ-132-01) `TestGeminiSubscriptionModeSkipsKeyAndRuns`: construct `GeminiCLI` with
  `testGeminiEntry("")` (empty SecretRef) and a secret source whose `NamedProviderToken` **fails the
  test if called** (e.g. calls `t.Fatal`). Stub the subprocess (exit 0, stdout `BRANCH:
  task/132-test`). Assert: secret source never consulted; subprocess env contains **no**
  `GEMINI_API_KEY=`; contains `GEMINI_MODEL=gemini-2.0-flash`; args are `--model gemini-2.0-flash`
  + prompt; `cmd.Dir == worktree`; result `{Branch:"task/132-test", OK:true}`.
- **TC-132-02** (REQ-132-02) `TestGeminiSubscriptionModeStripsStrayApiKey`: base env contains
  `GEMINI_API_KEY=stray`; subscription entry. Assert the subprocess env has **no** `GEMINI_API_KEY=`
  entry (stripped) and still has `GEMINI_MODEL=`.
- **TC-132-03** (REQ-132-03) `TestGeminiApiKeyModeUnchanged`: `testGeminiEntry("gemini-api-key")`
  with a source returning `gai-test`. Assert env contains `GEMINI_API_KEY=gai-test` and
  `GEMINI_MODEL=gemini-2.0-flash`, exit 0 → `{Branch, OK:true}` (regression guard for task 090
  TC-090-02).
- **TC-132-04** (REQ-132-03) `TestGeminiApiKeyModeStillErrorsOnMissingSecret`: API-key entry, source
  returns `ErrSecretNotFound`. Assert `run` returns an error wrapping `ErrGeminiSecretNotFound` and
  the subprocess is **not** invoked (regression guard for TC-090-03). Subscription mode must NOT
  exhibit this (covered by TC-132-01).
- **TC-132-05** (REQ-132-04) `TestConfigFromEnvAllowsGeminiSubscriptionEntryWithoutCloudKey`: build
  a registry whose only entry is a Gemini subscription entry (`SecretRef == ""`), with no cloud
  credential env set; assert `ConfigFromEnv` succeeds (no missing-credential error) and
  `buildExecutorForEntry` for that entry returns a non-nil `*executor.GeminiCLI`.
- **TC-132-06** (REQ-132-05) `TestSanitizeGeminiOutputSafeWithEmptyKey`: `sanitizeGeminiOutput(out,
  err, "")` returns the combined output unchanged (no panic, no spurious redaction of empty string).

## Verification levels

- **L2/L3:** `go test -race -count=1 ./internal/executor/... ./internal/runtime/...` and
  `make check` green. All TCs hard-assert exact values (no smoke tests).
- **L6 (operator, the point):** with the `gemini` CLI installed and the operator logged in via
  OAuth, register a Gemini subscription entry, route a scoped goal to it, and observe a
  **gate-passing branch produced via the subscription** (proxy/CLI shows no API key used). Recorded
  by operator observation per the verification ladder. Gated on the operator prerequisites (CLI
  install + login); not CI-automatable.
