# Task 066: vault token brokering via proxy mode

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** 🟡 (code merged; L6 capstone TC-066-05/06/07 pending operator run)

## Goal

Wire the vault block into agent-builder so that git and GitHub tokens are brokered
through vault's proxy injection rather than forwarded raw into the execution box.
Retire the "accepted token-in-box risk" for the git/GitHub publication tokens by:

1. Starting a vault daemon subprocess before the agent loop dispatches a task.
2. Storing git/GitHub tokens in vault via `put` and resolving them to opaque handles.
3. Passing the handles + vault socket path + `injection_mode="proxy"` through
   `RunRequest.wiring` to the exec-sandbox block.
4. exec-sandbox's spawn logic calls `vault.inject` per handle; the egress proxy injects
   the credential into outbound requests; the sandbox never sees the plaintext.

The provider token (Claude: `CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`) is
explicitly deferred to a follow-on task pending the feasibility probe in TC-066-07.

## Context

### Components introduced

**`internal/vault/client.go`** — minimal Go client for the vault Unix socket.
The vault protocol is newline-delimited JSON over a `0600` peer-uid-gated Unix socket:

```
// put
{"op":"put","secret_ref":"vault://agent-builder/git-token","value":"<tok>","injection_floor":"proxy","binding":{"host":"api.github.com","header":"Authorization","scheme":"Bearer","env_var":"GIT_TOKEN"}}
// resolve
{"op":"resolve","secret_ref":"vault://agent-builder/git-token","ttl":300}
// -> {"handle":"<opaque>","ttl":300,"injection_mode":"proxy"}
// ping
{"op":"ping"} -> {"ok":true}
```

Methods needed: `Ping() error`, `Put(secretRef, value, floor string, binding Binding) error`,
`Resolve(secretRef string, ttl int) (ResolveResult, error)`.
No `Inject` method — that is called by exec-sandbox, not agent-builder.

**`internal/vault/lifecycle.go`** — starts/stops a vault daemon subprocess.
```
VaultDaemon{BinPath, SocketPath, StorePath string}
Start(ctx context.Context) error  // execs vault serve; waits for Ping to succeed
Stop() error                      // kills the subprocess; cleans up socket file
```
Master key: `VAULT_MASTER_KEY` env var (hex-encoded 32 bytes) or file at
`VAULT_MASTER_KEY_FILE`. At minimum one must be set when vault is enabled; absence fails
loud before daemon start (never auto-generates an in-memory key silently — that loses
secrets across restarts).

**`internal/secrets/vault_source.go`** — `VaultSecretSource` implements `secrets.SecretSource`
(task 065 interface). On construction:
- Puts git token under `vault://agent-builder/git-token` with `binding.host:"api.github.com"`.
- Puts GitHub token under `vault://agent-builder/github-token` with `binding.host:"api.github.com"`
  (also relevant for `github.com:443` — one handle covers both if the binding is scoped to
  `api.github.com`; the operator can add a second entry for `github.com` if git push uses
  the raw hostname).
- Resolves both to handles (stored in-memory on the `VaultSecretSource` struct).
- `ProviderToken()` returns the raw env values (unchanged; provider token deferred).
- `PublisherTokens()` returns `("", "")` — tokens are in vault; the host-side publisher
  still reads from `Config.GitToken`/`Config.GitHubToken` (see Host-publisher note below).
- `Handles() []string` — returns the resolved handles for passing into `RunRequest.wiring`.

**`sandbox.Request.Wiring` (new field on the typed seam):**
```go
type RunWiring struct {
    VaultSocket   string
    SecretRefs    []string
    InjectionMode string
}
type Request struct {
    Command  []string
    Worktree string
    Tier     string
    Limits   Limits
    Wiring   RunWiring   // new field; zero value = empty wiring (ADR 035 behavior)
}
```
The exec-sandbox adapter (`internal/sandbox/execsandbox/run.go`) maps `Request.Wiring`
to `RunRequest.wiring` directly. Zero-value `RunWiring` produces the empty wiring that
ADR 035 specified as the deferred default.

**`internal/runtime/run.go`** extended:
When `AGENT_BUILDER_VAULT_BIN` is set:
1. Start vault daemon (lifecycle.go).
2. Construct `VaultSecretSource` (resolve handles for git/GitHub tokens).
3. Pass handles + socket path to the exec-sandbox runner via `Request.Wiring`.
4. Stop vault daemon after the run (in a deferred call).
When `AGENT_BUILDER_VAULT_BIN` is unset: vault is disabled; the old behavior
(env-forwarding of git/GitHub tokens) is preserved for backward compatibility.

