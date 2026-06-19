# Test spec — Task 065: SecretSource seam (pure refactor)

**Linked task:** `docs/tasks/backlog/065-secret-source-seam.md`
**Written:** 2026-06-19
**Status:** ready

## Context

Today agent-builder reads tokens in three places:
1. `internal/executor/claude_cli.go:88-89` — `NewClaudeCLIFromEnv` reads
   `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` directly via `os.Getenv`.
2. `internal/runtime/run.go:95-96` — `ConfigFromEnv` reads the same env vars
   and stores them in `Config.ClaudeToken` / `Config.ClaudeOAuthToken`.
3. `internal/runtime/run.go:105-106` — `ConfigFromEnv` reads `AGENT_BUILDER_GIT_TOKEN`
   and `AGENT_BUILDER_GITHUB_TOKEN` into `Config.GitToken` / `Config.GitHubToken`.
4. `internal/sandbox/podman/run.go:87` — the Podman runner inherits `os.Environ()`
   wholesale (all host env vars forwarded into the launch subprocess).

This task introduces a `SecretSource` interface in `internal/secrets/` and an
`EnvSecretSource` concrete implementation that reproduces today's exact behavior.
It is a pure refactor: no behavior change, no vault wiring, gate stays green.

This seam is what task 066 will plug vault behind — without this seam, the vault
wiring task would scatter changes across four files instead of swapping one constructor.

All four existing token read-sites are migrated to use `SecretSource`. The `os.Environ()`
wholesale forwarding in `internal/sandbox/podman/run.go` is **not** changed in this task
(the Podman backend is a fallback; the exec-sandbox backend is the primary path and does
not forward the host env).

## Requirements coverage

| Req ID     | Test cases               | Covered? |
|------------|--------------------------|----------|
| REQ-065-01 | TC-065-01, TC-065-02     | yes      |
| REQ-065-02 | TC-065-03                | yes      |
| REQ-065-03 | TC-065-04, TC-065-05     | yes      |
| REQ-065-04 | TC-065-06                | yes      |
| REQ-065-05 | TC-065-07                | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-065-01 — `SecretSource` interface and `EnvSecretSource` shape

- **Requirement:** REQ-065-01
- **Level:** L2 (compile-time / go list)
- **Test file:** `internal/secrets/secrets_test.go`

**Assertions:**
- Package `internal/secrets` exists and compiles.
- `SecretSource` is an interface with at least the method
  `ProviderToken() (authToken, oauthToken string)` and
  `PublisherTokens() (gitToken, githubToken string)`.
  (Exact method names may differ — the test asserts the interface is satisfied by
  `EnvSecretSource`, not the exact names; adjust if the implementation uses different
  names but equivalent semantics.)
- `EnvSecretSource` is a concrete struct that implements `SecretSource`.
- `NewEnvSecretSource()` constructs an `EnvSecretSource`.
- A compile-time assertion `var _ SecretSource = (*EnvSecretSource)(nil)` passes.

---

### TC-065-02 — `EnvSecretSource.ProviderToken` reads ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN

- **Requirement:** REQ-065-01
- **Level:** L5 (unit test with env manipulation)
- **Test file:** `internal/secrets/secrets_test.go`

Sub-cases (table-driven):

| Env state | Expected authToken | Expected oauthToken |
|-----------|-------------------|---------------------|
| `ANTHROPIC_API_KEY=sk-123`, `CLAUDE_CODE_OAUTH_TOKEN` unset | `"sk-123"` | `""` |
| `ANTHROPIC_API_KEY` unset, `CLAUDE_CODE_OAUTH_TOKEN=oauth-tok` | `""` | `"oauth-tok"` |
| Both set | `"sk-123"` | `"oauth-tok"` |
| Neither set | `""` | `""` |

**Assertion:** `EnvSecretSource.ProviderToken()` returns `(authToken, oauthToken)` exactly
matching the above table for each sub-case. No other env vars are read by this method.

---

### TC-065-03 — `EnvSecretSource.PublisherTokens` reads AGENT_BUILDER_GIT_TOKEN and AGENT_BUILDER_GITHUB_TOKEN

- **Requirement:** REQ-065-02
- **Level:** L5 (unit test with env manipulation)
- **Test file:** `internal/secrets/secrets_test.go`

Sub-cases:

| Env state | Expected gitToken | Expected githubToken |
|-----------|------------------|---------------------|
| `AGENT_BUILDER_GIT_TOKEN=gittok`, `AGENT_BUILDER_GITHUB_TOKEN` unset | `"gittok"` | `""` |
| `AGENT_BUILDER_GIT_TOKEN` unset, `AGENT_BUILDER_GITHUB_TOKEN=ghtok` | `""` | `"ghtok"` |
| Both set | `"gittok"` | `"ghtok"` |
| Neither set | `""` | `""` |

