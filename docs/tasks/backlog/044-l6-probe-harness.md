# Task 044: L6 probe harness and evidence collector

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

L6 is currently 100% manual — provisioning a host and running 9 probes by hand, copy-pasting outputs into the tracker. This task automates everything that does NOT require the operator's live host or credentials: the ordering, gating, and evidence-file production for all 9 L6 probes. When the operator IS on a provisioned host the probe run is one command (`make l6-probe`) rather than ten manual steps.

Deliver `scripts/l6-probe.sh` and a `make l6-probe` target. The script runs the 9 probes in the checklist's prescribed closing order, gates each on its prerequisites (skipping gracefully when tooling is absent), captures output, and writes a structured evidence file ready to paste into `coverage-tracker.md`. It calls `scripts/l6-preflight.sh` (task 043) first and refuses to run real probes if the host is NOT READY.

**Important:** this task does NOT itself promote 🟡 → ✅ rows or make `verify:` commits — that stays a human-reviewed step per the "no unattended self-modification" invariant. The harness only produces the evidence the operator reviews and then commits.

## Context

- Tech stack: bash shell script + Makefile target, no new Go code.
- Governing document: `docs/plans/phase0-l6-verification-checklist.md` — the 9 probes, their exact commands, success criteria, and the closing order are the authoritative source.
- Closing order (from checklist section "Closing order"):
  1. 014 — `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe`
  2. 015 — `containment/execution-box/run.sh --gate-tools <dir> --worktree . --egress-probe`
  3. 016 — `containment/execution-box/run.sh --gate-tools <dir> --worktree . --runtime runsc --probe`
  4. 021 — `env AGENT_BUILDER_LIVE_SRT=1 ... go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003`
  5. 030 — ledger update (record that 014/015/016/021 are green; the "probe" is reviewing their outputs)
  6. 022 — `env AGENT_BUILDER_CLAUDE_CLI=claude go run ./cmd/agent-builder run ...`
  7. 028 — `env AGENT_BUILDER_CLAUDE_CLI=claude AGENT_BUILDER_SANDBOX_RUNTIME=srt go run ./cmd/agent-builder run --task-root docs/tasks/...`
  8. 033 — `containment/execution-box/run.sh --gate-tools <dir> --worktree . --probe` (gate-in-box)
  9. 034 — `env AGENT_BUILDER_PUBLISH_REMOTE=<remote> AGENT_BUILDER_GH_CLI=gh ... go test -count=1 -v ./tests/publisher -run TestBranchPRPublication`
  10. 032 — `env AGENT_BUILDER_CLAUDE_CLI=claude AGENT_BUILDER_SANDBOX_RUNTIME=srt ... go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance`

  Note: the checklist lists 9 tasks but 10 probe invocations because task 030 is a ledger update, not a standalone binary probe. The evidence file has one row per task (9 rows total); task 030's row records the ledger-update observation.

- Gating rules (per-probe prerequisites):
  - 014, 015, 016, 033: require `podman` on PATH and rootless Podman
  - 016: also requires `runsc`
  - 021: requires `srt` (and the snap-confine blocker is a SKIP reason, not FAIL)
  - 022, 028: require authenticated `claude` CLI
  - 034: requires `gh` (authenticated) and a configured git remote
  - 032: requires all of the above (capstone)
  - 030: requires 014, 015, 016, 021 to have all run (not skipped)
- Testability constraint: ordering, gating (SKIP-when-prereq-absent), and evidence-file formatting MUST be unit-testable via `--dry-run` + faked PATH with NO live host. The `--dry-run` flag exercises the full script logic without invoking any real probe commands.
- Dependency: calls `scripts/l6-preflight.sh` (task 043) as its first step. Task 043 must be complete before this task can be implemented.
- **Model tier: balanced (sonnet)** — mechanical shell scripting against a fixed probe list.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-044-01 | `scripts/l6-probe.sh` runs (or in `--dry-run` simulates) all 9 probes in the exact closing order from the checklist; a `make l6-probe` target wraps it; the target is in `.PHONY` and NOT a prerequisite of `make check` or `make fitness` | must have |
| REQ-044-02 | Each probe is gated on its prerequisites; if any prerequisite is absent the probe is marked `SKIP` with a recorded reason and execution continues; a SKIP is not a FAIL and does not cause a non-zero exit | must have |
| REQ-044-03 | After each run (real or `--dry-run`), the script writes a structured evidence file with one row per task (9 rows) containing: task ID, probe command, verbatim final output line (or `[dry-run: not executed]`), and status (`PASS`/`SKIP`/`FAIL`); the format is paste-ready for the `Verified by` column of `coverage-tracker.md` | must have |
| REQ-044-04 | The script calls `scripts/l6-preflight.sh` before running any real probes and refuses (exits non-zero with an informative message) if preflight is NOT READY; `--dry-run` may bypass the preflight gate | must have |
| REQ-044-05 | TC-044-01 through TC-044-04 all pass using only `--dry-run` and stub binaries on a temp PATH, with no live host tooling required | must have |

