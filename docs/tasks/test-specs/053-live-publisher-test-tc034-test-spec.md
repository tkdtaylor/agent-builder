# Test Spec 053: live publisher test (TC-034)

**Linked task:** [`docs/tasks/backlog/053-live-publisher-test-tc034.md`](../backlog/053-live-publisher-test-tc034.md)
**Written:** 2026-06-17
**Status:** ready

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-053-01 | TC-053-01 | ⏳ |
| REQ-053-02 | TC-053-02 | ⏳ |
| REQ-053-03 | TC-053-03 | ⏳ |
| REQ-053-04 | TC-053-04 (regression) | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Context: live-test idiom

The codebase's established live-test idiom is in `tests/sandbox/sandbox_runtime_adapter_test.go:210-218` and `tests/e2e/phase1_end_to_end_acceptance_test.go:109-172`: skip when the env flag is unset, `t.Skipf` when a tool prerequisite is absent, `t.Fatalf` only on a genuine configuration error (e.g. the remote isn't configured). The new test `TestLiveBranchPRPublication_TC034` in `tests/publisher/live_publish_test.go` follows this exact pattern. The EXISTING `TestBranchPRPublication` in `tests/publisher/publisher_test.go` is untouched.

The skip-shape assertions (TC-053-02 and TC-053-03) are the in-repo L5 evidence: running `go test ./tests/publisher` with the flag unset must stay green in CI.

---

## Test cases

### TC-053-01: live test body — real git push and PR creation when flag set

- **Requirement:** REQ-053-01
- **Mechanism:** when `AGENT_BUILDER_LIVE_PUBLISH=1` is set AND `git`, `gh` are on PATH AND the configured remote (`AGENT_BUILDER_LIVE_PUBLISH_REMOTE`, default `l6`) exists AND `gh auth status` exits 0:
  1. The test creates a `t.TempDir()` git repo with `git init`.
  2. Adds the same remote URL (obtained from `git remote get-url <remote>` in the main repo).
  3. Commits one file on a unique branch `task/034-live-<unix-timestamp>-<pid>`.
  4. Calls `publisher.NewGitHubCLI(publisher.GitHubCLIConfig{...}).Publish(ctx, publisher.Request{...})`.
  5. Asserts `result.PRURL` matches the regexp `github\.com/.+/pull/\d+`.
  6. Asserts `result.PRID` is non-empty.
  7. Logs the real PR URL with `t.Log`.
  8. `t.Cleanup` runs: `gh pr close <branch> --delete-branch` (in the LIVE_PUBLISH_REMOTE repo) and `git push <remote> --delete <branch>` as a belt-and-suspenders cleanup.
- **Expected output:** test PASS; `t.Log` line shows a real `github.com/.../pull/N` URL; the remote branch and PR are cleaned up by `t.Cleanup`.
- **NOTE:** This TC is reached only when `AGENT_BUILDER_LIVE_PUBLISH=1` is set (operator-only, L6 residual). In-repo CI always sees this test skip.

### TC-053-02: skip cleanly when `AGENT_BUILDER_LIVE_PUBLISH` is unset

- **Requirement:** REQ-053-02
- **Mechanism:** run `go test ./tests/publisher` WITHOUT setting `AGENT_BUILDER_LIVE_PUBLISH`. Inspect the test output.
- **Expected output:**
  1. `TestLiveBranchPRPublication_TC034` emits `--- SKIP` in the verbose output.
  2. Exit code is 0.
  3. `TestBranchPRPublication` (the existing fake test) still passes — the live test file must not interfere with or shadow the existing test.
- **Edge cases:**
  - The test must call `t.Skip(...)` (not `t.Fatal`) when the flag is absent — SKIP and PASS are both acceptable outcomes from CI's perspective; FAIL is not.
  - Running with `AGENT_BUILDER_LIVE_PUBLISH=` (empty string) must also produce a skip, not a pass.

### TC-053-03: skip when a tool prerequisite is absent

