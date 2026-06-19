# Test spec — Task 066: vault token brokering via proxy mode

**Linked task:** `docs/tasks/backlog/066-vault-token-brokering.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task wires the vault block into agent-builder's exec-sandbox adapter so that git and
GitHub tokens are brokered through vault's proxy injection rather than forwarded raw into
the execution box. It is the first concrete step toward retiring the "accepted
token-in-box risk" named in CLAUDE.md and documented in ADR-036.

**Starting scope: git/GitHub tokens only.**
The Claude provider token (`CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`) targeting
`api.anthropic.com` is explicitly deferred. The proxy-mode feasibility for the provider
token is the critical open risk: the Claude CLI must authenticate with the token absent
from its env and present only on the egress proxy as an `Authorization: Bearer` header.
This has not been proven. The git/GitHub proxy path is a cleaner first win — the tokens
target allowlisted hosts (`api.github.com:443`, `github.com:443`) that are already in the
egress allowlist, and the header binding is identical (`Authorization: Bearer`).

**Vault socket protocol (newline-delimited JSON over a 0600 Unix socket):**

Admin path (called by agent-builder before launching the box):
```json
// put: store a secret
{"op":"put","secret_ref":"vault://agent-builder/git-token","value":"<plaintext>","injection_floor":"proxy","binding":{"host":"api.github.com","header":"Authorization","scheme":"Bearer","env_var":"GIT_TOKEN"}}
// response: {"ok":true} or {"error":{"code":"...","message":"...","retryable":false}}

