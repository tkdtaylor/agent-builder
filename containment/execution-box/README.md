# Execution-Box Profile

`containment/execution-box/` is the product containment profile for task 014. It launches one repo worktree in a rootless Podman box with a read-only root filesystem, one writable worktree mount, tmpfs scratch, non-root execution, dropped capabilities, no host home mount, no container-engine socket mount, and explicit resource limits.

## Runtime Probe

```bash
containment/execution-box/run.sh --worktree . --probe
```

The probe prints `TC-001` through `TC-005` PASS/FAIL lines from inside the box and a `TC-003` host inspection line before the container starts. If Podman is not installed or `podman info` fails for the current user, the launcher exits non-zero and names that blocker; static tests still cover the launcher contract.

## Defaults

| Setting | Environment override | Default |
|---------|----------------------|---------|
| Image tag | `EXEC_BOX_IMAGE` | `localhost/agent-builder/execution-box:014` |
| CPU quota | `EXEC_BOX_CPUS` | `2` |
| Memory quota | `EXEC_BOX_MEMORY` | `2g` |
| PID quota | `EXEC_BOX_PIDS_LIMIT` | `256` |
| Scratch tmpfs size | `EXEC_BOX_SCRATCH_SIZE` | `512m` |
| Shared memory size | `EXEC_BOX_SHM_SIZE` | `64m` |
| Overlay storage size | `EXEC_BOX_STORAGE_SIZE` | `4G` |

Capability add-backs: none. Normal Go build, vet, test, lint, and formatting flows should run with `--cap-drop=all`; any future add-back needs its own task-level justification.
