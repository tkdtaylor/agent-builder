# Configuration

**Project:** agent-builder
**Last updated:** 2026-06-05

Every knob the system exposes — env vars, config files, runtime parameters, deployment settings. Each entry is a public contract: changes to defaults or accepted values are observable.

Not in this file:
- What gets configured (the behaviors live in [behaviors.md](behaviors.md))
- How values get into the process (the parsing is in code; the *contract* is here)

---

## Configuration files

### File: `containment/execution-box/Containerfile`

- **Location:** `containment/execution-box/Containerfile`
- **Format:** OCI image build file consumed by Podman
- **Required vs optional:** required for the execution-box launcher
- **Reload behavior:** read when `containment/execution-box/run.sh` builds the local execution-box image

The image supplies the Go toolchain, `/work`, `/scratch`, and the in-box probe binary. Runtime security and quota settings live in the launcher, not in the image file, because they are host/container run arguments.

### File: `containment/execution-box/egress.allowlist`

- **Location:** `containment/execution-box/egress.allowlist`
- **Format:** plain UTF-8 text; one exact hostname plus explicit TCP port per non-comment line, followed by an inline `#` justification comment
- **Required vs optional:** required by the execution-box launcher; override with `EXEC_BOX_EGRESS_ALLOWLIST` or `--egress-allowlist`
- **Reload behavior:** read and validated on every `containment/execution-box/run.sh` invocation before Podman is required

Example:

```text
api.github.com:443 # GitHub API for branch and PR automation
```

Rules:

- Blank lines and lines beginning with `#` are ignored.
- Hostnames are exact matches after lowercase normalization. Wildcards, IP literals, CIDR blocks, URL schemes, paths, and query strings are not accepted by this bootstrap contract.
- Ports are mandatory decimal TCP ports from `1` through `65535`.
- Duplicate `host:port` entries are de-duplicated after parsing.
- Empty allowlist means total egress deny.
- Malformed entries fail closed before Podman starts; the launcher exits non-zero and names the bad line.

The launcher resolves allowlisted hostnames to IPv4 addresses before the workload starts, adds only those host records to the workload container, starts an egress sidecar, waits for the sidecar readiness marker, and only then starts the workload. The sidecar installs an nftables output policy with default drop and explicit allow rules for the resolved allowlisted IP-and-port pairs. The workload container keeps `--cap-drop=all`, `--security-opt=no-new-privileges`, and no `CAP_NET_ADMIN`; network-administration authority is isolated to the sidecar.

---

## Environment variables

> Variables read from the process environment. Distinguish required-at-startup from optional overrides.

| Variable | Type | Default | Required | Effect |
|----------|------|---------|----------|--------|
| `EXEC_BOX_IMAGE` | string | `localhost/agent-builder/execution-box:014` | no | Image tag built and run by the execution-box launcher |
| `EXEC_BOX_CPUS` | number/string accepted by Podman | `2` | no | CPU quota passed as `--cpus` |
| `EXEC_BOX_MEMORY` | size string | `2g` | no | Memory quota passed as `--memory` |
| `EXEC_BOX_PIDS_LIMIT` | integer | `256` | no | PID quota passed as `--pids-limit` |
| `EXEC_BOX_SCRATCH_SIZE` | size string | `512m` | no | Size of tmpfs mounted at `/scratch` |
| `EXEC_BOX_SHM_SIZE` | size string | `64m` | no | Shared-memory size passed as `--shm-size` |
| `EXEC_BOX_STORAGE_SIZE` | size string | `4G` | no | Overlay storage size passed as `--storage-opt size=...` |
| `EXEC_BOX_EGRESS_ALLOWLIST` | path | `containment/execution-box/egress.allowlist` | no | Plain-text egress allowlist consumed by the execution-box launcher |
| `EXEC_BOX_EGRESS_PROBE_ALLOW_HOST` | `host:port` | `api.github.com:443` | no | Allowlisted probe target expected to connect during `--egress-probe` |
| `EXEC_BOX_EGRESS_PROBE_DENY_HOST` | `host:port` | `example.com:443` | no | Non-allowlisted probe target expected to be blocked during `--egress-probe` |
| `EXEC_BOX_EGRESS_PROBE_DENY_IP` | `host:port` IP literal | `1.1.1.1:443` | no | Direct-IP probe target expected to be blocked during `--egress-probe` |

**Hook profile env vars** (consumed by `.claude/scripts/`, not the application itself):
- `CLAUDE_HOOK_PROFILE` — `minimal` / `standard` / `strict` (default `standard`)
- `CLAUDE_DISABLED_HOOKS` — comma-separated list of hook names to disable

---

## Runtime flags

> CLI flags that affect runtime mode rather than acting like commands. List here if [interfaces.md](interfaces.md) doesn't already cover them — avoid duplication. Cross-reference rather than restate.

## Runtime parameters

> Typed values supplied by callers at construction time rather than parsed from environment or CLI flags.

| Parameter | Type | Default | Required | Effect |
|-----------|------|---------|----------|--------|
| `loop.RetryPolicy.MaxAttempts` | non-negative integer | none | yes | Bounds Executor attempts for one picked task. `0` means mark `needs-human` immediately without running Executor or Gate; positive values permit exactly that many attempts before exhausted failures escalate. |

---

## Secrets

> Sensitive configuration that lives **outside** the repo. Never commit values; document only the names and where they come from.

| Secret | Source | Used for |
|--------|--------|----------|
| `ANTHROPIC_API_KEY` | Host environment or sandbox secret store | Claude API access in the executor environment |
| `GIT_TOKEN` | Host environment or sandbox secret store | Pushing commits from the executor environment |
| | | |

**Rule:** secrets are never pasted into the chat, never logged, and never written into the repo. The `protect-secrets` hook blocks writes to common credential filenames.

---

## Deployment configuration

> If the project has a deployment story (rootless Podman execution box, dev shell, sandbox), document the runtime contract here: ports exposed, volumes mounted, image tags, resource expectations.

| Aspect | Value | Notes |
|--------|-------|-------|
| Container image | `localhost/agent-builder/execution-box:014` by default | Built from `containment/execution-box/Containerfile`; override with `EXEC_BOX_IMAGE` |
| Ports exposed | none | The profile exposes no inbound ports and defaults outbound egress to deny |
| Volumes / mounts | `/work` bind mount from the supplied worktree; `/scratch` tmpfs | Rootfs is read-only; host home and container-engine sockets are not mounted |
| Resource floor (CPU / RAM / disk) | `2` CPU / `2g` memory / `4G` overlay storage by default | PID limit `256`, shared memory `64m`, scratch tmpfs `512m` |
| Runtime user/caps | workload: current non-root host uid/gid through `--userns=keep-id`; `--cap-drop=all`; egress sidecar: rootless namespace with `CAP_NET_ADMIN` only | Network administration is isolated to the trusted sidecar; no workload capability add-backs |
| Egress | default-deny; exact host:port allowlist only | Sidecar installs nftables rules before workload start; workload DNS is disabled except launcher-provided host records for allowlisted destinations |

---

## Defaults policy

Defaults are safe and bounded. The execution-box profile starts from read-only, non-root, no-new-privileges, dropped workload capabilities, no host-home or container-engine socket mounts, explicit resource quotas, and default-deny egress; overrides may tune quota sizes or choose a different allowlist file but must not weaken those containment guarantees without an ADR.
