# Task 046: l6-probe.sh live-run wiring fixes

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix three wiring bugs in `scripts/l6-probe.sh` that prevent its live-run path from passing real contracts: (1) `--gate-tools ""` is passed to `run.sh`, causing it to die with "Gate toolchain directory does not exist"; (2) probes 034 and 032 omit `AGENT_BUILDER_PUBLISH_REMOTE`, causing publication failures on required env; (3) probes 028 and 032 set the removed `AGENT_BUILDER_SANDBOX_RUNTIME=srt`, triggering the ADR 021 migration error from `ConfigFromEnv`. No new behavior is introduced — all three fixes make the harness conform to contracts that already exist.

## Context

- **Tech stack:** bash shell script (`scripts/l6-probe.sh`); no Go changes.
- **No ADR needed:** all three changes bring the harness into conformance with existing published contracts (`run.sh` defaults, `configuration.md` REQUIRED vars, ADR 021 migration error). No new design decisions are made.
- **Bug 1 — gate-tools `""`:** `run.sh` lines ~275, 295, 317, 432 pass `--gate-tools ""`. The `run.sh` default for `--gate-tools` is `containment/execution-box/gate-tools` (line ~49: `gate_tools="${EXEC_BOX_GATE_TOOLS:-$box_dir/gate-tools}"`). The fix: resolve the same default in `l6-probe.sh` and pass it. Honor `EXEC_BOX_GATE_TOOLS` when set. The display strings `CMD_014`, `CMD_015`, `CMD_016`, `CMD_033` keep showing `<gate-tools-dir>` as a human-readable placeholder; only the actual argv changes.
- **Bug 2 — `AGENT_BUILDER_PUBLISH_REMOTE` missing:** `docs/spec/configuration.md` lists `AGENT_BUILDER_PUBLISH_REMOTE` as REQUIRED for the publisher; its absence causes publication failure. The fix: thread `AGENT_BUILDER_PUBLISH_REMOTE` from the calling environment into the actual argv for probes 034 and 032. When `AGENT_BUILDER_PUBLISH_REMOTE` is unset, apply SKIP discipline (status SKIP, reason `AGENT_BUILDER_PUBLISH_REMOTE unset`, exit 0) — identical to how the script already handles absent `gh`/`git-remote`.
- **Bug 3 — stale `AGENT_BUILDER_SANDBOX_RUNTIME=srt`:** ADR 021 removed this env var; `internal/runtime/run.go` line ~83 now calls `return Config{}, fmt.Errorf("run config: %s was removed…")` when it is set non-empty. Probes 028 and 032 set it, so they die immediately with the migration error. The fix: drop `AGENT_BUILDER_SANDBOX_RUNTIME=srt` from the argv of probes 028 and 032 entirely. Also drop any `HAS_SRT` gating logic on probe 028 that exists solely to gate on this removed var (028 is gated only on `claude`; `srt` was never a real prerequisite for 028's workload — it was only there because the stale var needed `srt`). Probe 021 keeps its `HAS_SRT` gate (the live srt harness still requires `srt`).
- **Reference:** `internal/runtime/run.go` `EnvSandboxRuntime` constant and `ConfigFromEnv` error message; `docs/architecture/decisions/021-sandbox-runtime-adapter.md` (removed var origin); ADR 026 / tasks 035/036 (Podman is now the containment path).
- **Dependency:** independent of task 045 (can run in parallel). Both 045 and 046 must be merged before the live re-run that exercises the full probe suite.
- **Model tier: balanced (sonnet)** — mechanical bug fixes against a fixed contract.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-046-01 | Every invocation of `run.sh` in probes 014, 015, 016, and 033 passes a resolved, non-empty `--gate-tools` path (default: `containment/execution-box/gate-tools`, honoring `EXEC_BOX_GATE_TOOLS` when set); `--gate-tools ""` must never appear in the actual argv | must have |
| REQ-046-02 | Probes 034 and 032 thread `AGENT_BUILDER_PUBLISH_REMOTE` from the environment; when `AGENT_BUILDER_PUBLISH_REMOTE` is unset, both probes are marked `SKIP` with reason `AGENT_BUILDER_PUBLISH_REMOTE unset` and exit 0 (SKIP discipline identical to absent-gh SKIP) | must have |
| REQ-046-03 | `AGENT_BUILDER_SANDBOX_RUNTIME=srt` is removed from the argv of probes 028 and 032; any `HAS_SRT` prerequisite gating on probe 028 that was solely due to the removed var is also removed; probe 021's `HAS_SRT` gate is preserved | must have |
| REQ-046-04 | Existing TC-044-01 through TC-044-05 remain green after all three fixes | must have |

## Readiness gate

- [x] Test spec `046-l6-probe-wiring-fixes-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] No blocking dependencies (independent of task 045)

## Acceptance criteria

- [ ] [REQ-046-01] TC-046-01: `--dry-run` output does NOT contain `--gate-tools ""`; the resolved gate-tools arg in the actual argv is non-empty; with `EXEC_BOX_GATE_TOOLS=/tmp/test-gate-tools`, that value appears in the actual argv for probes 014/015/016/033
- [ ] [REQ-046-02] TC-046-02 Part A: with `AGENT_BUILDER_PUBLISH_REMOTE` set, probes 034 and 032 thread the value in their actual argv (verified via `--dry-run` stdout or evidence file)
- [ ] [REQ-046-02] TC-046-02 Part B: with `AGENT_BUILDER_PUBLISH_REMOTE` unset, probes 034 and 032 show `SKIP` with reason naming the unset var; exit code is 0
- [ ] [REQ-046-03] TC-046-03: `bash scripts/tests/l6-probe-test.sh` finds zero occurrences of `AGENT_BUILDER_SANDBOX_RUNTIME=srt` in `--dry-run` stdout and evidence file; with `srt` absent and `claude` present, probe 028 is NOT skipped; probe 021 is still skipped when `srt` absent
- [ ] [REQ-046-04] TC-046-04: all TC-044-01 through TC-044-05 sub-cases still pass after the fixes; `make check` passes

## Verification plan

- **Highest level achievable:** L5 — bash `--dry-run` integration tests added to the existing `scripts/tests/l6-probe-test.sh` harness; all TC-044 and TC-046 cases pass; no live host required.
- **L5 harness command:**
  ```
  bash scripts/tests/l6-probe-test.sh
  ```
  Expected final assertion: `=== Results: N passed, 0 failed ===` exit 0 (where N includes all TC-044 and TC-046 cases)
- **L6 residual (operator-only, after task 045 is also merged):** run `scripts/l6-probe.sh` (without `--dry-run`) on a provisioned host. Confirm probes 014/015/016/033 pass the resolved `--gate-tools` path to `run.sh` without the "Gate toolchain directory does not exist" error. Confirm probes 034/032 skip gracefully when `AGENT_BUILDER_PUBLISH_REMOTE` is unset. Confirm probes 028/032 no longer trigger the ADR 021 migration error.
- **Cross-module state risk:** none — pure `l6-probe.sh` change; no Go code touched.
- **Runtime-visible surface:** `--dry-run` stdout (argv changes visible in status lines), evidence file (SKIP rows for unset `AGENT_BUILDER_PUBLISH_REMOTE`).

## Out of scope

- Changing the probe commands themselves beyond fixing the three named bugs (gate-tools, publish remote, srt var).
- Fixing the storage-opt failure in `run.sh` — that is task 045.
- Adding new probes or changing probe ordering.
- Any Go code changes.
- Updating `coverage-tracker.md` rows for task 044's live-run evidence — that remains an operator step after 045+046 are both merged and a live run is done.

## Notes

- **Bug 1 resolution detail:** resolve `GATE_TOOLS_DIR="${EXEC_BOX_GATE_TOOLS:-${REPO_ROOT}/containment/execution-box/gate-tools}"` near the top of the script (after `REPO_ROOT` is computed) and substitute `$GATE_TOOLS_DIR` in place of `""` in each `run_probe` actual argv. The `CMD_*` display strings keep the human-readable `<gate-tools-dir>` placeholder unchanged.
- **Bug 2 resolution detail:** add an `AGENT_BUILDER_PUBLISH_REMOTE` check alongside the existing `HAS_GH` and `HAS_GIT_REMOTE` checks for probes 034 and 032. The SKIP reason string `AGENT_BUILDER_PUBLISH_REMOTE unset` must be distinct from the existing `no git remote configured` reason (they are different conditions — a configured git remote and a configured publish remote are separate).
- **Bug 3 resolution detail:** in the `run_probe "028" …` and `run_probe "032" …` calls, remove `AGENT_BUILDER_SANDBOX_RUNTIME=srt` from the actual argv array. In the probe 028 SKIP logic block, remove the `HAS_SRT` check and any `SKIP_028` assignment that sets the skip reason to srt absence (that gating only existed for the removed var). Leave the `HAS_SRT` check in the probe 021 block untouched. Verify by grepping `internal/runtime/run.go` for `EnvSandboxRuntime` to confirm the exact error condition that must no longer be triggered.
- The coverage-tracker note for task 044 need not be changed — its live-run evidence is still pending, and tasks 045+046 are the precondition for getting that evidence. The note already reads "L6 residual: run `make l6-probe` on provisioned host."
