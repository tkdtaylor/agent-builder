# Task 056: live publisher test bases its branch on the remote default branch

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix `TestLiveBranchPRPublication_TC034` (task 053) so it can actually open a PR against the
`l6` sandbox. The test currently builds its branch off a bare `git init` root with no shared
history with `l6/main`, so `gh pr create --fill` fails (`ambiguous argument 'l6/main...branch'`).
Base the working branch on the remote default branch via a full fetch instead.

## Context

- Discovered while running the live 034 probe: push succeeded (stray branch left on `l6`) but
  `gh pr create --fill` failed because `--fill` computes the PR body from `<remote>/main...branch`
  and the branch shared no ancestor with `l6/main`.
- Fix validated live against `l6` (PR #1 opened then closed/deleted): `git fetch <remote> main`
  (full, not shallow) → `git checkout -b <branch> FETCH_HEAD` → commit → push → `gh pr create
  --fill` succeeds.
- A shallow `--depth=1` fetch is NOT usable: the pushed branch is rejected ("failed to push some
  refs") across the shallow boundary. Use a full fetch.
- The ambient git identity (`…@users.noreply.github.com`) pushes cleanly; do not override
  `user.email` (forcing a personal address trips GitHub email-privacy push rejection).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-056-01 | The live test fetches `<remote> main` (full fetch) and creates the unique branch off `FETCH_HEAD` so it descends from `<remote>/main`; `publisher.Publish` then yields a real PR URL + non-empty PRID. | must |
| REQ-056-02 | Skip discipline unchanged: live test SKIPs when `AGENT_BUILDER_LIVE_PUBLISH` unset; fake `TestBranchPRPublication`/`TestPublisherFailureRedactsSecrets` still PASS; `go test ./tests/publisher` exit 0. | must |
| REQ-056-03 | The fetch is a full fetch (no `--depth`). | must |

## Readiness gate

- [x] Test spec `056-live-test-base-on-remote-main-test-spec.md` exists
- [x] Fix validated live against `l6` before coding
- [x] No blocking dependencies (053 merged)

## Acceptance criteria

- [ ] [REQ-056-01] TC-056-01: live run opens a real PR (operator L6)
- [ ] [REQ-056-02] TC-056-02: `go test ./tests/publisher -count=1 -v` → live SKIP, fakes PASS, exit 0
- [ ] [REQ-056-03] TC-056-03: no `--depth` in the live test's fetch

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./tests/publisher -count=1 -v` (live SKIP, fakes PASS), `make check` green.
- **L6 residual (operator):** `env AGENT_BUILDER_LIVE_PUBLISH=1 AGENT_BUILDER_LIVE_PUBLISH_REMOTE=l6 go test -count=1 -v ./tests/publisher -run TestLiveBranchPRPublication_TC034` → PASS, real PR URL, cleaned up.

## Out of scope

- The same base-on-remote fix for `seed_live_fixture` (022/028) and `newLiveCapstoneFixture`
  (032) — same root cause, separate (larger) change since those worktrees must be `l6` clones
  so the in-box gate and Claude run against real content.
