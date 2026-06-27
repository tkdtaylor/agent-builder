# Task 091: Local entry + translation-proxy seam

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Add the local-LLM config variant to the executor registry and document the
**translation-proxy seam** — the named pattern for fronting a local OpenAI-API
inference server with an Anthropic-compatible endpoint so the existing Claude CLI
harness can drive it.

A local entry is NOT a new harness. Per ADR 043, it is the Claude CLI harness
(`HarnessClaudeCLI`) configured with:
- `Endpoint` pointing at a local translation proxy (e.g. `http://localhost:8080`)
- `SecretRef == ""` (no cloud auth)
- `Budget.Limit == 0` (unlimited — no subscription cap; never marked exhausted)

This task also extends `executor.ClaudeCLI` (or adds `executor.NewClaudeCLIFromEntry`)
to accept a `RegistryEntry`, so the router can construct the adapter without a
bespoke constructor per entry.

## Context

ADR 043 explains the one-harness-many-entries factoring: Claude Code honors
`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`, so pointing it at a translation proxy
drives a local model without a new harness. The translation proxy (LiteLLM,
claude-code-router, or similar) presents an Anthropic-compatible endpoint over an
OpenAI-API local server. This task names and tests that seam.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                   | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-091-01 | `registry.LoadFromEnv()` recognizes the `"local-qwen"` (and a generic `"local"`) entry pattern: `Harness = HarnessClaudeCLI`, `SecretRef = ""`, `Budget.Limit = 0` (unlimited). An empty `SecretRef` is valid (not an error). | must have |
| REQ-091-02 | The `Endpoint` field convention for local entries is documented in `internal/registry` source: the entry's `Endpoint` is the translation-proxy URL, not the model's own URL. A named constant or doc comment identifies the LiteLLM / claude-code-router pattern as the named seam. | must have |
| REQ-091-03 | `executor.ClaudeCLI` constructed from a local `RegistryEntry` sets `ANTHROPIC_BASE_URL=entry.Endpoint` in the subprocess env and does NOT inject `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN` (empty `SecretRef` → no cloud auth). An `IsUnlimited()` predicate returns `true` when `Budget.Limit == 0`. | must have |
| REQ-091-04 | `executor.NewClaudeCLIFromEntry(entry RegistryEntry, secretSource secrets.SecretSource)` (or an updated `NewClaudeCLI`) compiles alongside existing call sites. No breaking change to existing constructors. | must have |

## Readiness gate

- [x] Test spec `091-local-entry-translation-proxy-test-spec.md` exists (written first)
- [ ] Task 087 merged (registry type + `LoadFromEnv`)
- [ ] Task 088 merged (`NamedProviderToken`)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-091-01] TC-091-01: `LoadFromEnv()` with local-entry env vars → entry with `Harness=HarnessClaudeCLI`, `SecretRef=""`, `Budget=zero`
- [ ] [REQ-091-02] TC-091-02: `entry.Endpoint` is the proxy URL; source doc names the translation-proxy seam
- [ ] [REQ-091-03] TC-091-03: `ClaudeCLI` from local entry sets `ANTHROPIC_BASE_URL`; no cloud auth env vars injected; stub subprocess returns OK
- [ ] [REQ-091-03] TC-091-04: `entry.IsUnlimited()` → `true` when `Budget.Limit == 0`
- [ ] [REQ-091-04] TC-091-05: `NewClaudeCLIFromEntry` compiles; existing call sites unchanged; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess.
- **Harness command:**
  ```
  go test -count=1 ./internal/registry/... ./internal/executor/...
  make check
  ```
- **L6 live (operator-run):** start translation proxy + local inference server;
  exercise adapter against real worktree. Note in verify commit.

## Out of scope

- Local model benchmarking (task 094).
- Router fallback to local when cloud is exhausted (task 092).
- Building or shipping the translation proxy (external tool).

## Dependencies

- Task 087 (registry type + `LoadFromEnv`).
- Task 088 (vault-brokered auth — `NamedProviderToken`).
- Informs: task 092 (router constructs adapters from entries); task 094 (local model
  evaluation, which picks `ModelID`/`Endpoint` config for this entry); task 095.
