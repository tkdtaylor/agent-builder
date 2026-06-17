# Task 049: egress pod `--userns`/`--pod` conflict (rootless podman)

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix the `--egress-probe` path in `containment/execution-box/run.sh` so the egress pod and its members launch on rootless podman: declare `--userns=keep-id` on `podman pod create` (the pod's infra container owns the user namespace) and remove `--userns` from the pod members (sidecar + egress workload), which inherit it. This unblocks the L6 egress probe (015), which currently dies with `Error: --userns and --pod cannot be set together`. Launch mechanics only — the egress containment posture (keep-id mapping, NET_ADMIN nftables sidecar, default-deny allowlist, read-only/cap-drop/no-new-privileges) is unchanged.

## Context

- Tech stack: bash (`containment/execution-box/run.sh` egress block ~lines 595-655). No Go code.
- **No ADR** — this is a podman-rootless-pods mechanics fix with one correct form (userns owned by the pod infra container in rootless mode; members inherit). Same bug class as tasks 045/047 (podman-portability launch fixes). It does NOT change the egress allowlist policy or any containment control — the egress allowlist is load-bearing, but this task touches only *how the pod members acquire their userns*, not the deny/allow logic.
- Verified live (podman 5.7.0, rootless, ext4): the sidecar `podman run -d --pod $pod --userns=keep-id` (~line 605) triggers `--userns and --pod cannot be set together`; the workload member also inherits `--userns=keep-id` from `common_args` (~line 503) + `--pod`.
- The non-pod paths (probes 014/016/033 via `common_args`, plain workload run) MUST keep `--userns=keep-id` on the container — only the pod (egress) path moves it to pod-create.
- **Model tier: balanced (sonnet)** — a launch-arg restructuring in a security-relevant script; touches the containment profile's egress path.
- Dependencies: builds on 045 (fail-loud) which already wrapped the egress `podman run`; independent of 046/048.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-049-01 | `podman pod create` for the egress pod declares `--userns=keep-id`; the pod's infra container owns the user namespace | must have |
| REQ-049-02 | The egress sidecar (`podman run -d --pod …`) does NOT pass `--userns` (inherits the pod's); its other args (NET_ADMIN, read-only, cap-drop, no-new-privileges, `--user 0:0`, mounts) are unchanged | must have |
| REQ-049-03 | The egress workload/probe member (`podman run … --pod …`) does NOT pass `--userns` (the egress `workload_args` are `common_args` minus `--userns`, plus `--pod`); the non-pod `--probe`/workload paths STILL pass `--userns=keep-id` unchanged | must have |
| REQ-049-04 | Pod members still pass `--user <uid>:<gid>` so the keep-id uid mapping the bind-mounted worktree relies on is preserved | must have |
| REQ-049-05 | A real `--egress-probe` run on this host gets past `podman pod create` + sidecar `podman run -d` without the `--userns and --pod` conflict (pod + sidecar start) | must have |
| REQ-049-06 | `docs/spec/behaviors.md` / `docs/spec/interfaces.md` egress-path description updated in the feat commit IF it specifies the per-container userns (note the rootless pod-level userns); otherwise state no spec change needed | must have |

## Readiness gate

- [x] Test spec `049-egress-pod-userns-conflict-test-spec.md` exists
- [x] All acceptance criteria below have a linked REQ ID
- [ ] No blocking dependencies (045 merged)

## Acceptance criteria

- [ ] [REQ-049-01] stub-podman argv: `podman pod create` contains `--userns=keep-id` (TC-049-01)
- [ ] [REQ-049-02] stub-podman argv: sidecar `run -d --pod` does NOT contain `--userns`; still has NET_ADMIN/read-only/cap-drop/no-new-privileges (TC-049-01)
- [ ] [REQ-049-03] stub-podman argv: egress workload member has `--pod`, no `--userns`; non-pod `--probe` member STILL has `--userns=keep-id` (TC-049-01, TC-049-02)
- [ ] [REQ-049-04] both pod members still pass `--user <uid>:<gid>` (TC-049-03)
- [ ] [REQ-049-05] real-host `--egress-probe` passes pod + sidecar start, no userns/pod conflict (TC-049-04)
- [ ] [REQ-049-06] spec updated if it specified per-container userns, else stated N/A

## Verification plan

- **Highest level achievable:** L6 — real `--egress-probe` no longer hits the userns/pod conflict (pod + sidecar start). Any further rootless-egress behavior is a reported residual.
- **L5 harness:** extend the `containment/execution-box/tests/` stub-podman harness with per-subcommand argv capture (pod create / sidecar / workload member); assert TC-049-01..03 without a live container.
- **L6 evidence:** quote the real `--egress-probe` output showing it gets past pod/sidecar start.
- **Cross-module state risk:** none.
- **Runtime-visible surface:** egress pod/sidecar/workload podman argv + `--egress-probe` outcome.

## Out of scope

- Egress allowlist policy / nftables rules / default-deny / NET_ADMIN — unchanged.
- Tasks 045/046/047/048 scope.
- Any rootless-egress behavior beyond the userns/pod launch conflict (report as a follow-up if the real run surfaces more).

## Notes

- Build the egress `workload_args` as a filtered copy of `common_args` with the `--userns=keep-id` element removed (then append `--pod`, `--dns none`, etc.) — do NOT remove `--userns` from `common_args` itself (the non-pod probe/workload paths need it).
- The fail-loud guards added in task 045 on the egress `podman run`/`pod create` stay — they are what surfaced this conflict as exit 125 rather than a silent pass.