### Host-publisher note

The publisher subprocess runs on the trusted host outside the execution box. It reads
`GIT_TOKEN` / `GH_TOKEN` / `GITHUB_TOKEN` from the host-side `Config.GitToken` /
`Config.GitHubToken`. These are populated from the host env, NOT from vault.
The publisher path is NOT changed by this task — it continues to use raw tokens.
Only the IN-BOX path (git/gh commands inside exec-sandbox) is brokered through vault.
This is appropriate: the publisher runs on the host (trusted), not in the sandbox.

### New configuration env vars

| Env var | Description |
|---------|-------------|
| `AGENT_BUILDER_VAULT_BIN` | Path to the vault binary. If unset, vault is disabled (old behavior). |
| `AGENT_BUILDER_VAULT_SOCKET` | Unix socket path for the vault daemon (default: `/tmp/agent-builder-vault-<pid>.sock`). |
| `AGENT_BUILDER_VAULT_STORE_PATH` | Optional path for vault's persistent encrypted store. Unset = in-memory only. |
| `VAULT_MASTER_KEY` | 32-byte hex-encoded master key (required when vault is enabled and `VAULT_MASTER_KEY_FILE` is unset). |
| `VAULT_MASTER_KEY_FILE` | Path to a file containing the master key (takes precedence over `VAULT_MASTER_KEY` when set). |

## Requirements

| Req ID     | Description                                                                                                                                                                                                        | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-066-01 | `internal/vault/client.go` implements `Ping`, `Put`, `Resolve` against the vault Unix socket protocol. `Put` sends `op:"put"` with `secret_ref`, `value`, `injection_floor`, and `binding`. `Resolve` sends `op:"resolve"` with `secret_ref` and `ttl`, returning `{handle, ttl, injection_mode}`. Neither `Put` nor `Resolve` logs or surfaces the secret value in errors. | must have |
| REQ-066-02 | `internal/vault/lifecycle.go` implements `VaultDaemon.Start(ctx)` and `Stop()`. `Start` execs `vault serve --socket <path>` and waits (up to 5 seconds) for `Ping` to succeed. Missing binary or non-executable path fails loud before exec. `Stop` kills the subprocess and removes the socket file. | must have |
| REQ-066-03 | `internal/sandbox/execsandbox` adapter maps `sandbox.Request.Wiring.{VaultSocket, SecretRefs, InjectionMode}` to `RunRequest.wiring.{vault_socket, secret_refs, injection_mode}`. Zero-value `RunWiring` produces the ADR 035 empty-fields behavior unchanged (no regression). | must have |
| REQ-066-04 | `sandbox.Request` gains a `Wiring sandbox.RunWiring` field. `sandbox.Runner` interface is unchanged. `go build ./...` exits 0. | must have |
| REQ-066-05 | L6 live: when vault is configured, the RunRequest JSON written to exec-sandbox's stdin contains non-empty `wiring.vault_socket`, non-empty `wiring.secret_refs`, and `wiring.injection_mode=="proxy"`. The raw git/GitHub token values do NOT appear anywhere in the RunRequest JSON body. `sandbox_status.secrets_injected` is non-empty after the run. | must have (L6) |
| REQ-066-06 | L6 live Phase-0 capstone passes end-to-end with vault git/GitHub proxy brokering active. Real PR opened and cleaned up on l6. Gate fully green. The raw token does not appear in the run log or run record. | must have (L6) |
| REQ-066-07 | L6 feasibility probe: attempt to authenticate the Claude CLI inside the exec-sandbox box with the provider token absent from the box env and present only on the egress proxy as `Authorization: Bearer`. Record the outcome (PASS/BLOCK) as evidence for the follow-on provider-token brokering task. Task 066 is APPROVED regardless of this outcome as long as REQ-066-05/06 are met. | nice-to-have (L6, feasibility) |
| REQ-066-08 | `make check` exits 0. `docs/spec/configuration.md` documents all five new env vars. `docs/spec/interfaces.md` notes `sandbox.Request.Wiring`. `docs/spec/SPEC.md` invariant 7 (secrets) is updated to reflect git/GitHub token brokering. `TestPhase0EndToEndAcceptance` (fake-provider) passes with vault unconfigured (vault is opt-in). | must have |

## Readiness gate

