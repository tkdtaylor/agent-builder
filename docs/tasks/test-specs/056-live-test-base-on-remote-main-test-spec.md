# Test spec — Task 056: live publisher test bases its branch on the remote default branch

## Context

The live publisher probe `TestLiveBranchPRPublication_TC034` (task 053) builds its temp
repo with a bare `git init`, then `git checkout -b <branch>` off that empty root, and calls
`publisher.Publish` which runs `gh pr create --head <branch> --fill`. `--fill` computes the
PR title/body from the commit range `<remote>/main...<branch>`. Because the branch shares no
history with `l6/main` (and `l6/main` was never fetched), the live run fails with:
`fatal: ambiguous argument 'l6/main...task/034-live-...': unknown revision`. The push itself
succeeds, leaving a stray branch on the remote.

Validated fix (confirmed live against `l6`, PR #1 opened then cleaned): fetch the remote
default branch and create the working branch off it, so the branch descends from `<remote>/main`
with shared history and `gh pr create --fill` resolves. A **full** fetch is required — a
shallow (`--depth=1`) fetch makes the branch unpushable across the shallow boundary.

## Test cases

### TC-056-01 — branch descends from the remote default branch (live, L6 residual)
- **Setup:** `AGENT_BUILDER_LIVE_PUBLISH=1`, real `git`/`gh`, `l6` remote configured + authed.
- **Assertion:** the temp repo fetches `<remote> main` (full, not shallow) and creates the
  unique branch off `FETCH_HEAD`; after `publisher.Publish`, `result.PRURL` matches
  `github\.com/.+/pull/\d+` and `result.PRID` is non-empty (i.e. `gh pr create --fill`
  succeeded — proving shared history with `<remote>/main`). The PR is closed and the remote
  branch deleted in `t.Cleanup`.
- **Highest in-repo level:** L5 (the test still SKIPs cleanly when the flag is unset). The PASS
  is L6, operator-run.

### TC-056-02 — skip discipline unchanged (L5)
- **Assertion:** with `AGENT_BUILDER_LIVE_PUBLISH` unset, `go test ./tests/publisher` shows
  `TestLiveBranchPRPublication_TC034 --- SKIP`, exit 0; the fake `TestBranchPRPublication` and
  `TestPublisherFailureRedactsSecrets` still PASS. The new fetch/branch logic lives entirely
  inside the live (flag-gated) path and does not run in CI.

### TC-056-03 — no shallow fetch (regression guard)
- **Assertion:** the fetch in the live test is a full fetch (no `--depth`), because a shallow
  fetch makes the pushed branch fail with "failed to push some refs". (Source-level check.)

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./tests/publisher -count=1 -v` →
  live test SKIP, fakes PASS, exit 0; `make check` → All checks passed.
- **L6 residual (operator):** `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6
  go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034` → PASS with a
  real `…/pull/N` URL logged; `gh pr list` shows the branch cleaned up.

## Out of scope

- The analogous repo-base fix for the `go run` probes (022/028 `seed_live_fixture` in
  `scripts/l6-probe.sh`) and the capstone (032 `newLiveCapstoneFixture` in
  `tests/e2e/live_phase0_e2e_test.go`) — tracked separately; same root cause, larger change
  (the worktree must be an `l6` clone so the gate and Claude run against real content).