**Assertion:** `EnvSecretSource.PublisherTokens()` returns `(gitToken, githubToken)`
matching the above table. No side effects; reads env exactly once per call (or on
construction — either is fine; the contract is the return values).

---

### TC-065-04 — `NewClaudeCLIFromEnv` delegates to `SecretSource` (not direct `os.Getenv`)

- **Requirement:** REQ-065-03
- **Level:** L5 (unit test with fake `SecretSource`)
- **Test file:** `internal/executor/claude_cli_test.go` (new sub-test or extension)

**Setup:** construct a `FakeSecretSource` that returns fixed `("sk-fake", "")` from
`ProviderToken()`. Pass it to the refactored `NewClaudeCLIFromEnv` (or equivalent
constructor that accepts a `SecretSource`).

**Assertion:**
- The resulting `ClaudeCLI` has `AuthToken == "sk-fake"` and `OAuthToken == ""`
  (from the fake, not from the real env).
- A parallel sub-case with `FakeSecretSource` returning `("", "oauth-fake")` produces
  `AuthToken == ""` and `OAuthToken == "oauth-fake"`.

**Note:** if `NewClaudeCLIFromEnv` is kept as a no-argument convenience wrapper
(backward compat), it must internally use `EnvSecretSource`; the test verifies this by
confirming the function calls `EnvSecretSource` (indirectly, by temporarily overriding
the env and seeing the expected token flow through).

---

### TC-065-05 — `ConfigFromEnv` reads tokens via `SecretSource` (not direct `os.Getenv` calls at the call-site)

- **Requirement:** REQ-065-03
- **Level:** L5 (unit test)
- **Test file:** `internal/runtime/run_test.go` (existing or new sub-test)

**Assertion:**
- `ConfigFromEnv` with `ANTHROPIC_API_KEY=sk-env` set produces `Config.ClaudeToken == "sk-env"`.
- `ConfigFromEnv` with `CLAUDE_CODE_OAUTH_TOKEN=oauth-env` set produces `Config.ClaudeOAuthToken == "oauth-env"`.
- `ConfigFromEnv` with `AGENT_BUILDER_GIT_TOKEN=gittok` set produces `Config.GitToken == "gittok"`.
- Behavior is unchanged from pre-refactor (this is a regression guard — the test may already
  exist and simply needs to keep passing; if it exists it must continue to pass after the refactor).

---

### TC-065-06 — `internal/secrets` is a leaf package (no internal deps; stdlib only)

- **Requirement:** REQ-065-04
- **Level:** L3 (import-graph check)
- **Test file:** CI / `Makefile` fitness step, or in-process `go list -deps` assertion

**Assertion:**
- `go list -deps ./internal/secrets/...` output contains no
  `github.com/tkdtaylor/agent-builder/internal/` package paths.
- The package imports nothing from `internal/executor`, `internal/runtime`,
  `internal/supervisor`, or any other agent-builder internal package.
- This confirms the dependency direction: `executor` and `runtime` depend on `secrets`,
  not the reverse.

---

### TC-065-07 — `make check` exits 0; no behavior change from pre-refactor

- **Requirement:** REQ-065-05
- **Level:** L3 / L5
- **Test file:** all existing tests pass unchanged

**Assertions:**
- `go test ./...` exits 0 with all existing tests passing.
- `make fitness` exits 0 (no new fitness failures introduced).
- `make check` → `All checks passed.`
- `go build ./...` exits 0.
- The Phase-0 capstone fake-provider test `TestPhase0EndToEndAcceptance` still passes:
  `go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance` → PASS.
- `docs/spec/` is updated in the same commit: `configuration.md` references
  `internal/secrets.SecretSource` as the token-reading seam (or updates the Secrets
  section to note the abstraction), and `interfaces.md` notes the `SecretSource`
  interface if it is part of the public surface of an internal package.

---

## Verification plan

- **Highest level achievable:** L5 — all unit tests + `make check` green. No runtime
  surface to exercise beyond the existing fake-provider Phase-0 acceptance test.
- **L6:** N/A — this is a pure refactor with no new runtime behavior. The existing L6
  capstone evidence (tasks 062/063) remains the runtime ground truth; re-running it is
  not required for this task.
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

- `FakeSecretSource` for use by task 066 (that task will introduce its own fake or
  reuse this one — defer the decision to 066).
- Vault client code, vault lifecycle management (task 066).
- Removing the `os.Environ()` wholesale forwarding from `internal/sandbox/podman/run.go`
  (the Podman backend is not the primary path; that cleanup is a separate ADR-driven task).
- Changing any externally-visible behavior — the only observable effect of this task is
  that `internal/secrets` appears as a new importable leaf package.
- Updating the egress allowlist or exec-sandbox wiring (unchanged).