- [x] Test spec `066-vault-token-brokering-test-spec.md` exists (written first)
- [ ] Task 064 (ADR-036) merged and accepted
- [ ] Task 065 (SecretSource seam) merged and verified
- [ ] vault binary built (`cargo build --release` in `~/Code/Public/vault`)
- [ ] exec-sandbox binary built (`go build -o bin/exec-sandbox ./...` in `~/Code/Public/exec-sandbox`)
- [ ] `VAULT_MASTER_KEY` available in the local `.env` or equivalent

## Acceptance criteria

- [ ] [REQ-066-01] TC-066-01: `VaultClient` put/resolve round-trip against real vault daemon; handles are opaque (no plaintext); error cases (no_such_secret, bad put) handled correctly
- [ ] [REQ-066-02] TC-066-02: `VaultDaemon` starts, becomes reachable via Ping, and stops; missing binary errors loud
- [ ] [REQ-066-03] TC-066-03: `buildRunRequest` populates wiring fields from `Request.Wiring`; zero-value produces empty wiring (regression guard)
- [ ] [REQ-066-04] TC-066-04: `sandbox.Request.Wiring` field compiles; `sandbox.Runner` interface unchanged
- [ ] [REQ-066-05] TC-066-05: L6 live — raw tokens not in RunRequest JSON; secrets_injected non-empty
- [ ] [REQ-066-06] TC-066-06: L6 live Phase-0 capstone passes with vault active; raw tokens not in log/record
- [ ] [REQ-066-07] TC-066-07: L6 feasibility probe outcome recorded (PASS or BLOCK)
- [ ] [REQ-066-08] TC-066-08: `make check` green; 5 new env vars in configuration.md; interfaces.md updated; SPEC.md invariant 7 updated; fake-provider capstone passes without vault

## Verification plan

- **Highest level achievable in-repo (no live binary):** L3 — `make check` green.
  TC-066-03/04 achievable at L5 with stub vault client.
- **L5 with vault binary** (gated on `AGENT_BUILDER_LIVE_VAULT=1`):
  ```
  AGENT_BUILDER_LIVE_VAULT=1 \
  AGENT_BUILDER_VAULT_BIN=$HOME/Code/Public/vault/target/release/vault \
  go test -count=1 -v ./internal/vault/... -run 'TestVaultClientPutResolveRoundTrip|TestVaultDaemonLifecycle'
  ```
- **L6 capstone with vault active:**
  ```
  AGENT_BUILDER_LIVE_E2E=1 \
  AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
  AGENT_BUILDER_PUBLISH_REMOTE=l6 \
  AGENT_BUILDER_EXEC_SANDBOX_BIN=$HOME/Code/Public/exec-sandbox/bin/exec-sandbox \
  AGENT_BUILDER_VAULT_BIN=$HOME/Code/Public/vault/target/release/vault \
  AGENT_BUILDER_VAULT_SOCKET=/tmp/agent-builder-vault.sock \
  AGENT_BUILDER_GIT_TOKEN=<real token> \
  AGENT_BUILDER_GITHUB_TOKEN=<real token> \
  CLAUDE_CODE_OAUTH_TOKEN=<from .env> \
  go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  ```
  Expected: `--- PASS` with real PR opened and cleaned; raw tokens absent from run log.
- **L6 feasibility probe** (see TC-066-07 in the test spec):
  `AGENT_BUILDER_VAULT_PROVIDER_PROBE=1 ...` (operator-run separately, outcome recorded).
- **Runtime observation (L6 gate):** run record `wiring.vault_socket` is non-empty;
  `sandbox_status.secrets_injected` contains entries for the git/GitHub host bindings;
  `strings.Contains(runLog, rawGitToken)` is false.

## Out of scope

- Provider token (`CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`) vault brokering
  (deferred; requires TC-066-07 to prove feasibility first).
- Env-mode injection (exec-sandbox v0 env mode is a STUB; excluded per ADR-036).
- Removing `os.Environ()` forwarding from `internal/sandbox/podman/run.go`.
- Vault HTTP read surface or loopback API.
- Vault persistent store as a default (in-memory only unless `AGENT_BUILDER_VAULT_STORE_PATH` set).
- `wiring.audit_socket` (vault ↔ audit-trail integration is a separate future task).
- policy-engine integration.
- Vault high-availability or multi-process coordination (single daemon per run, ephemeral).

## Dependencies

- Task 064 (ADR-036) — must be merged and human-approved.
- Task 065 (SecretSource seam) — `internal/secrets.SecretSource` interface must exist;
  `VaultSecretSource` implements it in this task.
- vault binary built from `~/Code/Public/vault` (Rust; `cargo build --release`).
- exec-sandbox binary built from `~/Code/Public/exec-sandbox` (tasks 062/063 dependencies
  already met).
