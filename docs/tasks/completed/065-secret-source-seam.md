# Task 065: SecretSource seam — pure refactor

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** ✅ (verified)

## Goal

Extract a `SecretSource` interface in a new leaf package `internal/secrets/` and an
`EnvSecretSource` concrete implementation that reproduces today's exact token-reading
behavior. Migrate all four existing token read-sites to use this interface. No behavior
change; the verification gate stays green throughout.

This is the clean foundation for task 066 (vault wiring): instead of scattering vault
client code across four files, task 066 will introduce a `VaultSecretSource` that
implements the same `SecretSource` interface and swap it in at the construction site.

## Context

### Current token read-sites (the four locations to migrate)

1. `internal/executor/claude_cli.go:88-89` — `NewClaudeCLIFromEnv` calls
   `os.Getenv(ClaudeCLIAuthEnv)` and `os.Getenv(ClaudeCLIOAuthEnv)` directly.
2. `internal/runtime/run.go:95-96` — `ConfigFromEnv` reads `executor.ClaudeCLIAuthEnv`
   and `executor.ClaudeCLIOAuthEnv` from `getenv`.
3. `internal/runtime/run.go:105-106` — `ConfigFromEnv` reads `EnvGitToken` and
   `EnvGitHubToken` from `getenv`.
4. `internal/publisher/publisher.go` — `GitToken` / `GitHubToken` fields on
   `GitHubCLIConfig` are set from `runtime.Config` (sourced from step 3 above).

**NOT in scope:** `internal/sandbox/podman/run.go:87` which calls `os.Environ()`
wholesale. The Podman backend is a fallback; the exec-sandbox backend is the primary path
and already does not forward host env. Cleaning up the Podman backend is a separate task.

### SecretSource interface contract

```go
// SecretSource abstracts token retrieval so vault can be substituted for
// os.Getenv without changing call-site code.
type SecretSource interface {
    // ProviderToken returns the Claude auth token and OAuth token.
    // Either may be empty. OAuth is preferred when both are set (ADR 033).
    ProviderToken() (authToken, oauthToken string)

    // PublisherTokens returns the git and GitHub publication tokens.
    // Either may be empty.
    PublisherTokens() (gitToken, githubToken string)
}
```

`EnvSecretSource` reads from env vars and is the only implementation produced by this
task. `VaultSecretSource` (task 066) will implement the same interface.

### Package placement

`internal/secrets/` — a new leaf package. No imports from other `agent-builder/internal/`
packages (dependency direction must be `executor → secrets`, not the reverse). A fitness
check or `go list -deps` assertion confirms this.

## Requirements

| Req ID     | Description                                                                                                                                                                                                       | Priority  |
|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-065-01 | `internal/secrets` package exists with a `SecretSource` interface and an `EnvSecretSource` concrete type implementing it. `EnvSecretSource.ProviderToken()` reads `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` from the process env. A compile-time interface-satisfaction assertion is present. | must have |
| REQ-065-02 | `EnvSecretSource.PublisherTokens()` reads `AGENT_BUILDER_GIT_TOKEN` and `AGENT_BUILDER_GITHUB_TOKEN` from the process env. | must have |
| REQ-065-03 | The four token read-sites are migrated to use `SecretSource`: `internal/executor/claude_cli.go` (NewClaudeCLIFromEnv), `internal/runtime/run.go` (ConfigFromEnv — both provider and publisher token reads). A `FakeSecretSource` in the test file confirms the injection seam. | must have |
| REQ-065-04 | `internal/secrets` is a leaf package: `go list -deps ./internal/secrets/...` contains no `github.com/tkdtaylor/agent-builder/internal/` paths. | must have |
| REQ-065-05 | `make check` exits 0. All existing tests pass unchanged. The fake-provider Phase-0 capstone test (`TestPhase0EndToEndAcceptance`) passes. `docs/spec/configuration.md` notes the new `SecretSource` seam in the Secrets section (or equivalent). | must have |

## Readiness gate

- [x] Test spec `065-secret-source-seam-test-spec.md` exists (written first)
- [ ] Task 064 (ADR-036) merged and accepted — the ADR motivates why this seam exists
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-065-01] TC-065-01: `SecretSource` interface + `EnvSecretSource` + `NewEnvSecretSource` compile; interface-satisfaction assertion present
- [ ] [REQ-065-01] TC-065-02: `EnvSecretSource.ProviderToken()` returns correct values for all four sub-cases
- [ ] [REQ-065-02] TC-065-03: `EnvSecretSource.PublisherTokens()` returns correct values for all four sub-cases
- [ ] [REQ-065-03] TC-065-04: `NewClaudeCLIFromEnv` (or equivalent) delegates to `SecretSource` — verified with a `FakeSecretSource`
- [ ] [REQ-065-03] TC-065-05: `ConfigFromEnv` reads tokens via `SecretSource` — regression test passes
- [ ] [REQ-065-04] TC-065-06: `go list -deps ./internal/secrets/...` shows no internal agent-builder imports
- [ ] [REQ-065-05] TC-065-07: `make check` → `All checks passed.`; `TestPhase0EndToEndAcceptance` → PASS; `docs/spec/configuration.md` updated in same commit

## Verification plan

- **Highest level achievable:** L5 — unit tests + `make check` green. No new runtime
  surface.
- **L6:** N/A — pure refactor; the existing L6 capstone evidence (tasks 062/063) is
  the runtime ground truth.
- **Harness command:**
  ```
  go test -count=1 ./internal/secrets/... ./internal/executor/... ./internal/runtime/...
  go list -deps ./internal/secrets/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check prints `PASS-leaf`; e2e test `PASS`;
  final line `All checks passed.`

## Out of scope

- Vault client code, socket protocol, lifecycle (task 066).
- `VaultSecretSource` implementation (task 066).
- Removing `os.Environ()` forwarding from `internal/sandbox/podman/run.go` (separate task).
- Changing exec-sandbox wiring (unchanged; `secret_refs`, `vault_socket`, `injection_mode`
  remain empty — that changes in task 066).
- New env vars (no new configuration surface is added by this task).
- Updating `docs/spec/interfaces.md` with vault-specific details (lands in 066 with code).

## Dependencies

- Task 064 (ADR-036) — must be merged before starting (provides the architectural rationale
  that the task-executor reads before implementing).
