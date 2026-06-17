# Test spec — Task 048: runtime-aware in-box resource-cap verification (gVisor) + probe-path launch-guard mislabel fix

**Task:** 048-l6-runtime-aware-inbox-cap-verification
**Created:** 2026-06-17
**Governing ADR:** 028 (runtime-aware in-box resource-cap verification)

## Context

Under gVisor (`runsc`), the execution-box in-box probe (`containment/execution-box/probe.sh`) TC-003 fails because gVisor's in-box `/sys/fs/cgroup` is a partial cgroup-v1-style emulation that exposes the **memory** limit but **not** cpu/pids — while the caps ARE applied and enforced (the launcher's host-side `podman inspect` confirms `NanoCpus`/`PidsLimit`/`Memory`). Per ADR 028, the in-box check becomes runtime-aware: `runsc` asserts memory in-box and defers cpu/pids to the authoritative host-side inspect; `runc` and any unknown runtime keep the strict all-three-in-box check (fail-closed allowlist). The same probe run also mislabels a legitimate in-box probe failure as `podman start failed: container did not run`; the probe-path launch guard must distinguish a podman launch failure (exit 125) from the container/probe's own non-zero exit.

Verified live (this host, podman 5.7.0, cgroup v2): runc → in-box `cpu.max`/`pids.max`/`memory.max` all visible; runsc → `cpu/ pids/ memory/` dirs but only memory readable; host-side inspect → `NanoCpus=2000000000 PidsLimit=256 Memory=2147483648`.

## Test cases

### TC-048-01 — runc: in-box TC-003 unchanged (all three caps asserted in-box)
- **Mechanism:** drive probe.sh (or the `--probe` path) with `EXEC_BOX_RUNTIME=runc` and an in-box `/sys/fs/cgroup` exposing `cpu.max`/`memory.max`/`pids.max` (or the v1 fallbacks). Assert TC-003 PASS with the existing all-three message; then with cpu (or pids) **absent** in-box, assert TC-003 **FAIL** + non-zero. The runc path must not be weakened.
- **Assertion:** runc + all three visible → TC-003 PASS; runc + any of cpu/pids/memory missing in-box → `fail TC-003` + non-zero exit.

### TC-048-02 — runsc: memory in-box + cpu/pids deferred to host-side (PASS when host-side caps present)
- **Mechanism:** `EXEC_BOX_RUNTIME=runsc`, in-box cgroupfs exposes memory but NOT cpu/pids (the gVisor shape), AND the launcher's host-side inspect reports non-zero NanoCpus + PidsLimit. Assert TC-003 PASS with a message that names cpu/pids as host-side-authoritative under runsc (e.g. `cpu/pids caps verified host-side under runsc`), exit 0. `cpu=unknown`/`pids=unknown` in-box must NOT fail the probe under runsc when the host-side caps are present.
- **Assertion:** runsc + memory-in-box + host-side cpu/pids present → TC-003 PASS, exit 0; the PASS line distinguishes the runsc verification shape.

### TC-048-03 (CRITICAL negative) — runsc: missing host-side cpu/pids cap STILL fails the probe
- **Mechanism:** `EXEC_BOX_RUNTIME=runsc`, in-box memory visible, but force the **host-side** inspect to report a missing/zero cpu or pids cap (NanoCpus=0 or PidsLimit=-1). Assert the overall `--probe` exits **non-zero** with the host-side TC-003 `die` (cpu/pids cap not set). This proves the runsc branch did NOT relax into an always-pass — no cap goes unchecked.
- **Assertion:** runsc + host-side cpu/pids absent → `--probe` exits non-zero, host-side TC-003 FAIL named.

### TC-048-04 — unknown/future runtime defaults to STRICT (fail-closed allowlist)
- **Mechanism:** `EXEC_BOX_RUNTIME=kata` (or any value that is not `runc`/`runsc`), in-box cgroupfs exposing memory but not cpu/pids (gVisor-like). Assert TC-003 **FAILS** in-box (strict all-three applies) — the relaxed path is an allowlist of `runsc` only; an unknown runtime must NOT silently inherit the deferral.
- **Assertion:** a non-`runc`/non-`runsc` runtime with cpu/pids not visible in-box → `fail TC-003` + non-zero.

### TC-048-05 — probe-path launch guard distinguishes launch failure from in-box failure
- **Mechanism:** on the `--probe` path (`run.sh`), (a) force the `podman start`/attach to return podman exit **125** (launch failure) → run.sh dies with a "container did not start" named error, non-zero; (b) force the in-box probe to exit non-zero (e.g. 1, a real TC failure inside a container that started) → run.sh reports it as a **probe failure** (surfaces the in-box output / non-zero), NOT as "container did not start"/"podman start failed".
- **Assertion:** 125 → "container did not start" named error + non-zero; in-box non-zero (≠125) → reported as a probe failure (no "container did not start" mislabel) + non-zero.

## Verification plan

- **Highest level achievable:** **L6** — a real `--probe` run under the default `agent`→`runsc` runtime on this cgroup-v2 host reaches exit 0 with the runsc TC-003 PASS shape; a real `--runtime runc --probe` run still passes with the all-three shape.
- **L5 harness:** extend `containment/execution-box/tests/storage-quota-test.sh` (or a sibling) with a stub `/sys/fs/cgroup` layout per runtime and a stub host-side inspect, plus a stub podman for TC-048-05; drive probe.sh / run.sh without a live container. Keep all existing storage-quota + runtime cases green.
- **L6 evidence:** quote verbatim — `bash containment/execution-box/run.sh --worktree . --probe` (default runsc) → runsc TC-003 PASS line + `exit 0`; and `--runtime runc --probe` → all-three TC-003 PASS + exit 0.
- **Cross-module state risk:** none — probe.sh in-box branch + run.sh probe-path guard only.
- **Runtime-visible surface:** the TC-003 PASS/FAIL line (now runtime-aware) and the probe exit code / launch-guard error text.

## Out of scope

- Storage-quota logic (task 045) and runtime inspect (task 047) — merged.
- `l6-probe.sh` harness wiring (task 046).
- A gVisor-enforcement test (ADR 028 names this as the right tool *if* gVisor enforcement is ever in question — not part of this task).
- Changing resource-cap values or the `EXEC_BOX_CPUS`/`EXEC_BOX_MEMORY`/`EXEC_BOX_PIDS_LIMIT` knobs.
