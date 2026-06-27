# Task 088: Vault-brokered per-provider auth

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Extend the `secrets.SecretSource` seam to support **named-provider secret resolution**
via `NamedProviderToken(ref string) (string, error)`. Each registry entry's `SecretRef`
names which vault secret to resolve at dispatch; this method is the call-site that
does that resolution.

The extension makes each provider token independently revocable (SPEC invariant 5 /
ADR 043): revoking the Gemini key does not touch the Claude token, because each is a
distinct vault-brokered credential keyed by `SecretRef`.

The existing `ProviderToken()` and `PublisherTokens()` methods must not change
behavior — this is additive.

## Context

Today `SecretSource.ProviderToken()` returns one fixed pair (Claude API key + OAuth
token). ADR 043 requires the router to resolve a per-entry secret at dispatch. This
task adds the method that resolves a named secret — both via env fallback
(`EnvSecretSource`) and via vault (`VaultSecretSource`).

The env fallback convention: `SecretRef "codex-token"` → env var
`AGENT_BUILDER_SECRET_CODEX_TOKEN` (uppercased, hyphens→underscores, prefixed).

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                          | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-088-01 | `SecretSource` interface gains `NamedProviderToken(ref string) (string, error)`. `EnvSecretSource.NamedProviderToken` resolves the ref to `AGENT_BUILDER_SECRET_<REF_UPPER>` env var; returns `("", ErrSecretNotFound)` if the var is unset. Existing `ProviderToken()` behavior unchanged. | must have |
| REQ-088-02 | `VaultSecretSource.NamedProviderToken` resolves the ref via vault's put/resolve round-trip (same mechanism as task 066). Returns an opaque handle (never plaintext); `ErrSecretNotFound` if the ref is not in vault. | must have |
| REQ-088-03 | Both `EnvSecretSource` and `VaultSecretSource` have compile-time assertions that they implement the updated `SecretSource` interface. | must have |
| REQ-088-04 | `internal/secrets` remains a leaf after extension: `go list -deps ./internal/secrets/...` adds no new `agent-builder/internal` imports beyond `internal/vault` (already present). `make check` passes. | must have |

## Readiness gate

- [x] Test spec `088-vault-per-provider-auth-test-spec.md` exists (written first)
- [ ] Task 087 merged (registry entry has `SecretRef` field — this task resolves it)
- [ ] Task 066 merged (vault round-trip pattern established; VaultSecretSource exists)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-088-01] TC-088-01: `EnvSecretSource{}.NamedProviderToken("codex-token")` with matching env var → `("sk-test-codex-key", nil)`; missing env var → `("", ErrSecretNotFound)`
- [ ] [REQ-088-01] TC-088-02: Existing `ProviderToken()` behavior unchanged (regression guard)
- [ ] [REQ-088-02] TC-088-03: `VaultSecretSource.NamedProviderToken("gemini-api-key")` with pre-put vault secret → opaque handle (not plaintext); missing ref → `("", ErrSecretNotFound)`
- [ ] [REQ-088-03] TC-088-04: Compile-time assertions for both `EnvSecretSource` and `VaultSecretSource` against updated `SecretSource` interface
- [ ] [REQ-088-04] TC-088-05: `go list -deps ./internal/secrets/...` → no new forbidden packages; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 (TC-088-03 with real vault binary, same gate as
  task 066 `AGENT_BUILDER_LIVE_VAULT=1`).
- **Harness command:**
  ```
  go test -count=1 ./internal/secrets/...
  go list -deps ./internal/secrets/...
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/secrets`
  - `go list` → no new forbidden packages
  - `make check` → `All checks passed.`
- **L5 live vault (optional, if vault binary present):**
  ```
  AGENT_BUILDER_LIVE_VAULT=1 AGENT_BUILDER_VAULT_BIN=<path> go test -count=1 \
    ./internal/secrets/... -run TestNamedProviderTokenVault
  ```

## Out of scope

- Changing how the existing coding-agent pipeline resolves Claude credentials (still
  via `ProviderToken()` until task 095 wires the real router).
- Per-provider env-var injection into the subprocess — that is in harness adapters
  (tasks 089, 090, 091), which use the handle returned by `NamedProviderToken`.
- Router selection logic (task 092).

## Dependencies

- Task 087 (registry type — provides the `SecretRef` field that this task resolves).
- Task 066 (vault round-trip pattern + `VaultSecretSource` exists).
- Informs: tasks 089, 090, 091 (harness adapters use `NamedProviderToken` to get
  the auth token for the subprocess); task 092 (router calls `NamedProviderToken`
  at dispatch).