// resolve: get an opaque handle (never the value)
{"op":"resolve","secret_ref":"vault://agent-builder/git-token","ttl":300}
// response: {"handle":"<opaque>","ttl":300,"injection_mode":"proxy"}
```

Injection path (called by exec-sandbox at spawn time, NOT by agent-builder):
```json
{"op":"inject","handle":"<opaque>","sandbox_identity":{"sandbox_id":"sbx-xxx","attestation":"..."},"mode":"proxy"}
// response: {"ok":true,"delivery":"proxy","credential":"<plaintext>","binding":{...}}
```

**Components introduced by this task:**

1. `internal/vault/client.go` — a minimal Go client for the vault socket:
   `VaultClient{socketPath string}` with methods `Put(...)`, `Resolve(...)`, and a
   `Ping()` for liveness. No `Inject` method — that is called by exec-sandbox, not
   agent-builder.

2. `internal/vault/lifecycle.go` — starts/stops a vault daemon subprocess:
   `VaultDaemon{binPath, socketPath, storePath string}` with `Start(ctx)` / `Stop()`.
   Master key sourced from `VAULT_MASTER_KEY` env var or file at `VAULT_MASTER_KEY_FILE`.

3. `internal/secrets/vault_source.go` — `VaultSecretSource implements SecretSource`
   (task 065 interface). On construction it puts the git/GitHub tokens into vault via
   `VaultClient.Put` and resolves them to handles. `ProviderToken()` returns the raw
   env values (unchanged — provider token brokering is deferred). `PublisherTokens()`
   returns `("", "")` — the tokens have been registered with vault; the handles are
   returned to the caller separately.

4. `internal/runtime/run.go` extended: when vault is configured, resolve handles and
   populate `RunRequest.wiring.vault_socket` and `RunRequest.run.secret_refs` via the
   exec-sandbox adapter.

**What changes in the RunRequest (the exec-sandbox block's stdin contract):**
```json
{
  "wiring": {
    "vault_socket": "/run/agent-builder/vault.sock",
    "injection_mode": "proxy",
    "secret_refs": ["<handle-for-git-token>", "<handle-for-github-token>"]
  }
}
```
exec-sandbox's `Run()` calls `vault.inject` with each handle at spawn time; the proxy
receives the credential; the sandbox never sees the plaintext.

**PROXY-MODE FEASIBILITY RISK (git/GitHub tokens):**
The git/GitHub tokens reach the publisher subprocess (running on the host, outside the
box) via `GIT_TOKEN` / `GH_TOKEN` / `GITHUB_TOKEN` env vars set by
`internal/publisher/publisher.go`. These tokens are NOT used inside the box — they are
used by the host-side `git push` and `gh pr create` commands. Vault proxy brokering
operates inside the box. Therefore the git/GitHub tokens have TWO paths:
- **In-box path** (if any in-box git/gh commands run): proxy brokering applies.
- **Host-side publisher path**: publisher reads `Config.GitToken` / `Config.GitHubToken`
  and passes them to subprocess env. This path is NOT changed by this task — the
  publisher continues to read tokens from `SecretSource.PublisherTokens()` on the host.

This means in-box git/gh commands will be brokered through vault; the host publisher
is unchanged. This is acceptable for v0: the publisher runs on the trusted host outside
the box, so it does not have the same token-in-box risk profile.

**PROXY-MODE FEASIBILITY RISK (provider token — explicitly deferred):**
The Claude CLI in-box must authenticate with the provider token. Today it receives it
via the `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` subprocess env var (set by
`claudeEnv()` in `internal/executor/claude_cli.go`). If vault proxy brokering were
applied, the token would need to be absent from the box env and present only on the
proxy as `Authorization: Bearer` on calls to `api.anthropic.com:443`. It is UNPROVEN
whether the Claude CLI can authenticate via a proxy-injected Authorization header with
the token absent from its own env. TC-066-07 (L6) specifically tests this feasibility
question. If TC-066-07 shows the Claude CLI cannot authenticate via proxy alone, the
provider token remains forwarded directly (the status quo). Git/GitHub proxy brokering
(TC-066-05, TC-066-06) is NOT gated on TC-066-07 and ships regardless of its outcome.

## Requirements coverage

| Req ID     | Test cases                       | Covered? |
|------------|----------------------------------|----------|
| REQ-066-01 | TC-066-01                        | yes      |
| REQ-066-02 | TC-066-02                        | yes      |
| REQ-066-03 | TC-066-03                        | yes      |
| REQ-066-04 | TC-066-04                        | yes      |
| REQ-066-05 | TC-066-05 (L6)                   | yes      |
| REQ-066-06 | TC-066-06 (L6)                   | yes      |
| REQ-066-07 | TC-066-07 (L6 feasibility probe) | yes      |
| REQ-066-08 | TC-066-08                        | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-066-01 — `VaultClient` put/resolve round-trip against a real vault daemon

- **Requirement:** REQ-066-01
- **Level:** L5 (integration against a real vault daemon started in the test — requires
  `vault` binary built from `~/Code/Public/vault`)
- **Test file:** `internal/vault/client_test.go`
- **Test name:** `TestVaultClientPutResolveRoundTrip`
- **Gate flag:** `AGENT_BUILDER_LIVE_VAULT=1`; skip with `t.Skip` if unset.

**Setup:**
1. Start a vault daemon on a temp socket:
   `vault serve --socket <tempdir>/vault.sock` (no `--store-path` → in-memory only;
   `VAULT_MASTER_KEY` must be set to any 32-byte hex value for the ephemeral key).
2. Construct `VaultClient{socketPath: <tempdir>/vault.sock}`.
3. Call `client.Ping()` — assert `ok == true`.

**Sub-case A — put git token:**
- Input: `Put("vault://agent-builder/git-token", "gittok-123", "proxy", Binding{Host:"api.github.com", Header:"Authorization", Scheme:"Bearer", EnvVar:"GIT_TOKEN"})`
- Expected: no error; response contains `ok == true`.

**Sub-case B — resolve returns opaque handle:**
- Input: `Resolve("vault://agent-builder/git-token", 300)`
- Expected:
  - No error.
  - `result.Handle` is non-empty (the opaque token reference).
  - `result.Handle` does NOT contain the string `"gittok-123"` (never returns plaintext).
  - `result.InjectionMode == "proxy"`.
  - `result.TTL == 300`.

**Sub-case C — put for an unknown secret_ref (no prior put) → resolve fails:**
- Input: `Resolve("vault://agent-builder/no-such-ref", 300)`
- Expected: error or response with `error.code == "no_such_secret"`.

**Sub-case D — put with env injection_floor → resolve returns injection_mode "env":**
- Input: `Put(...)` with `injection_floor:"env"`, then `Resolve(...)`.
- Expected: `result.InjectionMode == "env"`.

---

### TC-066-02 — `VaultDaemon` starts, becomes reachable, and stops cleanly

- **Requirement:** REQ-066-02
- **Level:** L5 (subprocess test)
- **Test file:** `internal/vault/lifecycle_test.go`
- **Test name:** `TestVaultDaemonLifecycle`
- **Gate flag:** `AGENT_BUILDER_LIVE_VAULT=1`

**Assertions:**
- `daemon.Start(ctx)` with a valid vault binary and temp socket path does not error.
- Within 3 seconds, `VaultClient.Ping()` on the socket returns `ok == true`.
- `daemon.Stop()` returns with no error and the socket file is gone (or the daemon
  process exits within 2 seconds).

**Edge cases:**
- Missing vault binary path → `Start()` returns a non-nil error naming the missing binary.
- Binary path points to a non-executable file → `Start()` returns a non-nil error.
- Second `Start()` call on a running daemon → `Start()` returns a non-nil error (already started).

---

### TC-066-03 — `buildRunRequest` populates `vault_socket`, `injection_mode="proxy"`, and `secret_refs` when vault config is set

- **Requirement:** REQ-066-03
- **Level:** L5 (unit test using a stub vault client and stub binary)
- **Test file:** `internal/sandbox/execsandbox/run_test.go`
- **Test name:** `TestBuildRunRequestWithVaultWiring`

**Input:** `sandbox.Request` with:
- `Wiring.VaultSocket = "/tmp/vault.sock"` (new field on `sandbox.Request`)
- `Wiring.SecretRefs = []string{"handle-abc", "handle-xyz"}`
- `Wiring.InjectionMode = "proxy"`

**Expected (from stub binary's recorded stdin):**
- `wiring.vault_socket == "/tmp/vault.sock"` (non-empty).
- `wiring.injection_mode == "proxy"`.
- `wiring.secret_refs` is `["handle-abc","handle-xyz"]`.
- All existing fields (origin_map, profile capabilities, PATH, FileRead) are unchanged.

**Regression guard (no vault config):**
- `sandbox.Request` with no `Wiring` fields set → `wiring.vault_socket == ""`,
  `wiring.injection_mode == ""`, `wiring.secret_refs == []` (the empty-fields behavior
  from ADR 035 is preserved exactly).

---

### TC-066-04 — `sandbox.Request` has a `Wiring` field; `sandbox.Limits` and `sandbox.Request` shapes are updated

- **Requirement:** REQ-066-04
- **Level:** L2 (compile-time)
- **Test file:** `internal/sandbox/sandbox_test.go` (new compile-time assertion)

**Assertions:**
- `sandbox.Request` has a field `Wiring sandbox.RunWiring` (or equivalent name).
- `sandbox.RunWiring` has fields `VaultSocket string`, `SecretRefs []string`,
  `InjectionMode string`.
- The existing `sandbox.Runner` interface `Run(Request) (Result, int, error)` is unchanged.
- A compile-time value construction `sandbox.Request{Wiring: sandbox.RunWiring{VaultSocket: "x"}}` compiles.
- `go build ./...` exits 0.

---

### TC-066-05 — L6 live: git/GitHub tokens brokered through vault proxy; no raw token in RunRequest

- **Requirement:** REQ-066-05
- **Level:** L6 (live, operator-observed; gated on `AGENT_BUILDER_LIVE_VAULT=1` and
  `AGENT_BUILDER_LIVE_EXEC_SANDBOX=1`)
- **Test file:** `internal/vault/integration_test.go` or `tests/e2e/vault_e2e_test.go`
- **Test name:** `TestVaultGitHubTokenProxyRoundTrip`

**Setup:**
1. Start real vault daemon on a temp socket.
2. Put `AGENT_BUILDER_GIT_TOKEN` value into vault under `vault://agent-builder/git-token`
   with `injection_floor:"proxy"` and `binding.host:"api.github.com"`.
