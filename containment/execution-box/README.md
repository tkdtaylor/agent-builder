# Execution-Box Profile

`containment/execution-box/` is the product containment profile for tasks 014 through 016. It launches one repo worktree in a rootless Podman box with a read-only root filesystem, one writable worktree mount, tmpfs scratch, non-root workload execution, dropped workload capabilities, no host home mount, no container-engine socket mount, explicit resource limits, default-deny egress, and selectable OCI runtime tiers.

## Runtime Probe

```bash
containment/execution-box/run.sh --worktree . --probe
```

The probe prints `TC-001` through `TC-005` PASS/FAIL lines from inside the box, a `TC-003` host inspection line, and `TC-016` runtime lines before/inside the container. Under `runsc`, it also builds a trivial Go module and prints `TC-016-GO`. If Podman is not installed, `podman info` fails, or the selected OCI runtime is unavailable for the current user, the launcher exits non-zero and names that blocker; static tests still cover the launcher contract.

Inspect the resolved runtime tier without Podman:

```bash
containment/execution-box/run.sh --print-runtime-plan
containment/execution-box/run.sh --workload dev --print-runtime-plan
containment/execution-box/run.sh --workload agent --runtime runc --print-runtime-plan
```

## Egress Probe

```bash
containment/execution-box/run.sh --worktree . --egress-probe
```

The egress probe starts a temporary Podman pod, starts the trusted egress sidecar first, waits for the sidecar readiness marker, then starts the non-root workload probe. The probe prints `TC-003 PASS` when the configured allowlisted host connects and `TC-004 PASS` when both a non-allowlisted host and a direct-IP target are blocked.

The plain-text allowlist lives at `containment/execution-box/egress.allowlist`. Parse it without Podman:

```bash
containment/execution-box/run.sh --print-egress-plan
```

## Defaults

| Setting | Environment override | Default |
|---------|----------------------|---------|
| Image tag | `EXEC_BOX_IMAGE` | `localhost/agent-builder/execution-box:016` |
| Workload tier | `EXEC_BOX_WORKLOAD` | `agent` |
| OCI runtime override | `EXEC_BOX_RUNTIME` | workload default |
| CPU quota | `EXEC_BOX_CPUS` | `2` |
| Memory quota | `EXEC_BOX_MEMORY` | `2g` |
| PID quota | `EXEC_BOX_PIDS_LIMIT` | `256` |
| Scratch tmpfs size | `EXEC_BOX_SCRATCH_SIZE` | `512m` |
| Shared memory size | `EXEC_BOX_SHM_SIZE` | `64m` |
| Overlay storage size | `EXEC_BOX_STORAGE_SIZE` | `4G` |
| Egress allowlist | `EXEC_BOX_EGRESS_ALLOWLIST` | `containment/execution-box/egress.allowlist` |

Workload runtime defaults: `agent` -> `runsc`, `dev` -> `runc`. Explicit `--runtime runc|runsc|kata` wins over the workload default. Workload capability add-backs: none. Normal Go build, vet, test, lint, and formatting flows should run with `--cap-drop=all`; any future workload add-back needs its own task-level justification. The egress sidecar is the only container granted network-administration authority, and only to install the default-deny filter before the workload starts.
