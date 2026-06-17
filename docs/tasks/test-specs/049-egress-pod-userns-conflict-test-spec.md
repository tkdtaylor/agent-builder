# Test spec — Task 049: egress pod `--userns`/`--pod` conflict (rootless)

**Task:** 049-egress-pod-userns-conflict
**Created:** 2026-06-17

## Context

The `--egress-probe` path (`containment/execution-box/run.sh`) runs an nftables egress sidecar in a podman **pod**, which the workload/probe container then joins. Both the sidecar (`run.sh` ~line 605) and the workload member (via `common_args` `--userns=keep-id`, ~line 503) pass `--userns=keep-id` while joining the pod. Rootless podman 5.7 rejects this:

```
Error: --userns and --pod cannot be set together
execution-box: podman run failed: egress sidecar did not start (exit 125)
```

In rootless podman the user namespace is owned by the **pod's infra container**; member containers inherit it and must NOT set `--userns` themselves. The fix: declare `--userns=keep-id` on `podman pod create` and remove it from the pod members (sidecar + egress workload). This is launch mechanics only — the keep-id uid mapping, the `--user $uid:$gid` on members, the NET_ADMIN nftables sidecar, the default-deny allowlist, and all other controls are **unchanged**. The non-pod paths (probes 014/016/033 and the plain workload run) keep `--userns=keep-id` on the container exactly as today.

Verified live (podman 5.7.0, rootless, ext4): `bash containment/execution-box/run.sh --worktree . --egress-probe` → the conflict above; pod creation never completes.

## Test cases

### TC-049-01 — pod create owns the userns; pod members do not set it
- **Mechanism:** drive the `--egress-probe` path with a stub `podman` that records argv per subcommand (pod create / sidecar run -d / workload run). Assert: the `podman pod create` argv contains `--userns=keep-id`; the sidecar `podman run -d --pod …` argv does NOT contain `--userns`; the egress workload `podman run … --pod …` argv does NOT contain `--userns` but DOES contain `--pod`.
- **Assertion:** `--userns` appears on pod-create and on NEITHER pod member; `--pod` appears on both members.

### TC-049-02 — non-pod paths keep `--userns` on the container (unchanged)
- **Mechanism:** drive the plain `--probe` path (014/016/033 — no pod) with the stub podman. Assert the workload/probe container argv STILL contains `--userns=keep-id` (the pod-path change must not strip userns from the non-pod containers).
- **Assertion:** non-pod `--probe` container argv contains `--userns=keep-id`.

### TC-049-03 — keep-id mapping preserved: members still set `--user $uid:$gid`
- **Mechanism:** assert the sidecar and egress workload member argv still carry `--user <uid>:<gid>` (the in-container identity), so moving userns to the pod does not drop the uid mapping the bind-mounted worktree relies on.
- **Assertion:** both pod members still pass `--user <uid>:<gid>`.

### TC-049-04 (L6, real host) — egress pod + sidecar start without the userns/pod conflict
- **Mechanism (real host):** `bash containment/execution-box/run.sh --worktree . --egress-probe`.
- **Assertion:** the run gets PAST `podman pod create` and the sidecar `podman run -d` without `--userns and --pod cannot be set together`; the sidecar becomes ready (or the probe proceeds to its allow/deny assertions). Record how far it gets verbatim. *(Note: this task fixes the userns/pod launch blocker; any further rootless-egress behavior the real run surfaces beyond pod/sidecar start is a separate follow-up, to be reported, not silently absorbed.)*

## Verification plan

- **Highest level achievable:** **L6** — a real `--egress-probe` run that no longer hits the userns/pod conflict (pod + sidecar start). Full allow/deny egress assertions under rootless gVisor are the residual if any further issue surfaces.
- **L5 harness:** extend `containment/execution-box/tests/` (the storage-quota stub-podman harness pattern) with argv capture for pod-create vs members; assert TC-049-01..03 with no live container.
- **L6 evidence:** quote the verbatim `--egress-probe` output showing it passes pod/sidecar start (no userns/pod error).
- **Cross-module state risk:** none — launch-arg restructuring in the egress path of run.sh only.
- **Runtime-visible surface:** the egress pod/sidecar/workload podman argv + the `--egress-probe` outcome.

## Out of scope

- The egress allowlist policy, nftables rules, default-deny logic, NET_ADMIN cap — all unchanged.
- Storage quota (045), runtime inspect (047), in-box cap verification (048), probe harness (046).
- Any further rootless-egress behavior beyond the userns/pod launch conflict (report as follow-up if surfaced).
