# Test Spec 046: l6-probe.sh live-run wiring fixes

**Linked task:** [`docs/tasks/backlog/046-l6-probe-wiring-fixes.md`](../backlog/046-l6-probe-wiring-fixes.md)
**Written:** 2026-06-17
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-046-01 | TC-046-01 | ⏳ |
| REQ-046-02 | TC-046-02 | ⏳ |
| REQ-046-03 | TC-046-03 | ⏳ |
| REQ-046-04 | TC-046-04 (regression) | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Context: existing test harness

The existing test harness lives at `scripts/tests/l6-probe-test.sh` and covers TC-044-01 through TC-044-05. All new TCs in this spec are added to that same file (do not create a separate harness). The harness uses `make_probe_stub_dir` plus `L6_PROBE_PATH` / `L6_EVIDENCE_FILE` to inject stubs without touching real PATH. New test cases follow the same `run_tc046_NN()` naming convention and are appended to the existing file.

---

## Test cases

### TC-046-01: resolved gate-tools argument is a real directory (non-empty), honors `EXEC_BOX_GATE_TOOLS`

- **Requirement:** REQ-046-01
- **Mechanism:** Run `scripts/l6-probe.sh --dry-run` with a faked PATH (all prerequisite stubs present). Inspect the output for the resolved gate-tools argument in the probe commands for tasks 014, 015, 016, and 033. Repeat with `EXEC_BOX_GATE_TOOLS` set to a known temp directory path.
- **Expected output (unset `EXEC_BOX_GATE_TOOLS`):** The probe command strings for tasks 014, 015, 016, and 033 contain a non-empty `--gate-tools` value that is NOT `""` (an empty string). The value is the resolved path to `containment/execution-box/gate-tools` (or an absolute equivalent). The stdout must NOT contain `--gate-tools ""`.
- **Expected output (set `EXEC_BOX_GATE_TOOLS=/tmp/test-gate-tools`):** The probe command strings for those same tasks contain `--gate-tools /tmp/test-gate-tools`. The display strings `CMD_014`, `CMD_015`, `CMD_016`, `CMD_033` may still show `<gate-tools-dir>` as a placeholder; what matters is that the **actual argv** passed to the subprocess (the `run_probe` actual_cmd fourth+ arguments) contains the resolved non-empty value.
- **Edge cases:**
  - Confirm the resolved default equals what `run.sh` itself uses by default (the path named in `run.sh` line ~49: `${EXEC_BOX_GATE_TOOLS:-$box_dir/gate-tools}`). The two scripts must resolve to the same directory when `EXEC_BOX_GATE_TOOLS` is unset.
  - If the resolved directory does not exist on the test host, the test still passes (it is testing the argv, not the directory contents; the real validation happens at runtime via `run.sh --print-toolchain-plan`).

### TC-046-02: `AGENT_BUILDER_PUBLISH_REMOTE` threading and SKIP-when-unset

- **Requirement:** REQ-046-02
- **Part A — threaded when set:** Run `scripts/l6-probe.sh --dry-run` with a faked PATH (all stubs present, including `gh` and `git`) and `AGENT_BUILDER_PUBLISH_REMOTE=git@github.com:example/repo.git` set in the environment. Inspect the dry-run output and evidence file for probes 034 and 032.
  - **Expected:** The actual argv (the `run_probe` fourth+ argument array) for probes 034 and 032 contains `AGENT_BUILDER_PUBLISH_REMOTE=git@github.com:example/repo.git`. The probes are NOT skipped solely because the var is set; their SKIP status depends only on the standard prerequisites (gh, git remote, etc.).