3. Resolve to get an opaque handle.
4. Construct a `sandbox.Request` with the vault socket, handle in `secret_refs`, and
   `injection_mode:"proxy"`.
5. Run a payload `["sh", "-c", "echo token-check-placeholder"]` through the real
   exec-sandbox binary.

**Assertions:**
- The RunRequest JSON written to the real exec-sandbox binary's stdin has:
  - `wiring.vault_socket` set to the real socket path.
  - `wiring.secret_refs` containing the resolved handle.
  - `wiring.injection_mode == "proxy"`.
  - The raw token value `AGENT_BUILDER_GIT_TOKEN`'s plaintext does NOT appear
    anywhere in the serialized RunRequest JSON (token never sent in request body).
- The exec-sandbox Run() call returns no error.
- `sandbox_status.secrets_injected` is non-empty (vault injected at spawn time).

---

### TC-066-06 — L6 live Phase-0 capstone passes with vault git/GitHub proxy brokering active

- **Requirement:** REQ-066-06
- **Level:** L6 (operator-observed; gated on `AGENT_BUILDER_LIVE_E2E=1` and vault flags)
- **Test name:** `TestLivePhase0EndToEndAcceptance_TC032` (existing test)

**Harness command:**
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

**Expected:**
- `--- PASS: TestLivePhase0EndToEndAcceptance_TC032`
- In-box gate fully green (all 7 steps).
- Real PR opened and cleaned up on l6.
- Run record shows `vault_socket` was set (non-empty in logged wiring fields).
- The raw `AGENT_BUILDER_GIT_TOKEN` / `AGENT_BUILDER_GITHUB_TOKEN` values do NOT appear
  in the run log, run record, or audit chain.
