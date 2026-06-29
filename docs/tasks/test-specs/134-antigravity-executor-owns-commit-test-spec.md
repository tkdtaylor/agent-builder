# Test spec — Task 134: Antigravity executor owns the branch + commit

**Task:** `docs/tasks/backlog/134-antigravity-executor-owns-commit.md`
**Relates to:** task 133 (the `AntigravityCLI` harness — currently trusts agy's `BRANCH:` line);
task 106 / REQ-106-04 (the OllamaNative precedent — the loop owns the commit, not the model);
ADR 057.

## Context — verified by live forensic probe (2026-06-29, `agy` v1.0.13)

Driving `agy --print "<prompt>" --model <m> --add-dir <worktree> --dangerously-skip-permissions`
against a real git worktree (the exact task-133 invocation), across two models:

- agy **reliably EDITS files in the worktree** (created `add.go`, `PROOF.txt` in-place). ✅
- agy's **git/shell operations do NOT persist to the worktree repo** — `git checkout -b` + commit
  left the repo on `main` with no new branch and no commit, **both runs**. ❌ (agy's terminal tool
  is sandboxed away from the host `.git`.)
- agy's **self-reported `BRANCH:` / "success" is unreliable** — one run hallucinated a full success
  it didn't perform; another produced empty output. ⚠️

Therefore task 133's contract ("trust agy to produce a committed branch + parse `BRANCH:`") cannot
reach L6. The fix mirrors task 106: the **executor** (trusted agent-builder code) owns the branch +
commit. agy edits; the executor commits.

## Requirements

- **REQ-134-01** — After a successful `agy` run (exit 0), `AntigravityCLI.run` commits the worktree
  edits onto a produced branch itself, via `git checkout -B <branch> && git add -A && git commit`
  (mirroring `OllamaNative.commitWorktree`), and returns `{Branch, OK:true}`. The commit must
  succeed even on a repo with no configured git user (set `-c user.email`/`-c user.name`
  fallbacks, as OllamaNative does).
- **REQ-134-02** — Branch name resolution: if agy's stdout contains a `BRANCH: <name>` line, use
  `<name>`; otherwise fall back to a deterministic name derived from the task (`task/<task.ID>`).
  The executor NO LONGER errors when agy omits `BRANCH:` (the old `ErrAntigravityMissingBranch`
  path is replaced — agy's output is advisory, not load-bearing).
- **REQ-134-03** — If the worktree has **no changes** after the agy run (agy produced nothing), the
  executor returns `{OK:false}` and a descriptive error (a failed attempt — the gate/retry loop
  handles it); it does NOT create an empty branch.
- **REQ-134-04** — The committed branch deterministically contains agy's edits: the produced branch
  HEAD includes the files agy wrote.

## Test cases

(Use the executor stub-subprocess infra; the stub "agy" helper WRITES a file into the worktree to
simulate agy's in-place edits, then the executor commits — mirror task 106's TC-106-04.)

- **TC-134-01** (REQ-134-01, -04) `TestAntigravityCommitsWorktreeOntoBranch`: temp git worktree
  (seed commit on `main`); stub agy writes `add.go` + prints `BRANCH: task/x`. After `run`: assert
  result `{Branch:"task/x", OK:true}`; the worktree is on `task/x`; `git log task/x` HEAD contains
  `add.go` (`git show task/x:add.go` succeeds with the written content). Hard-assert the file is
  committed, not just present in the working tree.
- **TC-134-02** (REQ-134-02) `TestAntigravityBranchNameFallback`: (a) stub prints `BRANCH:
  feature/y` → branch `feature/y`. (b) stub prints **no** BRANCH line (empty/unrelated output) but
  writes a file → branch falls back to `task/<task.ID>` and the run still succeeds (NO
  `ErrAntigravityMissingBranch`). Assert both branch names exactly.
- **TC-134-03** (REQ-134-03) `TestAntigravityNoChangesIsError`: stub agy writes nothing (worktree
  unchanged) and exits 0 → `run` returns `OK:false` + a non-nil error mentioning "no changes" (or
  similar); assert no new branch was created (still on the seed branch).
- **TC-134-04** (REQ-134-01) `TestAntigravityCommitWorksWithoutGitUser`: worktree with NO
  `user.email`/`user.name` configured; stub writes a file → commit still succeeds (fallback config),
  branch contains the file.

(Existing task-133 TCs that asserted `ErrAntigravityMissingBranch` as the missing-branch path are
updated here to the new fallback contract; the argv/subscription/PriorFailure/config TCs from 133
remain unchanged and must still pass.)

## Verification levels

- **L2/L3:** `go test -race -count=1 ./internal/executor/... ./internal/runtime/...` + `make check`
  + `make fitness-supervisor-isolation` green. Hard assertions (commit verified via `git show
  <branch>:<file>`), no smoke tests.
- **L6 (the payoff — `agy` runs headless on this host):** route a scoped goal to an antigravity
  subscription entry against a real target worktree; agy edits the worktree; the executor commits
  the edits onto the produced branch; the verify gate runs on that committed branch. Observe a
  **gate-passing committed branch produced via `agy`** — the end-to-end the task-133 contract could
  not reach. (Main session drives this.)
