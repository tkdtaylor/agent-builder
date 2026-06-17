# Test spec — Task 047: execution-box TC-016 runtime inspect portability

**Task:** 047-execution-box-runtime-inspect-portability
**Created:** 2026-06-17

## Context

The execution-box `--probe` host-side runtime check (`containment/execution-box/run.sh` ~line 568) verifies the container's OCI runtime by inspecting `{{.HostConfig.Runtime}}` and asserting it equals the requested `--runtime` value (`runc`/`runsc`). On **podman 5.7** `.HostConfig.Runtime` evaluates to the generic string `"oci"` (the runtime *type*), not the runtime name, so `TC-016` fails on every container probe:

```
TC-016 HOST: workload=agent runtime=oci
execution-box: TC-016 FAIL: host inspect runtime=oci expected runc
exit=1
```

Verified live on this host (podman 5.7.0): the actual runtime name is exposed at the **top-level `.OCIRuntime`** field — `podman inspect --format '{{.OCIRuntime}}'` returns `runc` for `--runtime runc` and `runsc` for `--runtime runsc`. `.HostConfig.Runtime` returns `oci` in both cases. This is the same class of podman-version inspect incompatibility fixed for `StorageOpt` in task 045 (the probe's host-side inspect assertions were authored against fields that don't hold on podman 5.x).

This is a pure portability fix: it changes which inspect field the runtime check reads. It does not change the assertion semantics (`actual runtime == requested runtime`) or any containment control.

## Test cases

### TC-047-01 — runtime check reads `.OCIRuntime` and PASSES when it matches the requested runtime
- **Mechanism:** drive the `--probe` host-inspect path with a stub `podman` whose `inspect` returns `.OCIRuntime = runc` for a `--runtime runc` box (and the four load-bearing numeric fields non-zero). Assert the run.sh runtime template references `.OCIRuntime` (not `.HostConfig.Runtime`), `TC-016 HOST: … runtime=runc` is printed, no `TC-016 FAIL`, exit 0.
- **Assertion:** the inspect template string in run.sh contains `.OCIRuntime` and does NOT contain `.HostConfig.Runtime`; the probe prints `TC-016 …runtime=runc` and exits 0.

### TC-047-02 — runtime mismatch still FAILS loudly
- **Mechanism:** stub `podman inspect` returns `.OCIRuntime = runc` while the box was requested with `--runtime runsc` (or vice-versa). Assert `TC-016 FAIL` is emitted (via `die`) and run.sh exits non-zero. This guards that the portability fix did not weaken the assertion into an always-pass.
- **Assertion:** a runtime that does not equal the requested `--runtime` value → `TC-016 FAIL` on stderr + non-zero exit.

### TC-047-03 (L6, real host) — real podman probe completes with TC-016 PASS, exit 0
- **Mechanism (operator/real host):**
  ```
  bash containment/execution-box/run.sh --worktree . --runtime runc --probe
  ```
- **Assertion:** the probe runs the box, prints `TC-016 HOST: … runtime=runc` and the TC-003 PASS line, and **exits 0** (no inspect template error, no TC-016 FAIL). The previously-failing TC-016 step now passes on podman 5.7.0/ext4.

## Verification plan

- **Highest level achievable:** **L6** — a real `--probe` run on this host (podman 5.7.0) reaches exit 0 with `TC-016 HOST: … runtime=runc`. The whole point of the fix is observable only against real podman; the stub L5 cases (TC-047-01/02) guard the template choice and the mismatch path.
- **L5 harness:** the existing `containment/execution-box/tests/storage-quota-test.sh` (or a sibling) drives the inspect path with a stub podman; assert the `.OCIRuntime` template + the PASS/FAIL behavior without a live container.
- **L6 evidence:** `bash containment/execution-box/run.sh --worktree . --runtime runc --probe` → `TC-016 HOST: workload=agent runtime=runc` + exit 0 (quote verbatim).
- **Cross-module state risk:** none — single inspect-field change in the launcher's probe path.
- **Runtime-visible surface:** the `TC-016 HOST:` line and the probe's exit code.

## Out of scope

- Any change to the load-bearing controls or the storage-quota logic (task 045).
- The `l6-probe.sh` harness wiring (task 046).
- The in-box runtime detection (gVisor markers) — this task only fixes the host-side `.OCIRuntime` inspect.
