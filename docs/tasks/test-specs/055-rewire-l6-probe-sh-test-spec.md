# Test Spec 055: rewire `scripts/l6-probe.sh` (022/028/032/034) + fixture-seed helper

**Linked task:** [`docs/tasks/backlog/055-rewire-l6-probe-sh.md`](../backlog/055-rewire-l6-probe-sh.md)
**Written:** 2026-06-17
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-055-01 | TC-055-01 | ⏳ |
| REQ-055-02 | TC-055-02 | ⏳ |
| REQ-055-03 | TC-055-03 | ⏳ |
| REQ-055-04 | TC-055-04 | ⏳ |
| REQ-055-05 | TC-055-05 (regression) | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Context: existing test harness

The existing test harness lives at `scripts/tests/l6-probe-test.sh` and currently covers TC-044-01 through TC-044-05 and TC-046-01 through TC-046-04 (10 passing cases). All new TCs in this spec are added to that same file. The harness uses `make_probe_stub_dir` plus `L6_PROBE_PATH` / `L6_EVIDENCE_FILE` to inject stubs without touching the real PATH. New test functions follow the `run_tc055_NN()` naming convention and are appended to the existing file.

The closing order and the 10-row constraint are load-bearing invariants: they must be preserved exactly.

---

## Test cases

### TC-055-01: probe 022 and 028 carry the full env contract and no `--task-root` arg

- **Requirement:** REQ-055-01
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with a faked PATH (all prereq stubs present, including `claude`); capture stdout and the evidence file. Inspect the probe command strings for 022 and 028.
- **Assertions:**
  1. The actual argv for probe 022 and 028 does NOT contain `--task-root` anywhere (the `run` subcommand takes no positional args — `internal/cli/cli.go:104-106`).
  2. The actual argv or the env prefix for probe 022 and 028 references `AGENT_BUILDER_TASK_ROOT` (the env-var form, set from the `seed_live_fixture` helper output or from a script-level variable).
  3. The actual argv or the env prefix for probe 028 references `AGENT_BUILDER_WORKTREE`, `AGENT_BUILDER_PUBLISH_REMOTE`, `AGENT_BUILDER_RUN_TIMEOUT`, `AGENT_BUILDER_MAX_ATTEMPTS`, and `AGENT_BUILDER_RUN_RECORD`.
  4. Neither 022 nor 028 contains `AGENT_BUILDER_SANDBOX_RUNTIME` in their env prefix (the stale srt var must remain gone — REQ-046-03 regression).
  5. `ANTHROPIC_API_KEY` presence check: when `ANTHROPIC_API_KEY` is unset (`env -u ANTHROPIC_API_KEY`), probes 022 and 028 show `SKIP` with a reason referencing `ANTHROPIC_API_KEY`; exit code is 0.
- **Edge cases:**
  - `ANTHROPIC_API_KEY` unset must produce a SKIP (not FAIL) — same skip discipline as missing-tool prerequisites.
  - When `ANTHROPIC_API_KEY` is set but empty (`ANTHROPIC_API_KEY=`), the skip must also fire.

### TC-055-02: probe 034 uses `go test ./tests/publisher -run TestLiveBranchPRPublication_TC034`

