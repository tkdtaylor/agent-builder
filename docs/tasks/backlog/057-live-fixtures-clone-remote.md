# Task 057: live-fixtures-clone-remote

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix both live-run fixtures so they base their WORKTREE on a clone of the configured publish remote (`l6`) instead of a bare `git init`. This allows the orchestrator's publisher to run `gh pr create --fill`, which requires the branch to descend from the remote's default branch `l6/main` (shared history); without it, the command fails with `ambiguous argument 'l6/main...<branch>'`.

Affected fixtures:
1. `seed_live_fixture()` in `scripts/l6-probe.sh` — used by probes 022/028
2. `newLiveCapstoneFixture(t)` in `tests/e2e/live_phase0_e2e_test.go` — used by probe 032 capstone test

## Context

- **Root cause:** Both fixtures currently create a bare `git init` with no shared history with the l6 remote. When the executor creates a task branch and the publisher runs `gh pr create --fill`, it tries to compute the PR description from `l6/main...branch` and fails because there is no common ancestor.
- **Validation:** Task 056 fixed the same issue for the live publisher test (`TestLiveBranchPRPublication_TC034`); the fix was validated live against `l6` (PR #1 opened then closed/deleted).
- **Architecture fact:** Claude, the gate, and the publisher run HOST-SIDE; the Podman box runs only `/bin/true` as a liveness probe. The gate (`make check` / `go test ./...`) runs host-side against the worktree (the l6 clone = the agent-builder mirror). This is intentional — a realistic capstone.
- **Recursion hazard:** The binary's gate runs `go test ./...` on the l6 clone, which includes the live tests themselves. They SKIP only if `AGENT_BUILDER_LIVE_E2E` / `AGENT_BUILDER_LIVE_PUBLISH` are NOT set in the gate's environment. The capstone test drives the binary via `runAgentBuilder()` with an explicit env map — it MUST NOT include `AGENT_BUILDER_LIVE_E2E` (that flag gates the OUTER go-test only; the binary never needs it). Verify `runAgentBuilder()`/`filteredEnv()` does not propagate the ambient `AGENT_BUILDER_LIVE_E2E` into the binary.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-057-01 | `seed_live_fixture()` creates a WORKTREE by cloning the configured l6 remote (default: resolve via `git remote get-url l6` from the repo root), not via bare `git init`. The clone is full (no `--depth`). The TASK-ROOT (roadmap.md + one ready task) stays a separate temp dir, unchanged. | must have |
| REQ-057-02 | `newLiveCapstoneFixture(t)` creates a WORKTREE by cloning the configured `AGENT_BUILDER_LIVE_E2E_REMOTE` (default: `l6`), not via bare `git init`. The clone is full. The TASK-ROOT stays separate. | must have |
| REQ-057-03 | Both fixtures resolve the remote URL via `git -C <repo> remote get-url <remote>` (remote defaults to `l6` / `AGENT_BUILDER_LIVE_E2E_REMOTE`). Do NOT hardcode a URL; do NOT override git `user.email` in either fixture. The ambient identity (`...@users.noreply.github.com`) pushes cleanly. | must have |
| REQ-057-04 | `runAgentBuilder(t)` or the fixture env map does not carry ambient `AGENT_BUILDER_LIVE_E2E` into the binary's environment. Verify the code path and document in the spec. | must have |
| REQ-057-05 | `bash scripts/tests/l6-probe-test.sh` → all prior TC-044/TC-046 cases still PASS, plus new TC-057 assertions that the fixtures produce l6 clones (`.git/`, `main` branch, `l6` remote, descends from `l6/main`). Update TC-055-04 assertion if needed (e.g., assert worktree has `.git/` and `l6` remote, instead of just `go.mod`). | must have |
| REQ-057-06 | `go test ./tests/e2e -count=1 -v` → capstone `TestLivePhase0EndToEndAcceptance_TC032` SKIPs cleanly (flag unset), all other e2e tests PASS, exit 0. | must have |
| REQ-057-07 | `make check` → all checks pass. `bash scripts/l6-probe.sh --dry-run` → 10 rows in closing order, exit 0. | must have |
| REQ-057-08 | Add a real (non-Claude) clone validation test that invokes the fixture's clone logic against the l6 remote when available and asserts the worktree is a git repo with `.git/`, on `main`, with `<remote>/main` ancestor relationship. GATE this so it SKIPs when l6/gh is not configured (do not fail CI). This proves the clone mechanics without spending Claude. | must have |

## Readiness gate

- [x] Test spec `057-live-fixtures-clone-remote-test-spec.md` exists
- [x] All acceptance criteria below have linked REQ IDs
- [x] Tasks 053/054/055/056 merged

## Acceptance criteria

- [ ] [REQ-057-01] TC-057-01: `seed_live_fixture()` clones l6 remote into WORKTREE; full clone; TASK-ROOT separate
- [ ] [REQ-057-02] TC-057-02: `newLiveCapstoneFixture(t)` clones `AGENT_BUILDER_LIVE_E2E_REMOTE` (default `l6`) into WORKTREE; full clone; TASK-ROOT separate
- [ ] [REQ-057-03] TC-057-03, TC-057-04: both fixtures resolve remote via `git remote get-url`; no hardcoded URL; no `user.email` override
- [ ] [REQ-057-04] TC-057-03: `runAgentBuilder()` env map does not carry `AGENT_BUILDER_LIVE_E2E` into the binary; documented in spec
- [ ] [REQ-057-05] TC-057-05: `bash scripts/tests/l6-probe-test.sh` → all TC-044/TC-046 cases PASS; TC-055-04 updated to assert l6 clone properties; new TC-057-01/TC-057-02 assertions green
- [ ] [REQ-057-06] TC-057-06: capstone test SKIPs cleanly when flag unset; all other e2e tests PASS; `go test ./tests/e2e -count=1 -v` exit 0
- [ ] [REQ-057-07] TC-057-07: `make check` green; `bash scripts/l6-probe.sh --dry-run` → 10 rows in order, exit 0
- [ ] [REQ-057-08] TC-057-08: non-Claude clone validation test SKIPs cleanly when l6/gh not configured; PASS with real assertions when configured; `go test ./...` exits 0

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./tests/e2e -count=1 -v` (capstone SKIP, others PASS), `bash scripts/tests/l6-probe-test.sh` (all TCs green including TC-055-04 regression), `bash scripts/l6-probe.sh --dry-run`, `make check` green.
- **L5 harness commands:**
  ```
  go test ./tests/e2e -count=1 -v
  bash scripts/tests/l6-probe-test.sh
  bash scripts/l6-probe.sh --dry-run
  make check
  ```
- **L6 residual (operator-only, provisioned host):**
  ```
  env AGENT_BUILDER_LIVE_E2E=1 AGENT_BUILDER_LIVE_E2E_REMOTE=l6 ANTHROPIC_API_KEY=<real> go test -count=1 -v ./tests/e2e -run TestLivePhase0EndToEndAcceptance_TC032
  bash scripts/l6-probe.sh
  ```
  Expected: capstone test PASS (real PR opened, cleaned up); all 10 probes PASS or SKIP for prerequisite-absent reasons (not config errors).

## Out of scope

- Changes to the closing order (014 → 015 → 016 → 021 → 030 → 022 → 028 → 033 → 034 → 032)
- Changes to the env contract or argv format for other probes (tasks 053/054 set the contract)
- Adding new probes

## Notes

### Implementation strategy

1. **In `scripts/l6-probe.sh` (seed_live_fixture):**
   - Keep the TASK-ROOT creation unchanged (roadmap.md + one ready task in temp dir)
   - Change the WORKTREE from bare `git init` to a full clone of the configured l6 remote
   - Resolve the remote URL: `git -C <repo-root> remote get-url l6`
   - Create the worktree directory with `mktemp -d`
   - Clone via `git clone <url> <worktree-dir>` (full clone, no `--depth`)
   - Verify the clone succeeded and the worktree is on the default branch
   - Do NOT set `user.email` or `user.name` in the fixture

2. **In `tests/e2e/live_phase0_e2e_test.go` (newLiveCapstoneFixture):**
   - Keep the TASK-ROOT creation unchanged
   - Change the WORKTREE from bare `git init` to a full clone of `AGENT_BUILDER_LIVE_E2E_REMOTE` (default: `l6`)
   - Resolve the remote URL: `git -C <repoRoot> remote get-url <remote>` or inline
   - Clone via `exec.Command("git", "clone", <url>, <worktree-dir>)` (no shallow flag)
   - Do NOT override `user.email` in any git config command
   - The fixture's `env(t)` method returns the env map passed to `runAgentBuilder()` — ensure `AGENT_BUILDER_LIVE_E2E` is NOT in this map (it's only for gating the outer test)

3. **Verify recursion hazard (TC-057-03):**
   - Inspect `runAgentBuilder(t, binary, fixture.env(t), "run")` in the capstone test
   - Confirm the env map passed to `cmd.Env = <map>` does NOT include `AGENT_BUILDER_LIVE_E2E`
   - Add a comment documenting this constraint

4. **Update test harness (TC-055-04 regression):**
   - Modify `scripts/tests/l6-probe-test.sh` TC-055-04 assertion to check that the fixture produces a real git clone (assert `.git/` exists, `git -C <worktree> remote` returns `l6`, `git merge-base --is-ancestor` succeeds)
   - Add TC-057-01 and TC-057-02 assertions in the same harness
   - Keep all prior TC-044 and TC-046 checks green

5. **Non-Claude clone validation (TC-057-08):**
   - Add a real test (or extend an existing one) that calls the fixture's clone logic
   - Gate it on `which gh && git -C <repo> remote get-url l6` (SKIP if l6 not configured)
   - Assert the resulting worktree has `.git/`, is on `main`, has the `l6` remote, and `git merge-base --is-ancestor l6/main main` succeeds
   - Do not spend Claude; this is a real assertion test

### Architecture constraints (do NOT violate)

- **Recursion guard:** The binary's gate runs `go test ./...` on the l6 clone. If the gate environment has `AGENT_BUILDER_LIVE_E2E=1`, the live tests run inside the gate and may themselves run the binary, causing infinite recursion. The outer test (TC-032) must ensure its env map does NOT carry this flag. Verify and document in code + spec.
- **No stale srt/SANDBOX_RUNTIME:** Tasks 051/052 removed the srt/sandbox-runtime backend. Ensure no `AGENT_BUILDER_SANDBOX_RUNTIME` is added to the fixture env; TC-046-03 regression guard will catch it.
- **No hardcoded URLs:** Resolve remotes dynamically via `git remote get-url` to stay resilient to rebranding or alternate mirror URLs.
- **Full clones only:** Shallow clones break the later `git push` (rejected across the shallow boundary). Task 056 proved this; use full clones here too.

