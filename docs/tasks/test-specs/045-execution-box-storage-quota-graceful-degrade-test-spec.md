# Test Spec 045: Execution-box launcher — host-portable disk quota + fail-loud on launch failure

**Linked task:** [`docs/tasks/backlog/045-execution-box-storage-quota-graceful-degrade.md`](../backlog/045-execution-box-storage-quota-graceful-degrade.md)
**Written:** 2026-06-17
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-045-01 | TC-045-01 | ⏳ |
| REQ-045-02 | TC-045-02 | ⏳ |
| REQ-045-03 | TC-045-03 | ⏳ |
| REQ-045-04 | TC-045-01, TC-045-02 | ⏳ |
| REQ-045-05 | TC-045-03 | ⏳ |
| REQ-045-06 | TC-045-04 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Testability seam design

`containment/execution-box/run.sh` must expose a detection seam so the storage-quota enforceability decision is testable without launching a real container on a real XFS or ext4 host. The recommended approach (mirrors the `L6_PROBE_PATH` pattern used by the probe harness):

**`EXEC_BOX_STORAGE_QUOTA_SUPPORTED` env override:** When this variable is set to `1`, treat the backing store as enforceable (apply `--storage-opt size=…`); when set to `0`, treat it as non-enforceable (omit the flag, emit the WARNING). When the variable is **unset**, run the real detection (parse `podman info` or do a capability probe). This keeps the detection logic in one place while making every branch unit-testable without XFS.

For TC-045-01 and TC-045-02, the test harness uses a **stubbed `podman`** placed on a temp PATH (same pattern as `scripts/tests/l6-probe-test.sh`) combined with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` to control the quota-enforceability branch. For TC-045-03, the stubbed `podman create` exits non-zero unconditionally.

The test file lives at `containment/execution-box/tests/storage-quota-test.sh` (a new file; the directory is new — create it). The harness style follows `scripts/tests/l6-probe-test.sh`: stub factory, named test cases, `tc_pass`/`tc_fail` helpers, summary line, exit 1 on any failure.

---

## Test cases

### TC-045-01: quota applied when enforceable — `--storage-opt size=…` present in podman argv

- **Requirement:** REQ-045-01, REQ-045-04
- **Mechanism:** Set `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1` and `EXEC_BOX_STORAGE_SIZE=4G`. Run `containment/execution-box/run.sh --print-runtime-plan` first to confirm the seam is reachable. Then invoke the argv-builder path via a stubbed `podman` that captures and echoes its arguments to a temp file, then exits 0. Trigger `run.sh --probe` with the stub on PATH and `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1`.
- **Expected output:**
  - The captured `podman create` argv contains `--storage-opt` and `size=4G`.
  - No WARNING line appears on stderr.
  - `run.sh` exits 0 (the stub `podman` probe completes successfully).
- **Edge cases:**
  - If `EXEC_BOX_STORAGE_SIZE` is explicitly set to empty (`EXEC_BOX_STORAGE_SIZE=""`), the `--storage-opt` flag must be absent even on a "supported" host and no WARNING is emitted (REQ-045-04: empty = deliberate opt-out). This variant is tested as part of TC-045-01.

### TC-045-02: quota skipped + WARNING emitted when not enforceable — flag absent, box still launches

- **Requirement:** REQ-045-02, REQ-045-04
- **Mechanism:** Set `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0` and `EXEC_BOX_STORAGE_SIZE=4G`. Provide a stubbed `podman` that captures argv, then exits 0. Run `run.sh --probe` with the stub on PATH.
- **Expected output:**
  - The captured `podman create` argv does NOT contain `--storage-opt`.
  - stderr contains a WARNING line that names the degraded control. The WARNING must include the word `WARNING` (case-sensitive) and the phrase `disk quota` or `overlay` or `storage-opt` (any one of these — the exact wording is set by the implementation but must be identifiable as naming the degraded control per ADR 027 Decision §3).
  - `run.sh` exits 0 (the box still launches — degraded, not dead).
- **Edge cases:**
  - Empty `EXEC_BOX_STORAGE_SIZE=""` with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0`: flag absent AND no WARNING (operator opted out; degraded-quota warning is suppressed when the operator explicitly opted out). Verify this variant too.
  - `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0` with `EXEC_BOX_STORAGE_SIZE=4G` and `--egress-probe` flag: same behavior applies (common_args is shared by both probe paths); the `--storage-opt` flag must be absent from the `podman` argv in this path too.