## Readiness gate

- [x] Test spec `044-l6-probe-harness-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 043 complete (preflight script must exist for the gating integration)

## Acceptance criteria

- [ ] [REQ-044-01] `scripts/l6-probe.sh --dry-run` prints exactly 9 rows in closing order (014, 015, 016, 021, 030, 022, 028, 033, 034, 032); each row identifies the task ID and the probe command from the checklist; exit 0
- [ ] [REQ-044-01] `make l6-probe` invokes the script; `l6-probe` is in `.PHONY`; the target is NOT in `make check` or `make fitness` prerequisites
- [ ] [REQ-044-02] With `runsc` absent from a faked PATH and `--dry-run`, task 016's row shows `SKIP` with a reason; all other rows (that don't require `runsc`) are unaffected; exit 0
- [ ] [REQ-044-02] With `srt` absent and `--dry-run`, task 021's row shows `SKIP`; exit 0
- [ ] [REQ-044-03] The evidence file written after `--dry-run` has exactly 9 rows; each row includes task ID, probe command, output-line field (`[dry-run: not executed]`), and status; SKIP rows are present in the file with their skip reason; the format is consistent across all rows
- [ ] [REQ-044-04] Running `scripts/l6-probe.sh` (without `--dry-run`) against a faked NOT READY environment exits non-zero and prints a message directing the operator to `make l6-preflight` (or equivalent); no probe commands are invoked
- [ ] [REQ-044-05] TC-044-01 through TC-044-04 all pass with stub binaries and `--dry-run`, no live host required

## Verification plan

- **Highest level achievable without a live host:** L5 — bash integration tests using `--dry-run` + faked PATH prove ordering, gating, and evidence-file format (TC-044-01 through TC-044-04).
- **L5 harness command:** run the test script (e.g. `bash scripts/tests/l6-probe-test.sh --dry-run`) with stub binaries; all TC-044 cases pass; exit 0.
- **L6 residual (operator-only):** run `make l6-probe` on a real provisioned host with live podman/runsc/srt/claude/gh and a configured git remote. Observe the evidence file produced. Use the evidence file to author `verify:` commits (one per task, human-reviewed). This step requires operator presence and cannot be automated.
- **Cross-module state risk:** none on the code side. The evidence file is written to a documented output path; the operator decides whether and how to commit its contents.
- **Runtime-visible surface:** the evidence file (paste-ready rows) and the per-probe PASS/SKIP/FAIL status lines on stdout.

## Out of scope

- Promoting 🟡 → ✅ rows in `coverage-tracker.md` or making `verify:` commits — those remain human-reviewed steps per the "no unattended self-modification" invariant.
- Installing any required tools — operator responsibility.
- Changing the probe commands themselves — they are fixed by the checklist.
- Any Go code changes.
- CI integration — both l6-preflight and l6-probe are operator-invoked diagnostics, not CI gates.

## Notes

- The evidence file location should be documented in the script's `--help` output (e.g. `l6-evidence-$(date +%Y%m%d-%H%M%S).txt` in the repo root, or a fixed path like `docs/plans/l6-evidence.txt`). A fixed path is simpler for tests; a timestamped path avoids accidental overwrite. Pick one and document it.
- Task 030 ("runtime isolation evidence") is a ledger-update probe, not a binary invocation. Its row in the evidence file records the observation that tasks 014/015/016/021 are green (or summarises their results). If any of 014/015/016/021 are SKIP, task 030 must also be SKIP.
- The operator-only residual (authenticating claude/gh, configuring a git remote, installing podman/runsc/srt) stays with the human by necessity — this task only automates what is host-independent.
