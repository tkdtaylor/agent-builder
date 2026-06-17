# Task 043: L6 host preflight doctor

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

L6 is currently 100% manual — provisioning a host and running 9 probes by hand, copy-pasting outputs into the tracker. This task automates the host-readiness check (the prerequisite phase of the L6 checklist) so that when the operator IS on a provisioned host the readiness check is one command, not eight manual `command -v` invocations.

Deliver `scripts/l6-preflight.sh` and a `make l6-preflight` target. The script checks every prerequisite from `docs/plans/phase0-l6-verification-checklist.md`: tool presence, rootless Podman, git remote, and baseline gate health. It emits a single structured readiness report and exits non-zero if the host is NOT READY.

## Context

- Tech stack: bash shell script + Makefile target, no new Go code.
- Governing document: `docs/plans/phase0-l6-verification-checklist.md` — the prerequisite list (podman, runsc, bwrap, srt, claude, gh, git remote, rootless Podman, `make check` / `make fitness` green) is the authoritative source.
- Known blocker: if `srt` is installed via snap, it hits `snap-confine has elevated permissions and is not confined` — the script must detect this specific output and report it as a distinct FAIL with a remediation hint (install srt off snap).
- Testability constraint: the prerequisite-detection logic MUST be unit-testable without a live host. Design the script so PATH lookups and `podman info` output can be faked — for example, by accepting a configurable PATH prefix via an environment variable (`L6_PREFLIGHT_PATH`), sourcing detection helpers that can be overridden in test, or using stub-binary injection on a temporary PATH. The test spec (TC-043-01 through TC-043-04) requires this seam to exist.
- Pattern: mirrors the PASS/FAIL/MISSING output style and the operator-diagnostic stance of the existing execution-box probe scripts (`containment/execution-box/probe.sh`).
- **Model tier: balanced (sonnet)** — mechanical shell scripting against a fixed checklist.
- Dependencies: none (standalone diagnostic; does not depend on any other backlog task).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-043-01 | `scripts/l6-preflight.sh` checks all 8 prerequisites from the checklist: `command -v` for podman, runsc, bwrap, srt, claude, gh; `git remote -v` non-empty; `podman info` rootless == true; `make check` and `make fitness` exit 0. For each: emits `PASS`, `FAIL`, or `MISSING` with a one-line remediation hint on FAIL/MISSING | must have |
| REQ-043-02 | A missing tool produces a `MISSING` row and overall `NOT READY` verdict; exit non-zero | must have |
| REQ-043-03 | A `srt` that exits with the snap-confine blocker string produces a `FAIL` row with a snap-specific remediation hint (distinct from the generic FAIL path) | must have |
| REQ-043-04 | A `podman info` that prints `false` (or exits non-zero) produces a `FAIL` in the rootless-Podman check row with a remediation hint | must have |
| REQ-043-05 | The detection logic is injectable via faked PATH (e.g. `L6_PREFLIGHT_PATH` env var prepended to PATH, or equivalent seam) so TC-043-01 through TC-043-04 are achievable without a live host | must have |

## Readiness gate

- [x] Test spec `043-l6-host-preflight-doctor-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] No blocking dependencies (none)

## Acceptance criteria

- [ ] [REQ-043-01] `scripts/l6-preflight.sh` emits one row per prerequisite with `PASS`, `FAIL`, or `MISSING`; a FAIL/MISSING row includes a one-line remediation hint; the final line is `READY` or `NOT READY`
- [ ] [REQ-043-01] `make l6-preflight` invokes the script; `l6-preflight` is in `.PHONY`; the target is NOT a prerequisite of `make check` or `make fitness`
- [ ] [REQ-043-02] With any single required tool absent from PATH: that row is `MISSING`; other rows unaffected; exit non-zero; verdict `NOT READY`
- [ ] [REQ-043-03] With stub `srt` printing `snap-confine has elevated permissions and is not confined` and exiting non-zero: the `srt` row is `FAIL` with a snap-specific hint; other rows unaffected; exit non-zero
- [ ] [REQ-043-04] With stub `podman info` printing `false`: the rootless-Podman row is `FAIL` with a rootless hint; exit non-zero
- [ ] [REQ-043-05] TC-043-01 through TC-043-04 all pass using only stub binaries on a temp PATH, with no live host tooling required

## Verification plan

- **Highest level achievable without a live host:** L5 — bash integration tests with faked PATH prove all detection paths (TC-043-01 through TC-043-04).
- **L5 harness command:** run the test script (e.g. `bash scripts/tests/l6-preflight-test.sh` or equivalent) with stub binaries on a temp PATH; all TC-043 cases pass; exit 0.
- **L6 residual (operator-only):** run `make l6-preflight` on a real provisioned host with live podman/runsc/srt/claude/gh. Observe `READY` and exit 0. Record the final `READY` line in the tracker. This step requires operator presence and cannot be automated.
- **Cross-module state risk:** none — pure diagnostic, no state written.
- **Runtime-visible surface:** the script's stdout PASS/FAIL/MISSING rows and the final READY/NOT READY line.

## Out of scope

- Installing any of the required tools — the script only checks and reports; remediation is manual.
- Running the 9 L6 probes — that is task 044.
- Modifying `make check` or `make fitness` — the script only invokes them and inspects exit codes.
- Any Go code changes.

## Notes

- The snap-confine detection (REQ-043-03) is keyed to the exact error string documented in the checklist: `snap-confine has elevated permissions and is not confined`. A stub that prints something else should get a generic FAIL row, not the snap-specific hint.
- The `make check` and `make fitness` checks should be last in the output (they take the longest); tool presence checks come first so the operator gets the actionable signal early.
- `git remote -v` producing empty output (no remote configured) is a `MISSING`/`FAIL` condition distinct from git being absent.