- Note: stays `pending` until the live run is executed.

---

### TC-066-07 — L6 feasibility probe: Claude CLI authenticates via proxy-injected token only (no env)

- **Requirement:** REQ-066-07
- **Level:** L6 (operator-observed feasibility probe; NOT a regression gate — outcome
  determines whether the provider token is brokered in a follow-on task)
- **Test file:** `internal/vault/provider_proxy_probe_test.go`
- **Test name:** `TestProviderTokenProxyFeasibility`
- **Gate flag:** `AGENT_BUILDER_VAULT_PROVIDER_PROBE=1`

**Setup:**
1. Start real vault daemon on a temp socket.
2. Put `CLAUDE_CODE_OAUTH_TOKEN` into vault under `vault://agent-builder/claude-oauth`
   with `injection_floor:"proxy"` and
   `binding:{host:"api.anthropic.com", header:"Authorization", scheme:"Bearer", env_var:"CLAUDE_CODE_OAUTH_TOKEN"}`.
3. Resolve to get an opaque handle.
4. Run the exec-sandbox binary with a payload that invokes the Claude CLI:
   `claude -p "Reply with exactly the word: PROXY_OK"` — BUT with
   `CLAUDE_CODE_OAUTH_TOKEN` and `ANTHROPIC_API_KEY` both ABSENT from the sandbox env
   (`run.env` does not include them) and vault handle in `secret_refs`.
5. The exec-sandbox proxy receives `vault.inject` at spawn time and sets the
   `Authorization: Bearer <token>` header on all requests to `api.anthropic.com:443`.

**Expected (success path):**
- `Result.Stdout` contains `"PROXY_OK"`.
- `Result.ExitCode == 0`.
- `sandbox_status.secrets_injected` contains an entry for `api.anthropic.com`.
- RECORD the outcome in the Verified-by column as
  "TC-066-07 L6 PASS: Claude CLI authenticated via proxy-injected OAuth token with token
  absent from sandbox env" — this UNLOCKS the follow-on provider-token brokering task.

**Expected (failure path — equally valid outcome for this task):**
- `Result.ExitCode != 0` or `Result.Stdout` does not contain `"PROXY_OK"`.
- `Result.Stderr` or stdout contains an authentication error (e.g. "Not logged in",
  "Invalid credentials", or "401").
- RECORD the outcome as
  "TC-066-07 L6 BLOCKED: Claude CLI cannot authenticate via proxy-injected token;
  provider token brokering deferred" — the git/GitHub brokering (TC-066-05/06) is
  still PASS; only the provider-token follow-on task is blocked.

