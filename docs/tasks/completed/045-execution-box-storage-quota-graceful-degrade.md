# Task 045: Execution-box launcher — host-portable disk quota + fail-loud on launch failure

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** completed

## Goal

Fix two co-located bugs in `containment/execution-box/run.sh` that block every container-launching L6 probe on ext4 hosts: (1) `--storage-opt "size=$storage_size"` is applied unconditionally but Podman rejects it on non-XFS backing filesystems; (2) a `podman create`/`podman run` failure currently exits 0, letting a non-started box masquerade as a passing probe. Both are fixed in the same commit per ADR 027.

## Context

- **Tech stack:** bash shell script (`containment/execution-box/run.sh`); no Go changes.
- **Governing ADR:** `docs/architecture/decisions/027-execution-box-storage-quota-graceful-degrade.md` (Accepted 2026-06-17) — Decision §1–5 and "Governs task 045" define all required behaviors. Read the full ADR before implementing.
- **Defect reproduced on this host:** ext4 rootless Podman 5.7.0; `podman create … --storage-opt "size=4G"` fails with `overlay.size and overlay.inodes only supported for backingFS XFS. Found extfs`; `run.sh` exits 0 despite the container never starting.
- **Load-bearing controls unaffected:** egress allowlist, read-only rootfs, `--cap-drop=all`, `--security-opt=no-new-privileges`, gVisor (`runsc`), CPU/memory/PID/tmpfs caps. The writable-layer disk quota is a secondary anti-DoS bound only.
- **Testability seam:** `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0|1` env var overrides the detection result so TC-045-01 through TC-045-04 run without a real XFS or ext4 host. The seam must be documented in `run.sh`'s header comment.
- **Detection approach (implementor's choice of one):**
  - Parse `podman info --format '{{.Store.GraphDriverName}} {{.Store.GraphRoot}}'` and check whether the graph driver is `overlay` and the backing FS is `xfs`.
  - Or: check `podman info --format '{{.Store.GraphOptions}}'` for an existing `size=` option (if Podman itself already tracks it).
  - Or: issue a cheap capability probe (`podman create --storage-opt size=0 …` and discard) and treat exit 0 as "supported".
  - Any of these is acceptable; the seam override must short-circuit the probe when set.
- **Spec updates (same feat commit):** `docs/spec/configuration.md` (EXEC_BOX_STORAGE_SIZE row, Resource floor row, Defaults policy note), `docs/spec/behaviors.md` (launcher "overlay limits" entry), `docs/spec/interfaces.md` (`--probe`/TC-003 storage assertion now conditional).
- **Model tier: balanced (sonnet)** — mechanical shell fix against a fixed contract.
- **Dependencies:** none (standalone `run.sh` change; does not depend on 043/044/046).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-045-01 | When enforceable AND `EXEC_BOX_STORAGE_SIZE` is non-empty: pass `--storage-opt "size=$storage_size"` to `podman create`/`podman run` exactly as today; no WARNING emitted | must have |
| REQ-045-02 | When NOT enforceable AND `EXEC_BOX_STORAGE_SIZE` is non-empty: omit `--storage-opt` entirely; emit a clearly worded stderr `WARNING` naming the degraded control (per-container disk quota unavailable on this filesystem); the box still launches (exit 0) | must have |
| REQ-045-03 | When `EXEC_BOX_STORAGE_SIZE` is empty (operator opt-out): omit `--storage-opt`; emit no WARNING regardless of enforceability | must have |
| REQ-045-04 | A testability seam `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0|1` overrides the detection result when set, so TC-045-01 and TC-045-02 are achievable without a real XFS host | must have |
| REQ-045-05 | Every `podman create` and `podman run` invocation checks its exit code; a non-zero exit causes `run.sh` to exit non-zero with a named error (e.g. via the existing `die` function); the launcher must never return exit 0 when the container did not start | must have |
| REQ-045-06 | The `--probe` TC-003 host inspect block (run.sh lines ~479-486) tolerates a null/empty `StorageOpt` field when enforceability is off; it must not `die "TC-003 FAIL…"` solely because `StorageOpt` is null on a non-enforceable host; the CPU/memory/PID/SHM assertions remain intact | must have |

## Readiness gate

- [x] Test spec `045-execution-box-storage-quota-graceful-degrade-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] No blocking dependencies

## Acceptance criteria

- [ ] [REQ-045-01] With `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1` and `EXEC_BOX_STORAGE_SIZE=4G`, the `podman create` argv captured by TC-045-01 contains `--storage-opt size=4G` and no WARNING appears on stderr
- [ ] [REQ-045-02] With `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0` and `EXEC_BOX_STORAGE_SIZE=4G`, the `podman create` argv does NOT contain `--storage-opt`; stderr contains a line matching `WARNING` and naming the degraded control; `run.sh` exits 0
- [ ] [REQ-045-03] With `EXEC_BOX_STORAGE_SIZE=""` and any `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` value, `--storage-opt` is absent from argv and no WARNING is emitted
- [ ] [REQ-045-04] TC-045-01 and TC-045-02 both pass using stub `podman` on a temp PATH and the `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` override, with no live Podman or XFS required
- [ ] [REQ-045-05] TC-045-03: stub `podman create` exits non-zero → `run.sh` exits non-zero with a named error; no silent exit-0 on failed launch
- [ ] [REQ-045-06] TC-045-04: with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0` and a stub `podman inspect` returning null StorageOpt, `run.sh --probe` prints `TC-003 PASS` (or equivalent conditional pass) and exits 0; with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1` and non-null StorageOpt, `TC-003 PASS` still fires
- [ ] Spec files updated in the same commit: `docs/spec/configuration.md` (EXEC_BOX_STORAGE_SIZE row + Resource floor + Defaults policy), `docs/spec/behaviors.md` (launcher overlay limits entry), `docs/spec/interfaces.md` (`--probe` TC-003 storage assertion)
- [ ] `make check` passes after the change

## Verification plan

- **Highest level achievable without a live XFS host:** L5 — bash integration tests using `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` override + stub `podman` on a temp PATH prove all four branches (TC-045-01 through TC-045-04). Test harness at `containment/execution-box/tests/storage-quota-test.sh`.
- **L5 harness command:**
  ```
  bash containment/execution-box/tests/storage-quota-test.sh
  ```
  Expected final assertion: `=== Results: N passed, 0 failed ===` exit 0
- **L6 residual (operator-only):** run `containment/execution-box/run.sh --probe` on this host (ext4, rootless Podman) with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` unset (real detection). Observe the `WARNING` on stderr and a successful probe exit 0. Record `TC-003 PASS` line in the evidence file. This step produces the L6 evidence for tasks 014/015/016/033 that were previously blocked by this bug.
- **Cross-module state risk:** none — pure `run.sh` change; no Go code touched.
- **Runtime-visible surface:** stderr WARNING line on non-XFS hosts; `TC-003 PASS` in `--probe` stdout; non-zero exit on failed `podman create`.

## Out of scope

- Moving the container store to XFS — operator responsibility.
- Changing the egress allowlist, gate-toolchain, or any other run.sh feature.
- Any Go code changes.
- `l6-probe.sh` wiring changes — that is task 046.

## Notes

- The WARNING text must be identifiable as naming the degraded control. A reasonable template: `execution-box: WARNING: per-container writable-layer disk quota (--storage-opt size) unavailable on this host (backing filesystem does not support overlay size enforcement); running without disk quota.`
- The `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` seam must be documented in the `run.sh` header block (in the Environment section alongside `EXEC_BOX_STORAGE_SIZE`, `EXEC_BOX_MEMORY`, etc.).
- The fail-loud fix (REQ-045-05) must wrap ALL `podman create` and `podman run` calls in the script (probe path, egress-probe path, workload path). Check each callsite.
- ADR 027 Consequences: "A host that *should* be XFS but is misconfigured degrades to a warning rather than a hard stop; operators must watch for the WARNING in probe evidence." — this is the accepted trade-off; do not harden it to a hard stop.
