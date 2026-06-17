# Task 047: execution-box TC-016 runtime inspect portability

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix the execution-box `--probe` host-side runtime check so it reads the OCI runtime name from the portable `.OCIRuntime` inspect field instead of `.HostConfig.Runtime`, which podman 5.7 reports as the generic `"oci"`. Without this, `TC-016` fails on every container probe (`runtime=oci expected runc`), blocking all L6 container probes (014/016/033/032) even after task 045's storage-quota fix.

## Context

- Tech stack: bash (`containment/execution-box/run.sh`), no Go code.
- Same class of bug as task 045's StorageOpt fix: the probe's host-side `podman inspect` assertions were authored against fields that don't hold on podman 5.x. Verified live on this host (podman 5.7.0): `.HostConfig.Runtime` → `oci`; `.OCIRuntime` → `runc` (for `--runtime runc`) / `runsc` (for `--runtime runsc`). The fix swaps the field; the assertion (`actual == requested runtime`) is unchanged.
- The current check is `containment/execution-box/run.sh` ~line 568: `runtime_inspect="$(podman inspect --format '{{.HostConfig.Runtime}}' "$cid")"` then `[ "$runtime_inspect" = "$runtime" ] || die "TC-016 FAIL …"`.
- **Model tier: balanced (sonnet)** — a one-field shell fix plus a stub test and a real-host probe run.
- Dependencies: builds on task 045 (already merged) but is independent of task 046.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-047-01 | The `--probe` host-side runtime check reads `{{.OCIRuntime}}` (not `{{.HostConfig.Runtime}}`) and asserts it equals the requested `--runtime` value; the `TC-016 HOST:` line and the FAIL-on-mismatch `die` are preserved | must have |
| REQ-047-02 | A runtime mismatch still fails loudly (`TC-016 FAIL` + non-zero exit) — the fix must not turn the check into an always-pass | must have |
| REQ-047-03 | A real `--probe` run on this host (podman 5.7.0) reaches exit 0 with `TC-016 HOST: … runtime=runc` — the previously-failing step now passes | must have |
| REQ-047-04 | No load-bearing control or storage-quota logic is changed; if `docs/spec/interfaces.md` names the runtime-inspect field it is rewritten in place in the same commit | must have |

## Readiness gate

- [x] Test spec `047-execution-box-runtime-inspect-portability-test-spec.md` exists
- [x] All acceptance criteria below have a linked REQ ID
- [ ] No blocking dependencies (045 already merged)

## Acceptance criteria

- [ ] [REQ-047-01] run.sh's runtime inspect template references `.OCIRuntime` and not `.HostConfig.Runtime`; `TC-016 HOST:` is printed and the mismatch `die` remains
- [ ] [REQ-047-02] a stubbed `.OCIRuntime` that differs from the requested runtime → `TC-016 FAIL` + non-zero exit (TC-047-02)
- [ ] [REQ-047-03] `bash containment/execution-box/run.sh --worktree . --runtime runc --probe` → `TC-016 HOST: workload=agent runtime=runc` + exit 0 on this host (TC-047-03)
- [ ] [REQ-047-04] no load-bearing/storage change; spec updated in the feat commit if it names the field

## Verification plan

- **Highest level achievable:** L6 — real `--probe` run on podman 5.7.0 reaches exit 0 with `runtime=runc`.
- **L5 harness:** `bash containment/execution-box/tests/storage-quota-test.sh` (extend it, or a sibling) drives the inspect path with a stub podman exposing `.OCIRuntime`; assert the template choice + PASS/FAIL behavior without a live container.
- **L6 evidence:** quote the verbatim real-host `--probe` output (`TC-016 HOST: … runtime=runc`, exit 0).
- **Cross-module state risk:** none.
- **Runtime-visible surface:** the `TC-016 HOST:` line + probe exit code.

## Out of scope

- Storage-quota logic and fail-loud guards (task 045 — merged).
- `l6-probe.sh` harness wiring (task 046).
- In-box gVisor runtime detection (probe.sh) — this task is host-side `.OCIRuntime` only.

## Notes

- Confirm against real podman on this host that `.OCIRuntime` returns `runsc` for `--runtime runsc` (verified 2026-06-17) so probe 016 also passes once the host can launch runsc boxes.
- This completes the podman-5.7 host-inspect portability of the execution-box probe (StorageOpt fixed in 045, Runtime here).