**Note:** this is an explicit feasibility probe, not a regression gate. Task 066 is
APPROVED regardless of TC-066-07's outcome, as long as TC-066-05/06 pass. TC-066-07's
result is recorded as evidence for the follow-on scoping decision.

---

### TC-066-08 — `make check` green; `docs/spec/` updated; no raw token in RunRequest on capstone path

- **Requirement:** REQ-066-08
- **Level:** L3 / L5
- **Test file:** `Makefile` + `go test ./...`

**Assertions:**
- `go test ./...` exits 0 with all tests passing.
- `make fitness` exits 0 (all existing fitness checks pass; no new fitness failures).
- `make check` → `All checks passed.`
- `docs/spec/configuration.md` documents the new env vars:
  `AGENT_BUILDER_VAULT_BIN` (path to vault binary),
  `AGENT_BUILDER_VAULT_SOCKET` (socket path),
  `AGENT_BUILDER_VAULT_STORE_PATH` (optional persistent store),
  `VAULT_MASTER_KEY` / `VAULT_MASTER_KEY_FILE` (master key sourcing).
- `docs/spec/interfaces.md` notes that `sandbox.Request.Wiring` is now populated with
  vault handles when vault is configured, and that `wiring.vault_socket`,
  `wiring.secret_refs`, and `wiring.injection_mode` are no longer empty in vault-enabled runs.
- `docs/spec/SPEC.md` invariant 7 (secrets section) is updated to reflect the new
  brokered state: git/GitHub tokens are now brokered through vault in proxy mode (no
  longer raw-forwarded into the box).
- The existing Phase-0 fake-provider acceptance test `TestPhase0EndToEndAcceptance`
  passes without vault configured (vault is opt-in; when `AGENT_BUILDER_VAULT_BIN` is
  unset, the run proceeds with the old env-forwarding behavior unchanged).

---

## Verification plan

- **Highest level achievable in-repo (no live binary):** L3 — `make check` green.
  TC-066-03 and TC-066-04 are achievable at L5 (unit tests with stub vault client
  returning a canned handle + stub exec-sandbox binary recording the RunRequest JSON).
- **L5 with vault binary:** TC-066-01/02 require the vault binary built from
  `~/Code/Public/vault`. These are gated on `AGENT_BUILDER_LIVE_VAULT=1`.
- **L6 (operator-observed, critical):** TC-066-05 (git/GitHub tokens NOT in RunRequest
  body) + TC-066-06 (capstone passes) together constitute the primary L6 bar. TC-066-07
  (provider token feasibility) is a bonus probe — its outcome shapes the follow-on task.
- **L5 harness command (no vault binary, no live exec-sandbox):**
  ```
  go test -count=1 ./internal/vault/... ./internal/secrets/... ./internal/sandbox/execsandbox/...
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
- **L5 with vault binary:**
  ```
  AGENT_BUILDER_LIVE_VAULT=1 \
  AGENT_BUILDER_VAULT_BIN=$HOME/Code/Public/vault/target/release/vault \
  go test -count=1 -v ./internal/vault/... -run 'TestVaultClientPutResolveRoundTrip|TestVaultDaemonLifecycle'
  ```
- **L6 capstone command:** (see TC-066-06 above)
- **Proxy feasibility probe:** (see TC-066-07 above)

## Out of scope

- Provider token (`CLAUDE_CODE_OAUTH_TOKEN` / `ANTHROPIC_API_KEY`) vault brokering
  (deferred pending TC-066-07 feasibility result).
- Env-mode injection (exec-sandbox v0 env mode is a STUB — explicitly excluded per ADR-036).
- Removing `os.Environ()` forwarding from `internal/sandbox/podman/run.go` (Podman
  backend remains unchanged).
- Vault HTTP read surface (not used by agent-builder).
- Vault persistent store in production (in-memory is fine for v0; `--store-path` is
  optional and operator-configured via `AGENT_BUILDER_VAULT_STORE_PATH`).
- policy-engine or audit-trail integration with vault (out of scope for v0 brokering).
- `wiring.audit_socket` (vault's audit integration with agent-builder's audit-trail block
  is a separate, later task).
