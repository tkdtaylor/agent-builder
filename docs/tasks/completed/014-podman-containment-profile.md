# Task 014: Podman containment profile (execution box)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed

## Goal
Define the rootless-Podman execution-box profile — a product artifact under a named dir (`containment/`) — that runs agent code with a read-only rootfs, a writable repo worktree + tmpfs scratch, no host home, no container socket, non-root + dropped capabilities, and resource quotas.

## Context
- Tech stack: Go 1.26; rootless Podman; OCI runtimes (runc / runsc / kata)
- Authoritative design: `autonomous-builder.md` — §3 (containment skeleton), §4 (substrate)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.3)
- Related ADRs: ADR required — rootless-Podman substrate + execution-box profile decision
- Dependencies: 001
- This box IS exec-sandbox v0 Tier 1 minus orchestration. The profile is a PRODUCT artifact (the execution-box profile), under `containment/` — it must NOT trip fitness F-001 (no-docker, task 008), which carves out named product container dirs.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Read-only rootfs; the only writable mounts are the repo worktree and a tmpfs scratch area | must have |
| REQ-002 | No podman/docker socket mounted and no host-home mount; process runs non-root with all caps dropped, re-adding only what builds require | must have |
| REQ-003 | CPU, memory, PID, and disk quotas applied to the box | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 001

## Acceptance criteria
- [x] [REQ-001] Launching the box yields a read-only `/`; writes to `/` fail, while the mounted worktree and tmpfs scratch accept writes
- [x] [REQ-002] No `/var/run/*podman*.sock` (or docker socket) is present in-box, no host home is mounted, `id` reports non-root, and the capability set is the dropped/minimal set
- [x] [REQ-003] The box is constrained by explicit cpu/mem/pids/disk limits that are observable from the host (cgroup config) and enforced

## Verification plan
- **Highest level achievable:** L6 — containment is an observed runtime property; assert via in-box probes against a launched box.
- In-box probes and observable results to quote: write to `/` denied (read-only rootfs); `id` shows non-root uid/gid; `ls /var/run` / socket check shows no `*podman*.sock` or docker socket; writes to the worktree mount and tmpfs scratch succeed; host-side cgroup shows cpu/mem/pids/disk limits.
- **Executor runtime result:** L6 not reached in this environment. `containment/execution-box/run.sh --worktree . --probe` exited with `execution-box: podman unavailable on PATH`.
- **Cross-module state risk:** none — profile is self-contained; no shared mutable state with other modules.
- **Runtime-visible surface:** container filesystem permissions, user/capability set, mounted device/socket inventory, cgroup resource limits.

## Out of scope
- Egress allowlist / network posture (task 015)
- Tiered OCI runtime selection (task 016)
- Supervisor / orchestration wiring (task 017)

## Notes
- Substrate is rootless Podman, never Docker. Container definitions in this repo are product artifacts under `containment/`, not a generic dev container.
- Cap-add list must be the minimal set the Go build genuinely needs — start from drop-all and justify every add-back in the ADR.
