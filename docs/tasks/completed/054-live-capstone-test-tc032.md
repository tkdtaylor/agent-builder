# Task 054: live capstone test (TC-032) + fixture helper

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** done

## Goal

Add a real, env-gated live end-to-end capstone test that drives the built `agent-builder` binary with a real Claude executor, real `git`/`gh`, and real Podman containment against the private `l6` sandbox remote, closing the L6 gap for task 032 (Phase 0 end-to-end acceptance). Includes a `newLiveCapstoneFixture(t)` helper that seeds a minimal real git worktree and task-root. The test skips cleanly in CI when the env flag is unset.

## Context

- The existing `TestPhase0EndToEndAcceptance` uses fake shims and a faked URL — it is the deterministic L5 gate and **must not be modified**.
- This task adds `tests/e2e/live_phase0_e2e_test.go` with `TestLivePhase0EndToEndAcceptance_TC032`, gated on `AGENT_BUILDER_LIVE_E2E=1`.
- `newLiveCapstoneFixture(t)` seeds: a temp task-root (roadmap + one `ready` task that instructs Claude to create `LIVE_OK.txt` with one line) + a temp real `git init` worktree with a minimal gate-passing Go module (same shape as `tests/e2e/branch_pr_publication_test.go:143-154`).
- The test drives the real built binary via `runAgentBuilder(t, binary, fixture.env(), "run")` — same helper used by the existing e2e tests.
- ADR 031 (task 052) establishes that `claude`/gate/publisher run host-side; the Podman box runs only `/bin/true` as a liveness probe. The live capstone is feasible because Claude is on the host.
- **Model tier: fast (sonnet)** — wiring a live e2e test with correct fixture seeding, skip/fatal discipline, and cleanup.
- **Dependency:** task 052 (ADR 031 written). Independent of task 053.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-054-01 | `tests/e2e/live_phase0_e2e_test.go` contains `TestLivePhase0EndToEndAcceptance_TC032`; when `AGENT_BUILDER_LIVE_E2E=1` and all prereqs present: calls `newLiveCapstoneFixture(t)`, drives real binary with full env contract, asserts exit 0 + `run completed: task NNN` in stdout + `publication recorded: branch=` event in run record + `run_finished outcome=completed`; `t.Log` real PR URL; `t.Cleanup` closes PR + deletes remote branch | must have |
| REQ-054-02 | When `AGENT_BUILDER_LIVE_E2E` is unset or empty: `t.Skip`; when any tool prereq absent (`claude`, `git`, `gh`, `podman`) or `ANTHROPIC_API_KEY` unset: `t.Skipf` naming the specific missing prereq; all subcases → exit 0, no binary invoked | must have |
| REQ-054-03 | When `AGENT_BUILDER_LIVE_E2E=1` and all tool prereqs present but the gate-tools directory is missing (or `EXEC_BOX_GATE_TOOLS` names a nonexistent path): `t.Fatalf` (not `t.Skipf`) naming the missing gate-tools dir — this is a config error, not a prereq skip | must have |
| REQ-054-04 | `go test ./tests/e2e` (without live flag) stays green: all existing tests PASS, `TestLivePhase0EndToEndAcceptance_TC032` SKIP, no compilation error; `make check` passes | must have |

## Readiness gate

- [x] Test spec `054-live-capstone-test-tc032-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Task 052 merged (ADR 031 written, host-side architecture documented)

## Acceptance criteria

- [x] [REQ-054-01] TC-054-01: test body per spec — `newLiveCapstoneFixture(t)` helper exists; real binary driven with full env contract; exit 0 asserted; `run completed: task NNN` in stdout; `publication recorded: branch=` in run-record stdout event; `run_finished outcome=completed` in run record; `t.Log` real PR URL; `t.Cleanup` closes PR + deletes remote branch
- [x] [REQ-054-02] TC-054-02: flag unset → `t.Skip` + exit 0; any of `claude`/`git`/`gh`/`podman` absent → `t.Skipf` naming the missing tool + exit 0; `ANTHROPIC_API_KEY` unset → `t.Skipf` naming the var + exit 0
- [x] [REQ-054-03] TC-054-03: gate-tools dir missing with live flag set and all tools present → `t.Fatalf` (not `t.Skipf`), non-zero
- [x] [REQ-054-04] TC-054-04: `go test ./tests/e2e` without live flag exits 0; `TestPhase0EndToEndAcceptance` + `TestPhase1EndToEndAcceptance` still PASS; `make check` exits 0

## Verification plan

- **Highest level achievable in-repo:** L5 — the test skips cleanly when `AGENT_BUILDER_LIVE_E2E` is unset; `go test ./tests/e2e` stays green.
- **L5 harness command:**
  ```
  go test -count=1 -v ./tests/e2e
  ```
  Expected: `TestPhase0EndToEndAcceptance --- PASS`, `TestPhase1EndToEndAcceptance --- PASS`, `TestLivePhase0EndToEndAcceptance_TC032 --- SKIP`; exit 0.
- **L6 residual (operator-only, provisioned host):**
  ```
  env AGENT_BUILDER_LIVE_E2E=1 \
      AGENT_BUILDER_LIVE_E2E_REMOTE=l6 \
      ANTHROPIC_API_KEY=<from .env> \
    go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  ```
  Expected: `--- PASS TestLivePhase0EndToEndAcceptance_TC032`; run record shows `run_finished outcome=completed` and `publication recorded: branch=task/NNN-live-*`; real PR URL logged; cleanup confirmed (`gh pr list` shows the branch gone).
- **Cross-module state risk:** none — new test file in `tests/e2e/`; no Go source or spec files changed.
- **Runtime-visible surface:** `stdout` output from the real binary; run-record NDJSON events; real PR on `l6` sandbox (cleaned up by `t.Cleanup`).

## Out of scope

- Modifying `tests/e2e/phase0_end_to_end_acceptance_test.go` or `tests/e2e/phase1_end_to_end_acceptance_test.go` (existing L5 fake tests).
- Changes to `internal/` or `cmd/` — no production code changes.
- `docs/spec/` changes — no externally-visible behavior changes.
- The live publisher-only test (that is task 053).
- `scripts/l6-probe.sh` rewiring (that is task 055).

## Notes

- `newLiveCapstoneFixture(t)` seeds a Go module that must compile and pass `go test ./...` so the gate step (which the real binary runs) exits 0. Use a simple non-trivial module: `go.mod` + a `.go` source file with one exported function + a `_test.go` file that tests it — identical to the shape in `tests/e2e/branch_pr_publication_test.go:143-154`.
- The fixture task file goal: `"Create the file LIVE_OK.txt in the worktree with exactly one line: \"live probe ok\"."` — this is a deterministic, low-token workload for Claude.
- The task file must use `**Status:** ready` so `tasksource.Source.Next()` picks it up (per `internal/tasksource/source.go:186-201` `normalizeStatus`).
- Run `git init && git add -A && git commit -m "initial"` in the fixture worktree so the publisher has a clean tree to branch from.
- The exec-box launcher path: resolve via `filepath.Join(repoRoot, "containment/execution-box/run.sh")` where `repoRoot` is two `filepath.Dir()` calls above the test file's location (same pattern as `buildAgentBuilder` resolves the binary).
- Gate-tools dir check: use `exec.LookPath` or `os.Stat` to confirm the gate-tools dir exists before starting; `t.Fatalf` immediately if absent with a clear message.
- `t.Cleanup` branch extraction: scan the run record (NDJSON) for a line whose `data` field contains `publication recorded: branch=`; extract everything after `branch=` up to the next space or end-of-field.
