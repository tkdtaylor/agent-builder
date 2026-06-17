# Task 048: runtime-aware in-box resource-cap verification (gVisor) + probe-path launch-guard mislabel fix

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Make the execution-box in-box probe's TC-003 resource-cap check **runtime-aware** per ADR 028: under `runsc` (gVisor) assert the memory cap in-box and defer cpu/pids to the launcher's authoritative host-side `podman inspect`; under `runc` and any unknown/future runtime keep the strict all-three-in-box check (fail-closed allowlist). Also fix the probe-path launch guard so a legitimate in-box probe failure is not mislabeled as a podman launch failure. This unblocks the L6 container probes (014/016/033/032) under the default `agent`→`runsc` runtime, where TC-003 currently fails on a gVisor cgroupfs-presentation difference even though the caps are applied and enforced.

## Context

- Tech stack: bash (`containment/execution-box/probe.sh` TC-003 block ~lines 110-145; `containment/execution-box/run.sh` probe-path `podman start --attach` guard ~line 571). No Go code.
- **Governing ADR: 028** (`docs/architecture/decisions/028-execution-box-runtime-aware-inbox-cap-verification.md`, Accepted) — read its Decision (3 points), the allowlist note, and the "Governs task 048" assertions. Related: ADR 027 (same verify-where-reliable principle), ADR 016 (tiered runtime seam).
- Verified live (podman 5.7.0, cgroup v2): runc → in-box `cpu.max`/`pids.max`/`memory.max` all visible; runsc → `/sys/fs/cgroup` is `cpu/ cpuacct/ cpuset/ devices/ job/ memory/ pids/` with only memory readable; host-side inspect → `NanoCpus=2000000000 PidsLimit=256 Memory=2147483648`. The caps are enforced by gVisor; only the in-box cpu/pids *view* is absent.
- The launcher's host-side TC-003 cpu/pids `die`-on-zero checks (`run.sh` ~lines 556-558) are what make the runsc branch safe — they MUST stay exactly as-is (they are the authoritative cpu/pids check under runsc).
- **Model tier: balanced (sonnet)** — a runtime-conditional branch in a security-relevant shell script + a launch-guard fix, behind a strict gate; touches the containment profile.
- Dependencies: builds on tasks 045/047 (merged); independent of task 046.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-048-01 | probe.sh TC-003 branches on `EXEC_BOX_RUNTIME`: under `runc` (and any non-`runsc` runtime) all three caps (cpu, memory, pids) must be visible in-box — unchanged from today | must have |
| REQ-048-02 | Under `runsc`, the in-box check requires the memory cap visible in-box and does NOT fail on `cpu=unknown`/`pids=unknown`; the PASS line names cpu/pids as host-side-authoritative under runsc | must have |
| REQ-048-03 | Under `runsc`, no cap goes unchecked: the launcher's host-side `podman inspect` cpu/pids `die`-on-missing checks remain, so a missing host-side cpu/pids cap still fails the `--probe` non-zero | must have |
| REQ-048-04 | The relaxed path is an **allowlist** of `runsc` only: any runtime that is not `runc`/`runsc` (e.g. `kata`) defaults to the strict all-three-in-box check (fail-closed) | must have |
| REQ-048-05 | The probe-path `podman start`/attach guard distinguishes a podman launch failure (exit 125 → "container did not start", non-zero) from the container/probe's own non-zero exit (reported/propagated as a probe failure with its failing TC marker, NOT mislabeled as a launch failure) | must have |
| REQ-048-06 | `docs/spec/interfaces.md` (`--probe`/TC-003: ADD the in-box cap-visibility contract — runtime-aware) and `docs/spec/behaviors.md` B-010 (launch-failure vs in-box-failure distinction; runtime-aware in-box cap check), both referencing ADR 028, are updated in the same feat commit | must have |

## Readiness gate

- [x] Test spec `048-l6-runtime-aware-inbox-cap-verification-test-spec.md` exists
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Governing ADR 028 accepted (done)

## Acceptance criteria

- [ ] [REQ-048-01] runc + all three in-box → TC-003 PASS (all-three message); runc + cpu or pids missing in-box → `fail TC-003` + non-zero (TC-048-01)
- [ ] [REQ-048-02] runsc + memory in-box + host-side cpu/pids present → TC-003 PASS, exit 0, message names the runsc host-side-authoritative shape (TC-048-02)
- [ ] [REQ-048-03] runsc + host-side cpu or pids cap absent → `--probe` exits non-zero with the host-side TC-003 `die` (TC-048-03)
- [ ] [REQ-048-04] non-`runc`/non-`runsc` runtime + cpu/pids not visible in-box → strict path → `fail TC-003` + non-zero (TC-048-04)
- [ ] [REQ-048-05] probe-path: podman exit 125 → "container did not start" named error + non-zero; in-box non-zero (≠125) → probe failure, no "container did not start" mislabel (TC-048-05)
- [ ] [REQ-048-06] interfaces.md + behaviors.md updated in the feat commit, referencing ADR 028

## Verification plan

- **Highest level achievable:** **L6** — a real `--probe` run under default `agent`→`runsc` on this cgroup-v2 host reaches exit 0 with the runsc TC-003 PASS shape; `--runtime runc --probe` still passes with the all-three shape.
- **L5 harness:** extend `containment/execution-box/tests/storage-quota-test.sh` (or a sibling) with per-runtime stub `/sys/fs/cgroup` layouts + a stub host-side inspect + a stub podman for the launch-guard case; keep the existing 10 storage-quota/runtime cases green.
- **L6 evidence:** `bash containment/execution-box/run.sh --worktree . --probe` (default runsc) → runsc TC-003 PASS + exit 0; `--runtime runc --probe` → all-three TC-003 PASS + exit 0 (quote verbatim).
- **Cross-module state risk:** none.
- **Runtime-visible surface:** the runtime-aware TC-003 PASS/FAIL line + probe exit code + launch-guard error text.

## Out of scope

- Storage-quota (045) and runtime inspect (047) — merged.
- `l6-probe.sh` wiring (046).
- A gVisor-enforcement test (ADR 028 names it as the right tool only if gVisor enforcement is ever doubted).
- Resource-cap values / env knobs.

## Notes

- Key the relaxed branch on an explicit `runsc` allowlist so a future Kata/Firecracker tier fails closed to the strict check (ADR 028 reopening condition + architect Risk 5).
- Do not touch the host-side TC-003 cpu/pids `die` checks in run.sh — they are the authoritative cpu/pids verification the runsc branch relies on.
- Reuse the exit-125-vs-inner-exit pattern task 045 established on the workload-run and egress-probe paths for the probe-path guard.