### TC-045-03: forced `podman create` failure → `run.sh` exits non-zero with a named error

- **Requirement:** REQ-045-05
- **Mechanism:** Provide a stub `podman` that always exits non-zero (e.g. exit code 125) and prints a fake error to stderr. Set `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1` (so quota logic is not the cause of failure). Run `run.sh --probe` with the stub on PATH.
- **Expected output:**
  - `run.sh` exits non-zero (any non-zero code; must NOT be exit 0).
  - stderr output from `run.sh` contains a message identifying that `podman create` or `podman run` failed (a "named error", not just propagated stderr). The message must make it unambiguous that the container did not start — e.g. contains the word `failed` or the word `error` (case-insensitive) alongside reference to `podman` or the container name.
- **Edge cases:**
  - The same fail-loud behavior applies to the `podman run` path (the workload/egress path, not just `--probe`). Provide a second variant where the stub exits non-zero during a run invocation; confirm non-zero exit and named error. (Can be a sub-case of this TC, not a separate TC.)

### TC-045-04: `--probe` TC-003 storage assertion tolerates the no-quota inspect shape on non-enforceable hosts

- **Requirement:** REQ-045-06
- **Mechanism:** This TC validates that the fix in run.sh's `--probe` inspection block (lines ~479-486) no longer hard-fails when StorageOpt is null (the ext4/non-XFS case). Use `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=0`. Provide a stub `podman` whose `inspect` subcommand prints a payload with `null` in the StorageOpt position (e.g. `2000000000 2147483648 256 67108864 null`). Run `run.sh --probe` with the stub on PATH.
- **Expected output:**
  - `run.sh` does NOT call `die "TC-003 FAIL…"` on the null StorageOpt field.
  - stdout contains `TC-003 PASS` (or an equivalent conditional-pass message if the implementation uses different phrasing for the degraded case).
  - `run.sh` exits 0.
- **Edge cases:**
  - On an enforceable host (`EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1`), the existing TC-003 assertion (StorageOpt non-null) must still fire. Provide a stub `inspect` that returns a non-null StorageOpt field and confirm `TC-003 PASS` is still emitted correctly.
  - The `null` in the inspect output that triggers the old false-failure is specifically in the StorageOpt position. The assertion must not be weakened to tolerate null in the NanoCpus, Memory, or PidsLimit positions (those remain load-bearing and must still cause a `TC-003 FAIL`).

---

## Post-implementation verification

- [ ] All test cases above pass via `bash containment/execution-box/tests/storage-quota-test.sh` with stub podman on temp PATH (L5)
- [ ] `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` seam confirmed present and documented in `run.sh` header comment
- [ ] `make check` passes after the change
- [ ] L6 residual documented: run `containment/execution-box/run.sh --probe` on an actual ext4 host with rootless Podman to observe the WARNING on stderr and confirm the box launches; run on an XFS host (or with `EXEC_BOX_STORAGE_QUOTA_SUPPORTED=1` + real Podman) to confirm quota is applied

## Test framework notes

Framework: bash integration tests with stub binaries on a temp PATH. Follows the same pattern as `scripts/tests/l6-probe-test.sh`: a stub factory that writes binaries to a temp dir and prepends that dir to PATH; named test functions; `tc_pass`/`tc_fail` accumulators; a results summary line of the form `=== Results: N passed, M failed ===`; exit 1 on any failure.

The key testability seam is `EXEC_BOX_STORAGE_QUOTA_SUPPORTED` (env var, value `0`/`1`). The `podman` stub must handle at least the subcommands used in `run.sh --probe`: `build` (exit 0), `create` (capture argv to a file, exit 0 or non-zero depending on the test), `inspect` (print a configurable fake payload), `start` (exit 0), `rm` (exit 0).

The argv-capture pattern: the stub writes `$@` to a temp file (`$STUB_ARGV_FILE`) so the test can inspect what flags were actually passed to `podman create`. Set `STUB_ARGV_FILE` in the environment before invoking `run.sh`.

No live Podman, real image build, or XFS filesystem required for L5. The L6 residual (real ext4 host observation) stays with the operator.
