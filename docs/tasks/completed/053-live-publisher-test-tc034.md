# Task 053: live publisher test (TC-034)

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Add a real, env-gated live publisher test that actually invokes `git push` and `gh pr create` against the private `l6` sandbox remote, closing the L6 gap for task 034 (branch and PR publication). The test skips cleanly in CI when the env flag is unset, so `go test ./tests/publisher` stays green without live credentials.

## Context

- The existing `TestBranchPRPublication` in `tests/publisher/publisher_test.go` uses git/gh shims and asserts a faked URL (`acme/repo/pull/34`). It is the deterministic L5 acceptance gate and **must not be modified**.
- This task adds a NEW file `tests/publisher/live_publish_test.go` with `TestLiveBranchPRPublication_TC034`, gated on `AGENT_BUILDER_LIVE_PUBLISH=1`.
- The live-test idiom is established by `tests/sandbox/sandbox_runtime_adapter_test.go:210-218` and `tests/e2e/phase1_end_to_end_acceptance_test.go:109-172`: skip when flag unset, `t.Skipf` when a prereq binary is absent, `t.Fatalf` only on a genuine config error.
- Live PRs target the private `l6` remote (default) or the remote named by `AGENT_BUILDER_LIVE_PUBLISH_REMOTE`. The test self-cleans: `t.Cleanup` closes the PR and deletes the remote branch.
- **Model tier: balanced (sonnet)** — wiring a live test with correct skip/cleanup discipline.
- **Dependency:** task 052 (ADR 031 written; doc convention established). Independent of task 054.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-053-01 | `tests/publisher/live_publish_test.go` contains `TestLiveBranchPRPublication_TC034`; when `AGENT_BUILDER_LIVE_PUBLISH=1` is set AND all prereqs present: creates a temp git repo, commits on a unique branch `task/034-live-<ts>-<pid>`, calls `publisher.NewGitHubCLI(...).Publish(...)`, asserts `result.PRURL` matches `github\.com/.+/pull/\d+` and `result.PRID` is non-empty; `t.Log` the real URL; `t.Cleanup` closes PR + deletes remote branch | must have |
| REQ-053-02 | When `AGENT_BUILDER_LIVE_PUBLISH` is unset (or empty): `TestLiveBranchPRPublication_TC034` calls `t.Skip` and emits `--- SKIP`; when any prereq is absent (`git`, `gh`, remote not configured, `gh auth status` fails): `t.Skipf` with a reason naming the missing prereq; in both cases exit 0 and no PR is created | must have |
| REQ-053-03 | `go test ./tests/publisher` (without live flag) stays green: existing `TestBranchPRPublication` and `TestPublisherFailureDoesNotMarkDone` still PASS; new live test SKIP; no compilation error | must have |
| REQ-053-04 | `make check` passes after the new file is added | must have |

## Readiness gate

- [x] Test spec `053-live-publisher-test-tc034-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Task 052 merged (ADR 031 written, doc convention established)

## Acceptance criteria

- [ ] [REQ-053-01] TC-053-01: test body per spec — temp git repo, unique branch `task/034-live-<ts>-<pid>`, `publisher.NewGitHubCLI(...).Publish(...)` called, `result.PRURL` matches `github\.com/.+/pull/\d+`, `result.PRID` non-empty, `t.Log` real URL, `t.Cleanup` runs `gh pr close <branch> --delete-branch` + `git push <remote> --delete <branch>`
- [ ] [REQ-053-02] TC-053-02: `go test ./tests/publisher` without flag → `TestLiveBranchPRPublication_TC034 --- SKIP` + exit 0; TC-053-03: flag set but prereq absent → `t.Skipf` with named reason + exit 0
- [ ] [REQ-053-03] TC-053-04: existing `TestBranchPRPublication` + `TestPublisherFailureDoesNotMarkDone` PASS; `go test ./tests/publisher` exit 0
- [ ] [REQ-053-04] `make check` exits 0

## Verification plan

- **Highest level achievable in-repo:** L5 — the test skips cleanly when `AGENT_BUILDER_LIVE_PUBLISH` is unset; `go test ./tests/publisher` stays green (all tests pass or skip, none fail).
- **L5 harness command:**
  ```
  go test -count=1 -v ./tests/publisher
  ```
  Expected: `TestBranchPRPublication --- PASS`, `TestPublisherFailureDoesNotMarkDone --- PASS`, `TestLiveBranchPRPublication_TC034 --- SKIP`; exit 0.
- **L6 residual (operator-only, provisioned host):**
  ```
  env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6 \
    go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034
  ```
  Expected: `--- PASS TestLiveBranchPRPublication_TC034`; `t.Log` line contains a real `github.com/tkdtaylor/agent-builder-l6-sandbox/pull/N` URL; cleanup confirmed (`gh pr list` shows the branch gone).
- **Cross-module state risk:** none — new test file only; no Go source or spec files changed.
- **Runtime-visible surface:** `result.PRURL` and `result.PRID` (live PR artifact); cleanup commands.

## Out of scope

- Modifying `tests/publisher/publisher_test.go` (the existing fake L5 test).
- Changes to `internal/publisher/publisher.go` — it already works correctly.
- The end-to-end live capstone (that is task 054).
- Any `scripts/l6-probe.sh` changes (that is task 055).
- `docs/spec/` changes — no externally-visible behavior changes.

## Notes

- Unique branch name: `fmt.Sprintf("task/034-live-%d-%d", time.Now().Unix(), os.Getpid())`.
- Remote URL for the temp git repo: obtain via `exec.Command("git", "remote", "get-url", remote)` run from the main repo root (`os.Getenv("REPO_ROOT")` or by resolving relative to `runtime.Caller`'s file path).
- `t.Cleanup` cleanup commands may fail if the PR was already closed — log but do not `t.Fatal` on cleanup failure, so a cleanup glitch does not obscure a passing test.
- `publisher.NewGitHubCLI` takes `publisher.GitHubCLIConfig{GitPath: "git", GHPath: "gh", Worktree: tmpRepo, Remote: remote}`. Pass the real `git`/`gh` binaries (resolved via `exec.LookPath`).