- **Requirement:** REQ-055-02
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with a faked PATH; capture stdout and the evidence file. Inspect the command string for probe 034.
- **Assertions:**
  1. The actual argv for probe 034 contains `go test` + `-run TestLiveBranchPRPublication_TC034`.
  2. The actual argv for probe 034 contains `./tests/publisher`.
  3. The actual argv or env prefix for probe 034 contains `AGENT_BUILDER_LIVE_PUBLISH=1`.
  4. The actual argv or env prefix for probe 034 contains `AGENT_BUILDER_LIVE_PUBLISH_REMOTE` threaded from the environment (TC-046-02 regression: the remote is still threaded — the grep in TC-046-02's existing test must still pass).
  5. The actual argv for probe 034 does NOT contain `TestBranchPRPublication` as the `-run` target (that is the old fake test; the live test is the new one).
- **Edge cases:**
  - With `AGENT_BUILDER_PUBLISH_REMOTE` set, `AGENT_BUILDER_LIVE_PUBLISH_REMOTE` must also be set to the same value in the 034 env prefix (or they are the same variable threaded through).
  - The TC-046-02 existing assertion in `l6-probe-test.sh` (which greps for `AGENT_BUILDER_PUBLISH_REMOTE` in the 034 argv) must still pass — do not break that assertion.

### TC-055-03: probe 032 uses `go test ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032`

- **Requirement:** REQ-055-03
- **Mechanism:** run `scripts/l6-probe.sh --dry-run` with a faked PATH; capture stdout and the evidence file. Inspect the command string for probe 032.
- **Assertions:**
  1. The actual argv for probe 032 contains `go test` + `-run TestLivePhase0EndToEndAcceptance_TC032`.
  2. The actual argv for probe 032 contains `./tests/e2e`.
  3. The actual argv or env prefix for probe 032 contains `AGENT_BUILDER_LIVE_E2E=1`.
  4. The actual argv or env prefix for probe 032 contains `AGENT_BUILDER_PUBLISH_REMOTE` (the TC-046-02 grep invariant — this existing test must still pass).
  5. The actual argv for probe 032 does NOT contain `AGENT_BUILDER_SANDBOX_RUNTIME=srt` (TC-046-03 regression guard).
  6. The actual argv for probe 032 does NOT contain `TestPhase0EndToEndAcceptance` as the `-run` target (that is the old fake test).
- **Edge cases:**
  - `AGENT_BUILDER_LIVE_E2E_REMOTE` should be threaded alongside `AGENT_BUILDER_PUBLISH_REMOTE` in the 032 env prefix (the remote name must reach the live test).

### TC-055-04: `seed_live_fixture()` bash helper emits a valid temp task-root and worktree

- **Requirement:** REQ-055-04
- **Mechanism:** source the helper (or invoke `scripts/l6-probe.sh` in a mode that calls `seed_live_fixture`) and inspect the output:
  1. The helper writes at least two paths to stdout or sets shell variables: a task-root path and a worktree path.
  2. The task-root contains `docs/plans/roadmap.md` (the tasksource reader requires this per `internal/tasksource/source.go`).
  3. The task-root contains at least one task file at `docs/tasks/backlog/*.md` with `**Status:** ready`.
  4. The worktree is a real `git init` directory (`.git/` subdirectory present) containing a minimal Go module (`go.mod` present).
  5. Both paths are distinct temp directories (not the main repo root).
- **Mechanism for the test:** invoke `scripts/l6-probe.sh --dry-run` with a stub that intercepts `seed_live_fixture` output (or grep the dry-run status lines for probe 022/028 to confirm `AGENT_BUILDER_TASK_ROOT` and `AGENT_BUILDER_WORKTREE` point to temp dirs, not to `docs/tasks/` in the main repo).
- **Edge cases:**
  - The fixture task-root must NOT be the main repo's `docs/tasks/` directory — the 028 probe must drive a standalone minimal task, not the real backlog.

### TC-055-05: all existing TC-044 and TC-046 cases remain green (regression guard)

- **Requirement:** REQ-055-05
- **Mechanism:** after applying all changes to `scripts/l6-probe.sh` and `scripts/tests/l6-probe-test.sh`, run the full test harness:
  ```
  bash scripts/tests/l6-probe-test.sh
  ```
- **Expected output:** `=== Results: N passed, 0 failed ===` where N includes all TC-044 and TC-046 cases plus the new TC-055 cases; exit 0.
- **Specific regression checks:**
  1. **TC-044-01 (closing order):** 10 rows in exact sequence 014 → 015 → 016 → 021 → 030 → 022 → 028 → 033 → 034 → 032.
  2. **TC-044-02 (skip conditions):** 016 + 032 SKIP when `runsc` absent; 021 SKIP when `srt` absent.
  3. **TC-044-03 (evidence file shape):** 10 TASK- rows, correct format.
  4. **TC-044-04 (preflight gate):** non-dry-run with NOT READY preflight → non-zero exit.
  5. **TC-046-02 (PUBLISH_REMOTE threaded):** the probe 034 and probe 032 dry-run lines contain `AGENT_BUILDER_PUBLISH_REMOTE`; this assertion must still pass after 034/032 are updated to the new live-test commands.
  6. **TC-046-03 (no srt in any argv):** `bash scripts/l6-probe.sh --dry-run` output contains zero occurrences of `AGENT_BUILDER_SANDBOX_RUNTIME=srt`.
  7. **`bash scripts/l6-probe.sh --dry-run`:** exits 0 with exactly 10 rows in closing order.
- **Edge cases:**
  - If TC-046-02's grep literal targets the old 034 command (`TestBranchPRPublication`), update the grep target in `l6-probe-test.sh` to match the new 034 command (`TestLiveBranchPRPublication_TC034`). The **invariant** (PUBLISH_REMOTE is threaded) must still be tested — only the literal being grepped may change, and only if it genuinely moved.
  - Do not add `srt` back to any argv for any reason.

---

## Post-implementation verification

- [ ] TC-055-01: `scripts/l6-probe.sh --dry-run` shows no `--task-root` arg and no `AGENT_BUILDER_SANDBOX_RUNTIME` in 022/028 argv; ANTHROPIC_API_KEY absent → SKIP for 022/028
- [ ] TC-055-02: probe 034 argv contains `TestLiveBranchPRPublication_TC034` and `AGENT_BUILDER_LIVE_PUBLISH=1` and `AGENT_BUILDER_PUBLISH_REMOTE`
- [ ] TC-055-03: probe 032 argv contains `TestLivePhase0EndToEndAcceptance_TC032` and `AGENT_BUILDER_LIVE_E2E=1` and `AGENT_BUILDER_PUBLISH_REMOTE` and no `srt`
- [ ] TC-055-04: `seed_live_fixture()` helper emits valid temp task-root (with roadmap.md + one ready task) and a real git-init worktree with go.mod
- [ ] TC-055-05: `bash scripts/tests/l6-probe-test.sh` → `=== Results: N passed, 0 failed ===` exit 0; all TC-044 and TC-046 cases PASS
- [ ] `bash scripts/l6-probe.sh --dry-run` → 10 rows in closing order, exit 0
- [ ] `make check` passes
- [ ] L6 residual: `make l6-probe` on provisioned host (with live credentials) produces all 10 probes PASS

## Test framework notes

Framework: add new `run_tc055_NN()` functions to the existing `scripts/tests/l6-probe-test.sh` file. Call them in the main body alongside existing TC-044/TC-046 calls. Use the same `make_probe_stub_dir` stub factory — no new test infrastructure needed.

For TC-055-01, use `env -u ANTHROPIC_API_KEY bash scripts/l6-probe.sh --dry-run` in a test subshell to confirm 022/028 SKIP when the key is absent.

For TC-055-02, grep the `--dry-run` stdout for the probe 034 command line and assert presence of `TestLiveBranchPRPublication_TC034` and `AGENT_BUILDER_LIVE_PUBLISH=1`.

For TC-055-03, similarly grep for probe 032 and assert presence of `TestLivePhase0EndToEndAcceptance_TC032` and `AGENT_BUILDER_LIVE_E2E=1`; assert absence of `AGENT_BUILDER_SANDBOX_RUNTIME=srt`.

For TC-055-04, call `seed_live_fixture` (sourced or invoked) and check the resulting directories exist with the expected files; use `[ -f "$task_root/docs/plans/roadmap.md" ]` etc.

For TC-055-05 (TC-046-02 regression), the existing assertion in `l6-probe-test.sh` greps for `AGENT_BUILDER_PUBLISH_REMOTE` in the probe 034/032 lines. After the rewire, the new 034 command uses `AGENT_BUILDER_LIVE_PUBLISH_REMOTE`; the constraint says `AGENT_BUILDER_PUBLISH_REMOTE` must still appear in the env prefix. Keep or update the grep to match the actual variable that's present — the invariant (publish remote is threaded) is what matters, not the literal variable name, as long as both map to the same value.

No live Podman, srt, claude, gh, or real ANTHROPIC_API_KEY required. Highest achievable level: **L5**.
