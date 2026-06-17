# Test Spec 044: L6 probe harness and evidence collector

**Linked task:** [`docs/tasks/backlog/044-l6-probe-harness.md`](../backlog/044-l6-probe-harness.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-044-01 | TC-044-01 | ⏳ |
| REQ-044-02 | TC-044-02 | ⏳ |
| REQ-044-03 | TC-044-03 | ⏳ |
| REQ-044-04 | TC-044-04 | ⏳ |
| REQ-044-05 | TC-044-05 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-044-01: dry-run emits all 10 rows in the correct closing order

- **Requirement:** REQ-044-01
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with a faked PATH that satisfies all prerequisites so no probe is skipped. The script must not invoke any real probe commands; it must only print what it *would* do.
- **Expected output:** stdout contains exactly 10 rows, one per task ID in the authoritative checklist's closing order, in this sequence: 014, 015, 016, 021, 030, 022, 028, 033, 034, 032 (the checklist enumerates 10 distinct task IDs — 030 is the ledger-update task and gets its own row). Each row identifies the task ID and the probe command it would invoke (verbatim, matching the checklist). Exit code is 0.
- **Edge cases:** the order must be exactly as specified — the closing order from the checklist (014 → 015 → 016 → 021 → 030-ledger → 022 → 028 → 033 → 034 → 032). A sorted-by-task-ID order would be wrong; the test must assert position, not just presence.

### TC-044-02: a probe whose prerequisite is absent is marked SKIP with a reason — not FAIL

- **Requirement:** REQ-044-02
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with a faked PATH where `runsc` is absent (removing it from the stub directory). The probes that require `runsc` (tasks 016 and the live `podman --runtime runsc` calls) must be skipped; the remaining probes must proceed normally.
- **Expected output:** the row(s) for tasks that depend on `runsc` show status `SKIP` and include a recorded reason (e.g. `prereq runsc absent`). All other rows show `DRY-RUN` (or equivalent not-executed status). Exit code is 0 (a SKIP is not a failure). No row shows `FAIL` solely because a prerequisite is missing.
- **Edge cases:** similarly, removing `srt` from PATH must SKIP the task 021 probe (which requires the live `AGENT_BUILDER_LIVE_SRT=1` harness). Removing `gh` must SKIP task 034. Each individual prereq absence must SKIP only the affected probe(s) and leave others unaffected.

### TC-044-03: evidence file has the expected paste-ready structure

- **Requirement:** REQ-044-03
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with all prerequisites satisfied. After the run, read the evidence file written to disk (path is either documented or printed by the script).
- **Expected output:** the evidence file contains exactly 10 rows (one per task ID in the closing order). Each row is structured with the following fields, in a format suitable for pasting into the `Verified by` column of `coverage-tracker.md`: task ID, probe command (the exact command the checklist specifies), verbatim final output line (in dry-run mode this is a placeholder such as `[dry-run: not executed]`), and status (`PASS`, `SKIP`, or `FAIL`). The file format is consistent across all 10 rows (same delimiter / field structure). No row is missing; no extra rows appear.
- **Edge cases:** when a probe is SKIP, its evidence row must still appear in the file (with status `SKIP` and the skip reason in the output-line field). A missing row for a SKIP-ped probe would break the paste-ready contract.

### TC-044-04: preflight gate — harness refuses to run probes when preflight is NOT READY

- **Requirement:** REQ-044-04
- **Mechanism:** run `scripts/l6-probe.sh` (without `--dry-run`) in an environment where `scripts/l6-preflight.sh` (task 043) would report `NOT READY` (e.g. `podman` absent from PATH). The harness must call the preflight script first and check its exit code.
- **Expected output:** the harness exits non-zero and prints a message indicating it refused to run probes because preflight was NOT READY. No probe commands are invoked. The error message references the preflight script (or instructs the operator to run `make l6-preflight` first).
- **Edge cases:** `--dry-run` may bypass the preflight gate (acceptable, since dry-run does not invoke real commands). The spec does not require `--dry-run` to call preflight. The test for this case uses the non-dry-run path with a faked-PATH environment.

### TC-044-05: make l6-probe target exists, is in .PHONY, and is not wired into make check

- **Requirement:** REQ-044-01 (Makefile surface)
- **Mechanism:** run `make --dry-run l6-probe` and inspect the Makefile's `check:` and `fitness:` prerequisite lists.
- **Expected output:** the dry-run output shows the invocation of `scripts/l6-probe.sh`; `l6-probe` appears in `.PHONY`; neither `check:` nor `fitness:` lists `l6-probe` as a prerequisite (it is an operator-invoked diagnostic, not a gate).
- **Edge cases:** confirm `l6-preflight` (task 043) also does not appear in `check:` or `fitness:` — both new targets are diagnostic-only.

## Post-implementation verification

- [ ] All test cases above pass via `--dry-run` + faked PATH (L5)
- [ ] `make --dry-run l6-probe` shows the script invocation
- [ ] L6 residual documented: run without `--dry-run` on a fully provisioned host; evidence file used to produce `verify:` commits (human-reviewed)

## Test framework notes

Framework: bash integration tests with stub binaries on a temp PATH, matching the approach for task 043. The `--dry-run` flag is the primary testability seam: it exercises ordering, gating, and evidence-file formatting without live probes. The evidence file path must be deterministic or printed to stdout so the test harness can locate and parse it. No live Podman, runsc, srt, claude, or gh required for L5. The only L6 residual is running the script for real on a provisioned host and then using its evidence output as the basis for `verify:` commits (those commits remain a human-reviewed step).