- **Requirement:** REQ-053-02
- **Mechanism:** set `AGENT_BUILDER_LIVE_PUBLISH=1` but simulate missing prerequisites:
  - Subcase A: `git` not on PATH (or `git` set to a path that doesn't exist).
  - Subcase B: `gh` not on PATH.
  - Subcase C: `AGENT_BUILDER_LIVE_PUBLISH_REMOTE` names a remote that is not configured in the repo (`git remote get-url <name>` fails).
  - Subcase D: `gh auth status` returns non-zero.
- **Expected output for all subcases:** `TestLiveBranchPRPublication_TC034` emits `--- SKIP` with a reason naming the missing prerequisite; exit code is 0 (not 1). No PR is created; no cleanup is attempted.
- **Edge cases:**
  - A missing remote must produce a `t.Skipf`, not a `t.Fatalf` (it is a missing-prereq SKIP, not a config error).
  - A failed `gh auth status` must produce a `t.Skipf` (unauthenticated is a prereq SKIP, not a test failure).

### TC-053-04: existing `tests/publisher` fake tests still pass (regression guard)

- **Requirement:** REQ-053-04
- **Mechanism:** after adding `tests/publisher/live_publish_test.go`, run `go test ./tests/publisher`. The existing fake tests must still pass unchanged.
- **Expected output:** `TestBranchPRPublication` PASS; `TestPublisherFailureRedactsSecrets` PASS; exit 0. The new live test SKIP (since `AGENT_BUILDER_LIVE_PUBLISH` is unset in CI). (Note: the e2e publisher regression `TestPublisherFailureDoesNotMarkDone` lives in `tests/e2e/`, not `tests/publisher/`; it is covered by `make check`, which runs both packages.)
- **Edge cases:**
  - Confirm the new file is in the same package `publisher_test` and does not introduce any compilation error, name collision, or unused import.

---

## Post-implementation verification

- [ ] TC-053-02: `go test ./tests/publisher` exits 0 with `TestLiveBranchPRPublication_TC034 --- SKIP` when flag unset (L5, in-repo)
- [ ] TC-053-03: skip-shape for all four missing-prereq subcases verified (L5, in-repo, using `t.Skipf` path)
- [ ] TC-053-04: existing fake tests still PASS (regression guard)
- [ ] `make check` passes after the new file is added
- [ ] L6 residual: operator runs `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6 go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034` on a provisioned host with `gh` authenticated and the `l6` remote configured; TC-053-01 PASS; real PR URL logged; cleanup confirmed

## Test framework notes

Framework: new file `tests/publisher/live_publish_test.go`, package `publisher_test`. The file imports:
- `github.com/tkdtaylor/agent-builder/internal/publisher`
- standard library (`context`, `fmt`, `os`, `os/exec`, `regexp`, `strconv`, `testing`, `time`)

No new build tags. The skip gate is `os.Getenv("AGENT_BUILDER_LIVE_PUBLISH") != "1"`.

For TC-053-03 subcases, use `exec.LookPath("git")` / `exec.LookPath("gh")` and call `t.Skipf` when the binary is absent. For the remote check, run `exec.Command("git", "remote", "get-url", remote).Run()` in the main repo working directory; if it returns non-zero, `t.Skipf`. For `gh auth status`, run `exec.Command("gh", "auth", "status").Run()` and `t.Skipf` on non-zero.

For the branch name uniqueness, use `fmt.Sprintf("task/034-live-%d-%d", time.Now().Unix(), os.Getpid())`.

For `t.Cleanup`, use `exec.Command("gh", "pr", "close", branch, "--delete-branch")` run in a repo directory that has the remote; also `exec.Command("git", "push", remote, "--delete", branch)` as belt-and-suspenders. These cleanup commands may fail if the PR was already closed or the branch was never pushed — log the error but do not fail the test on cleanup failure.

Highest achievable level in-repo: **L5** (skip-shape green in `go test ./tests/publisher` without live flag). TC-053-01 is L6 residual (operator-only).
