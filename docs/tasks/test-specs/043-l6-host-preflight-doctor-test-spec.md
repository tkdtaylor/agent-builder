# Test Spec 043: L6 host preflight doctor

**Linked task:** [`docs/tasks/backlog/043-l6-host-preflight-doctor.md`](../backlog/043-l6-host-preflight-doctor.md)
**Written:** 2026-06-16
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-043-01 | TC-043-01 | ⏳ |
| REQ-043-02 | TC-043-02 | ⏳ |
| REQ-043-03 | TC-043-03 | ⏳ |
| REQ-043-04 | TC-043-04 | ⏳ |
| REQ-043-05 | TC-043-05 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-043-01: all prerequisites present — READY exit 0

- **Requirement:** REQ-043-01
- **Mechanism:** run `scripts/l6-preflight.sh` with a faked PATH containing stub binaries that succeed for every required tool (`podman`, `runsc`, `bwrap`, `srt`, `claude`, `gh`), a stubbed `podman info` that prints `true`, a stubbed `git remote -v` that prints a non-empty remote listing, and no-op stubs for `make check` and `make fitness` that print the expected success lines.
- **Expected output:** every prerequisite row prints `PASS`; the report ends with `READY`; exit code is 0.
- **Faking approach:** place stub shell scripts on a temporary directory prepended to PATH, or source the detection logic from a helper function and override the lookup with environment variables or a configurable lookup wrapper, per REQ-043-05.

### TC-043-02: a missing tool — that row MISSING, overall NOT READY, exit non-zero

- **Requirement:** REQ-043-02
- **Mechanism:** same faked PATH as TC-043-01 except `runsc` is absent. Run `scripts/l6-preflight.sh`.
- **Expected output:** the `runsc` row shows `MISSING`; all other rows show `PASS`; the report ends with `NOT READY`; exit code is non-zero. No other rows are affected by the single missing tool.
- **Edge cases:** verify each of the seven required tools in turn (podman, runsc, bwrap, srt, claude, gh, git) can independently produce a MISSING row; the remaining rows stay PASS. Only one variant needs to be a concrete test case; the others are documented as equivalent variants that should each be exercised.

### TC-043-03: snap-confine srt blocker — srt row FAIL with specific hint

- **Requirement:** REQ-043-03
- **Mechanism:** provide a stub `srt` on PATH that exits non-zero and prints the blocker string `snap-confine has elevated permissions and is not confined`. Run `scripts/l6-preflight.sh`.
- **Expected output:** the `srt` row shows `FAIL` (distinct from `MISSING` — the binary is present but unusable); the remediation hint mentions either `snap-confine` or the word `snap` and suggests a non-snap install path; other rows remain `PASS`; exit is non-zero; the overall verdict is `NOT READY`.
- **Edge cases:** a stub `srt` that exits non-zero with a *different* error message should produce a generic `FAIL` row (not the snap-specific hint), so the hint is keyed specifically to the snap-confine output pattern.

### TC-043-04: rootless Podman check fails — podman row FAIL with remediation hint

- **Requirement:** REQ-043-04
- **Mechanism:** provide a stub `podman` binary and a stub `podman info` that prints `false` (rootless is false). Run `scripts/l6-preflight.sh`.
- **Expected output:** the `podman` row (or a dedicated `podman-rootless` row — whichever the implementation uses) shows `FAIL`; the remediation hint references rootless or `podman info`; other rows `PASS`; exit non-zero; verdict `NOT READY`.
- **Edge cases:** a stub `podman info` that exits non-zero entirely (Podman present but misconfigured) also produces a `FAIL` in the same row.

### TC-043-05: make l6-preflight target exists and invokes the script

- **Requirement:** REQ-043-01 (Makefile surface)
- **Mechanism:** run `make --dry-run l6-preflight` in the repo root and inspect its output.
- **Expected output:** the dry-run output shows the invocation of `scripts/l6-preflight.sh` (or an equivalent shell call), confirming the target exists and is wired up. The target must also appear in `.PHONY`.
- **Edge cases:** `make l6-preflight` must not be a prerequisite of `make check` or `make fitness` — it is an operator-invoked diagnostic, not a gate (confirm by inspecting the `check:` and `fitness:` prerequisite lists).

## Post-implementation verification

- [ ] All test cases above pass via faked PATH / stub binaries (L5)
- [ ] `make --dry-run l6-preflight` shows the script invocation
- [ ] L6 residual documented: run on a real provisioned host with live podman/runsc/srt

## Test framework notes

Framework: bash integration tests (or a bats-like inline harness) — same approach as other shell-script tests in this repo where stub binaries on a temp PATH stand in for real system tools. The prerequisite-detection logic must expose a hook (configurable PATH, injectable lookup function, or env-var override) that makes faking possible without modifying PATH system-wide. The L5 tests are self-contained and require no host tooling beyond bash and the repo itself.
