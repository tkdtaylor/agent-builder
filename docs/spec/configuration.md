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

**Hook profile env vars** (consumed by `.claude/scripts/`, not the application itself):
- `CLAUDE_HOOK_PROFILE` — `minimal` / `standard` / `strict` (default `standard`)
- `CLAUDE_DISABLED_HOOKS` — comma-separated list of hook names to disable

---

## Runtime flags

> CLI flags that affect runtime mode rather than acting like commands. List here if [interfaces.md](interfaces.md) doesn't already cover them — avoid duplication. Cross-reference rather than restate.

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
| Ports exposed | none | Task 015 owns egress allowlist behavior; this profile exposes no inbound ports |
| Volumes / mounts | `/work` bind mount from the supplied worktree; `/scratch` tmpfs | Rootfs is read-only; host home and container-engine sockets are not mounted |
| Resource floor (CPU / RAM / disk) | `2` CPU / `2g` memory / `4G` overlay storage by default | PID limit `256`, shared memory `64m`, scratch tmpfs `512m` |
| Runtime user/caps | current non-root host uid/gid through `--userns=keep-id`; `--cap-drop=all` | No default capability add-backs |

---

## Defaults policy

Defaults are safe and bounded. The execution-box profile starts from read-only, non-root, no-new-privileges, dropped capabilities, no host-home or container-engine socket mounts, and explicit resource quotas; overrides may tune quota sizes but must not weaken those containment guarantees without an ADR.
