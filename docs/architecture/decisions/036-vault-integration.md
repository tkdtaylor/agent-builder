# ADR 036 — Adopt the vault block as agent-builder's token broker

**Status:** Accepted (implemented — tasks 064–066, all ✅ verified)
**Date:** 2026-06-19
**Preceded by:** ADR 035 (adopt exec-sandbox block as default run backend — deferred vault wiring)

## Context

CLAUDE.md names "the egress allowlist is the load-bearing control for the accepted
token-in-box risk." That risk is concrete today: agent-builder's executor and publisher
forward four live secrets directly into subprocess environments:

- `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` — injected by `executor.ClaudeCLI` into
  the Claude subprocess env (ADR 033).
- `AGENT_BUILDER_GIT_TOKEN` / `AGENT_BUILDER_GITHUB_TOKEN` — injected by
  `publisher.GitHubCLI` into git and gh subprocess envs.

These secrets are independently revocable and never logged (ADR 033; `docs/spec/configuration.md`
Secrets section), but they are present in plaintext inside the subprocess environment on every
run. A compromised workload, a prompt-injection that tricks the in-box agent into `env | curl`,
or a dep-scan miss on a supply-chain attack can exfiltrate them.

The vault block (`~/Code/Public/vault`) is now complete. It is a standalone Rust daemon that
stores secrets at startup time, provides opaque handles to callers, and delivers plaintext
only at the injection edge inside the sandbox. The exec-sandbox block (`~/Code/Public/exec-sandbox`)
already has vault integration hooks: `RunRequest.wiring.vault_socket`,
`RunRequest.run.secret_refs`, and `RunRequest.wiring.injection_mode`. Per ADR 035, agent-builder
sends all three fields empty. This ADR decides how and in what order to populate them.

### Exec-sandbox v0 injection mode constraint

exec-sandbox v0 ships two injection modes:

- **proxy mode** (`injection_mode="proxy"`) — the egress proxy attaches `<scheme> <cred>`
  to the configured header (e.g. `Authorization: Bearer <token>`) on allowlisted hosts.
  This is **fully wired** in exec-sandbox v0.
- **env mode** (`injection_mode="env"`) — the resolved secret value is injected as an
  environment variable inside the sandbox. This is a **STUB** in exec-sandbox v0: the field
  is recorded in the RunRequest JSON but the value is NOT loaded into the sandbox env. Any
  integration targeting exec-sandbox v0 MUST use proxy mode; env mode is not available as a
  delivery mechanism.

This constraint is not a policy choice; it is a hard capability boundary. The implementation
tasks (065, 066) must target proxy mode exclusively until exec-sandbox promotes the env-mode
stub to a real implementation.

### Roadmap sequencing

The original roadmap sequences vault after audit-trail and policy-engine. This decision
pulls vault forward because:

1. The token-in-box risk is the most concrete and present security gap agent-builder faces.
2. The vault block is complete and the exec-sandbox proxy-mode injection path is already wired.
3. The implementation tasks (065, 066) are narrowly scoped: refactor token sourcing to a
   `SecretSource` interface (065) and wire the vault client into the proxy-mode injection path
   (066). Neither task modifies the agent loop, Gate, or publisher semantics.

## Decision

**Adopt the vault block as the token broker for agent-builder's git/GitHub tokens via
exec-sandbox's egress proxy (`injection_mode="proxy"`), starting with `AGENT_BUILDER_GIT_TOKEN`
and `AGENT_BUILDER_GITHUB_TOKEN`. The Claude provider token is deferred pending resolution of
the proxy feasibility risk below.**

### Vault socket protocol

The vault block exposes a Unix domain socket. The three verbs agent-builder uses are:

| Verb      | Direction            | Description |
|-----------|----------------------|-------------|
| `put`     | agent-builder → vault | Store a secret value; vault returns an opaque handle string. Called once at startup per secret. |
| `resolve` | agent-builder → vault | Exchange a handle for the opaque token that exec-sandbox can pass to the injection edge. Returns an opaque token (not the plaintext secret value). Called when building a RunRequest. |
| `inject`  | vault → sandbox      | Vault delivers the plaintext secret to the egress proxy at injection time. This verb fires inside exec-sandbox's injection path — agent-builder does not call it directly. |

agent-builder holds handles, not values, after the `put` call. The plaintext secret is never
present in agent-builder's memory beyond the initial `put`. Handles are safe to log.

### Binding shape

A `Binding` controls which header the egress proxy injects the resolved secret on, for which
allowlisted host:

```
Binding {
    handle:  string       // opaque handle returned by vault put
    host:    string       // allowlisted host (e.g. "api.github.com")
    port:    uint16       // TCP port (e.g. 443)
    header:  string       // HTTP header name (e.g. "Authorization")
    scheme:  string       // header value prefix (e.g. "Bearer")
}
```

The exec-sandbox proxy injects `<scheme> <resolved-value>` as the named header on every
allowlisted request to `host:port`. Multiple bindings may name the same host with different
headers.

### RunRequest wiring fields

Two fields on `RunRequest` carry the vault wiring from agent-builder to exec-sandbox:

