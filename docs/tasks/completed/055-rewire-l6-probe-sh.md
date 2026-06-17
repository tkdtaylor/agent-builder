# Task 055: rewire `scripts/l6-probe.sh` (022/028/032/034) + fixture-seed helper

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Update `scripts/l6-probe.sh` so that probes 022, 028, 034, and 032 invoke the correct live commands introduced by tasks 053/054, and add a `seed_live_fixture()` bash helper that seeds a minimal temp task-root and git worktree for the `go run` probes (022/028). The existing `scripts/tests/l6-probe-test.sh` harness must stay fully green.

Four concrete changes:

1. **`seed_live_fixture()` helper** — emits a temp task-root (`docs/plans/roadmap.md` + one `**Status:** ready` task) and a temp real `git init` worktree with a minimal gate-passing Go module; sets `AGENT_BUILDER_TASK_ROOT` and `AGENT_BUILDER_WORKTREE` shell variables.
2. **Probes 022/028** — full env contract (`ANTHROPIC_API_KEY`, `AGENT_BUILDER_TASK_ROOT=<fixture>`, `AGENT_BUILDER_WORKTREE=<fixture>`, `AGENT_BUILDER_PUBLISH_REMOTE`, `AGENT_BUILDER_RUN_TIMEOUT=300s`, `AGENT_BUILDER_MAX_ATTEMPTS=1`, `AGENT_BUILDER_RUN_RECORD=<tmp>`); drop the invalid `--task-root` arg; gate adds an `ANTHROPIC_API_KEY` presence skip.
3. **Probe 034** — `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=$remote go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034`.
4. **Probe 032** — `env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=$remote AGENT_BUILDER_PUBLISH_REMOTE=$remote go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032` (keep `AGENT_BUILDER_PUBLISH_REMOTE` in the env prefix so TC-046-02's existing grep still matches).

The closing order (014 → 015 → 016 → 021 → 030 → 022 → 028 → 033 → 034 → 032), the 10 evidence rows, the no-srt assertion (TC-046-03), and PUBLISH_REMOTE threading (TC-046-02) are all preserved.

## Context

- **Tech stack:** bash shell script (`scripts/l6-probe.sh`, `scripts/tests/l6-probe-test.sh`); no Go changes.
- **No ADR needed** — this is mechanical wiring to conform the harness to the commands introduced by tasks 053/054 and the env contract from `internal/runtime/run.go:80-148`.
- The `go run ./cmd/agent-builder run` path (022/028) requires `ANTHROPIC_API_KEY` (proxied to `ANTHROPIC_API_KEY` by `executor.ClaudeCLIAuthEnv`). When the key is absent, the probe must SKIP (not FAIL) — same discipline as existing missing-tool skips.
- `seed_live_fixture()` must not use the main repo's `docs/tasks/` as the task-root — the 028 probe must drive a standalone minimal fixture, not the real backlog.
- **Model tier: balanced (sonnet)** — surgical wiring to a fixed contract with test-coverage discipline.
- **Dependencies:** task 052 (doc convention established) + task 053 (`TestLiveBranchPRPublication_TC034` exists) + task 054 (`TestLivePhase0EndToEndAcceptance_TC032` exists). Do last.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-055-01 | Probes 022 and 028 carry the full env contract via a seeded fixture; no `--task-root` arg anywhere in their `go run ./cmd/agent-builder run` argv; `ANTHROPIC_API_KEY` absence → SKIP with named reason; no `AGENT_BUILDER_SANDBOX_RUNTIME` in their env prefix | must have |
| REQ-055-02 | Probe 034 argv: `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=$remote go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034`; `AGENT_BUILDER_PUBLISH_REMOTE` also present in the env prefix (TC-046-02 regression invariant) | must have |
| REQ-055-03 | Probe 032 argv: `env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=$remote AGENT_BUILDER_PUBLISH_REMOTE=$remote go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032`; no `AGENT_BUILDER_SANDBOX_RUNTIME=srt` (TC-046-03 regression invariant) | must have |
| REQ-055-04 | `seed_live_fixture()` bash helper: creates a temp task-root with `docs/plans/roadmap.md` + one `**Status:** ready` task file + a temp real `git init` worktree with a minimal Go module; sets `AGENT_BUILDER_TASK_ROOT` and `AGENT_BUILDER_WORKTREE` shell variables to the created paths | must have |
| REQ-055-05 | `bash scripts/tests/l6-probe-test.sh` → `=== Results: N passed, 0 failed ===` exit 0: all TC-044 and TC-046 cases preserved; closing order 014→015→016→021→030→022→028→033→034→032; TC-046-02 (PUBLISH_REMOTE threading) still passes (update grep literal if it moved but preserve the invariant); TC-046-03 (no srt) still passes; `bash scripts/l6-probe.sh --dry-run` → 10 rows in closing order, exit 0 | must have |

## Readiness gate

- [x] Test spec `055-rewire-l6-probe-sh-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Task 052 merged
- [ ] Task 053 merged (`TestLiveBranchPRPublication_TC034` exists in `tests/publisher/`)
- [ ] Task 054 merged (`TestLivePhase0EndToEndAcceptance_TC032` exists in `tests/e2e/`)

## Acceptance criteria

- [ ] [REQ-055-01] TC-055-01: 022/028 argv has no `--task-root`; env prefix has full contract vars; `AGENT_BUILDER_SANDBOX_RUNTIME` absent; `ANTHROPIC_API_KEY` unset → SKIP with reason, exit 0
- [ ] [REQ-055-02] TC-055-02: 034 argv contains `TestLiveBranchPRPublication_TC034` + `AGENT_BUILDER_LIVE_PUBLISH=1` + `AGENT_BUILDER_PUBLISH_REMOTE`
- [ ] [REQ-055-03] TC-055-03: 032 argv contains `TestLivePhase0EndToEndAcceptance_TC032` + `AGENT_BUILDER_LIVE_E2E=1` + `AGENT_BUILDER_PUBLISH_REMOTE` + no `AGENT_BUILDER_SANDBOX_RUNTIME=srt`
- [ ] [REQ-055-04] TC-055-04: `seed_live_fixture()` creates task-root with roadmap.md + one ready task + worktree with `.git/` and `go.mod`; `AGENT_BUILDER_TASK_ROOT` and `AGENT_BUILDER_WORKTREE` set to temp dirs (not main-repo paths)
- [ ] [REQ-055-05] TC-055-05: `bash scripts/tests/l6-probe-test.sh` → all TC-044 + TC-046 + TC-055 cases PASS, 0 failed; `bash scripts/l6-probe.sh --dry-run` → 10 rows in closing order, exit 0; `make check` passes

## Verification plan

- **Highest level achievable:** L5 — `bash scripts/tests/l6-probe-test.sh` green (all cases PASS) + `bash scripts/l6-probe.sh --dry-run` 10 rows in order exit 0.
- **L5 harness commands:**
  ```
  bash scripts/tests/l6-probe-test.sh
  ```
  Expected: `=== Results: N passed, 0 failed ===` exit 0.
  ```
  bash scripts/l6-probe.sh --dry-run
  ```
  Expected: 10 rows in closing order (014 015 016 021 030 022 028 033 034 032), exit 0.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.` exit 0.
- **L6 residual (operator-only, provisioned host):**
  ```
  make l6-probe
  ```
  Expected: all 10 probes PASS (or SKIP for prerequisite-absent probes); evidence file written; no srt errors, no config errors.
- **Cross-module state risk:** none — pure `scripts/l6-probe.sh` + `scripts/tests/l6-probe-test.sh` changes; no Go code touched.

## Out of scope

- Changes to `internal/` or `cmd/` — no production code changes.
- Adding new probes or changing the 10-step closing order.
- Changing the gate-tools wiring (REQ-046-01, already done in task 046).
- Any `tests/publisher/` or `tests/e2e/` changes (tasks 053/054).
- `docs/spec/` changes — no externally-visible behavior changes.

## Notes

- **`seed_live_fixture()` implementation:** use `mktemp -d` for both the task-root and the worktree. In the task-root, write `docs/plans/roadmap.md` (just `# Roadmap`) and `docs/tasks/backlog/001-fixture.md` with the standard task heading, `**Status:** ready`, and a simple goal. In the worktree, run `git init`, write `go.mod`, a `.go` source file, and a `_test.go` file (same shape as in `tests/e2e/branch_pr_publication_test.go:143-154`), then `git add -A && git commit -m "initial"`.
- **`ANTHROPIC_API_KEY` skip:** add this check alongside the existing `HAS_CLAUDE` check for probes 022/028. The SKIP reason must name `ANTHROPIC_API_KEY` specifically (e.g. `ANTHROPIC_API_KEY unset`).
- **TC-046-02 regression for 034/032:** the existing `l6-probe-test.sh` TC-046-02 assertion greps for `AGENT_BUILDER_PUBLISH_REMOTE` in the 034/032 argv. After the rewire, probe 034 uses `AGENT_BUILDER_LIVE_PUBLISH_REMOTE` as the primary remote flag AND `AGENT_BUILDER_PUBLISH_REMOTE` must also be in the env prefix (carry it explicitly). Probe 032 uses `AGENT_BUILDER_LIVE_E2E_REMOTE` AND `AGENT_BUILDER_PUBLISH_REMOTE`. The TC-046-02 grep must still find `AGENT_BUILDER_PUBLISH_REMOTE` — this is the load-bearing invariant the existing test checks. Update the test's grep literal only if the variable moved, but the invariant must survive.
- **`AGENT_BUILDER_RUN_RECORD`:** use a `mktemp` temp file for 022/028 so the run record doesn't collide with the main repo's files.
- **Cleanup:** `seed_live_fixture()` does not clean up (it is called from the probe block, not from a test framework that manages `t.Cleanup`). Temp dirs from `mktemp -d` are auto-cleaned by the OS at reboot. This is acceptable for an L6 operator-run script.