- **Part B — SKIP when unset, exit 0:** Run `scripts/l6-probe.sh --dry-run` with faked PATH but `AGENT_BUILDER_PUBLISH_REMOTE` deliberately unset (ensure it is not inherited from the shell). Inspect the dry-run output and evidence file for probes 034 and 032.
  - **Expected:** Probe 034 row shows `SKIP` with a reason that references the missing remote (e.g. `AGENT_BUILDER_PUBLISH_REMOTE unset`). Probe 032 row also shows `SKIP` (because 034's dependency is missing). Exit code is 0 (SKIP is not FAIL). The SKIP reason must be distinct from the existing `no git remote configured` SKIP (which fires when `git remote -v` returns empty — a different condition).
- **Edge cases:**
  - `AGENT_BUILDER_PUBLISH_REMOTE` unset must NOT cause a FAIL row (it is a SKIP with exit 0, same as a missing tool prerequisite).
  - When `AGENT_BUILDER_PUBLISH_REMOTE` is set but `gh` is absent, the existing `gh absent` SKIP reason must still fire (the missing-gh condition takes precedence or is combined; both must be surfaced).

### TC-046-03: no probe command contains `AGENT_BUILDER_SANDBOX_RUNTIME=srt`

- **Requirement:** REQ-046-03
- **Mechanism:** Run `scripts/l6-probe.sh --dry-run` with a faked PATH (all stubs present). Capture the complete stdout + the evidence file content. Scan both for the string `AGENT_BUILDER_SANDBOX_RUNTIME=srt`.
- **Expected output:** Neither stdout nor the evidence file contains the string `AGENT_BUILDER_SANDBOX_RUNTIME=srt`. Exit code is 0.
- **Part B — probe 028 skip condition no longer gates on `HAS_SRT`:** With `srt` absent from the stub PATH, probe 028 must NOT be skipped due to `srt` absence (the srt gating on 028 only existed because the stale `AGENT_BUILDER_SANDBOX_RUNTIME=srt` needed srt present; after the fix, 028 is gated only on `claude` presence). Confirm that with `srt` absent and `claude` present, probe 028 is NOT skipped.
- **Part C — probe 021 SKIP reason unchanged:** With `srt` absent, probe 021 must still show `SKIP` with reason `prereq srt absent` (the live srt harness still requires srt; that gating is real). Confirm the fix did not accidentally remove the 021 srt gate while removing the 028/032 srt dependency.
- **Edge cases:**
  - `AGENT_BUILDER_SANDBOX_RUNTIME` must not appear anywhere in the output with value `srt`. It is acceptable if the script mentions the variable name in a comment or documentation string, but the actual env-var assignment `AGENT_BUILDER_SANDBOX_RUNTIME=srt` must be absent from every argv array.

### TC-046-04: existing TC-044-01 through TC-044-05 remain green (regression guard)

- **Requirement:** REQ-046-04
- **Mechanism:** After applying all three fixes, run the full existing `scripts/tests/l6-probe-test.sh` test file (which includes the new TC-046-01 through TC-046-03 functions appended to it). All original TC-044-xx cases must still pass.
- **Expected output:** The results summary line reports all TC-044-xx cases as `PASS`. Exit code is 0.
- **Specific regression checks:**
  - TC-044-01 (10 rows in correct closing order): must still emit all 10 rows in the exact sequence 014 → 015 → 016 → 021 → 030 → 022 → 028 → 033 → 034 → 032.
  - TC-044-02 (SKIP when runsc absent): 016 and 032 must still show SKIP; 028 must NOT show SKIP solely due to runsc absence (this verifies the fix in REQ-046-03 does not break the 016-specific srt gating while keeping 028 free of it).
  - TC-044-03 (evidence file shape): evidence file still has 10 rows with the correct format.
  - TC-044-04 (preflight gate): non-dry-run with NOT READY preflight still exits non-zero.
  - TC-044-05 (make target): `make --dry-run l6-probe` still shows the script invocation; `l6-probe` still in `.PHONY`; still absent from `check:` and `fitness:` prereqs.

---

## Post-implementation verification

- [ ] All test cases above pass via `bash scripts/tests/l6-probe-test.sh` (L5, no live host required)
- [ ] `make check` passes after the changes
- [ ] No `AGENT_BUILDER_SANDBOX_RUNTIME=srt` in any probe argv confirmed by TC-046-03
- [ ] L6 residual: run `scripts/l6-probe.sh` (without `--dry-run`) on a provisioned host after task 045 is merged; confirm probes 014/015/016 now proceed past the storage-opt error; confirm 034/032 skip gracefully when `AGENT_BUILDER_PUBLISH_REMOTE` is unset; confirm 028 is no longer blocked by missing srt

## Test framework notes

Framework: add new `run_tc046_NN()` functions to the existing `scripts/tests/l6-probe-test.sh` file. Call them in the main body alongside the existing TC-044 calls. Use the same `make_probe_stub_dir` stub factory — no new test infrastructure needed.

For TC-046-01 (gate-tools argv inspection), the `--dry-run` path does not invoke real subprocesses, but it does construct the `run_probe actual_cmd` array. The test must verify that the fourth+ arguments to `run_probe` (the real argv) contain the resolved path, not `""`. This may require the fix to store the resolved gate-tools path in a variable that is visible in the dry-run output or passed to `status_line`; alternatively, the test can grep the dry-run stdout for the literal substring `--gate-tools ""` and assert it is absent, then assert the resolved path substring is present. The implementation detail of how the resolved path is surfaced is up to the executor, but the test must confirm it is non-empty and honors `EXEC_BOX_GATE_TOOLS`.

For TC-046-02 Part B, use `env -u AGENT_BUILDER_PUBLISH_REMOTE bash scripts/l6-probe.sh --dry-run` or explicitly `unset AGENT_BUILDER_PUBLISH_REMOTE` in the test subshell before invoking the script.

For TC-046-03, the full stdout scan is done with `grep -F 'AGENT_BUILDER_SANDBOX_RUNTIME=srt'` against the combined output; the test must grep both the printed status lines AND the CMD_028/CMD_032 display strings (which the task spec says may remain as display strings but must not contain the actual var assignment).

No live Podman, srt, claude, or gh required. Highest achievable: L5.