| Field                       | Type            | Purpose |
|-----------------------------|-----------------|---------|
| `wiring.vault_socket`       | string (path)   | Unix socket path for the running vault daemon. exec-sandbox connects here at injection time. |
| `run.secret_refs`           | `[]Binding`     | Ordered list of Bindings; each carries the opaque handle and the host/header/scheme for the proxy injection. |

`injection_mode` is set to `"proxy"` when vault wiring is active; the field remains `""` when
vault is not configured (current behavior per ADR 035).

### Opaque handle model

agent-builder's wiring code holds handles (opaque strings), not secret values, after the
initial `put`. The flow is:

```
agent-builder startup:
  vault.put(AGENT_BUILDER_GIT_TOKEN value)    → git_handle
  vault.put(AGENT_BUILDER_GITHUB_TOKEN value) → github_handle

per RunRequest:
  RunRequest.wiring.vault_socket = vault_socket_path
  RunRequest.run.secret_refs     = [
    Binding{handle: git_handle,    host: "github.com",     port: 443, header: "Authorization", scheme: "Bearer"},
    Binding{handle: github_handle, host: "api.github.com", port: 443, header: "Authorization", scheme: "Bearer"},
  ]
  RunRequest.wiring.injection_mode = "proxy"

at exec-sandbox egress proxy (inside sandbox):
  vault.inject(git_handle)    → git token plaintext → injected as "Authorization: Bearer <value>"
  vault.inject(github_handle) → github token plaintext → injected as "Authorization: Bearer <value>"
```

The git and GitHub token values are never present in the RunRequest JSON or in agent-builder's
logs after the `put` call. Handles are safe to log and trace.

### Starting scope: git/GitHub tokens first

**git/GitHub tokens (`AGENT_BUILDER_GIT_TOKEN`, `AGENT_BUILDER_GITHUB_TOKEN`) are the first
secrets brokered through vault.** Reasons:

1. Their allowlist entries (`api.github.com:443`, `github.com:443`) are already present in the
   exec-sandbox egress allowlist and the exec-sandbox adapter's `EgressAllowlist` mapping.
2. The proxy injection model (`Authorization: Bearer <token>`) is standard for GitHub's REST
   API and git-over-HTTPS — no special client-side handling is required inside the sandbox.
3. The git/GitHub credential path does not depend on the in-box agent CLI authenticating with
   the injected token; the proxy transparently prepends it to outbound requests.

### Claude provider token: feasibility risk, deferred

Brokering the Claude provider token (`ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`) through
vault's proxy mode requires the in-box Claude CLI to authenticate with the token **absent from
the box env** and **present only on the proxy** for `api.anthropic.com:443`. This path is
**unproven** as of this ADR:

- It is not established that the Claude CLI will authenticate via a proxy-injected
  `Authorization` header when neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` is set
  in its environment.
- The Claude CLI may perform credential discovery steps that bypass the egress proxy entirely.

Until this path is verified in task 066's Verification plan (the live in-box Claude CLI
authenticating against `api.anthropic.com:443` with a proxy-injected credential and no env
token), the Claude provider token continues to be forwarded via the existing executor env path
(ADR 033). Brokering the provider token is explicitly deferred as a follow-on once the
git/GitHub proxy path is proven.

### No-unattended-self-modification invariant

The implementation tasks (065, 066) add wiring code to agent-builder's `internal/` packages;
they do not edit agent-builder's own orchestration logic, Gate, loop, or task-source code
autonomously. The invariant from CLAUDE.md — "agent-builder reads from its own repo but never
edits it autonomously" — is preserved: the wiring tasks add new packages and wire them at the
`internal/runtime` assembly point, but no self-editing loop is introduced.

## Consequences

- **Token-in-box risk reduced for git/GitHub tokens** once task 066 is merged and deployed:
  `AGENT_BUILDER_GIT_TOKEN` and `AGENT_BUILDER_GITHUB_TOKEN` no longer travel as plaintext
  subprocess env vars; they are resolved by the vault proxy at the egress edge.
- **`ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` remain in-env** until the provider-token
  proxy path is proven in task 066's Verification plan. The accepted-risk acknowledgment in
  CLAUDE.md is unchanged for those two credentials.
- **One new runtime dependency: vault daemon.** The vault socket path must be discoverable
  (via a new `AGENT_BUILDER_VAULT_SOCKET` env var — `docs/spec/configuration.md` will be
  updated in task 065/066). When vault is not configured, vault wiring is skipped and
  behavior reverts to the current env-forwarding path. Vault absence is not a startup error.
- **`SecretSource` interface added (task 065):** a new internal interface abstracts token
  sourcing — env read (current) vs. vault handle (new). This is a pure refactor; existing
  behavior is unchanged.
- **env mode explicitly deferred:** `injection_mode="env"` integration is not implemented until
  exec-sandbox promotes the env-mode stub to a real implementation. This ADR does not prescribe
  when that happens.
- **Spec files updated by implementation tasks:**
  - `docs/spec/configuration.md` — updated in task 065 with `AGENT_BUILDER_VAULT_SOCKET` and
    in task 066 with any additional vault-client configuration. Not updated in this task.
  - `docs/spec/interfaces.md` — updated in task 065 with the `SecretSource` interface shape
    and in task 066 with the vault-brokering outbound dependency row. Not updated in this task.
