# Task 134 — Antigravity executor owns the branch + commit

**Status:** backlog
**Spec:** `docs/tasks/test-specs/134-antigravity-executor-owns-commit-test-spec.md`
**Relates to:** task 133 (`AntigravityCLI`); task 106 / REQ-106-04 (OllamaNative commit precedent);
ADR 057.

## Goal

Make the Antigravity executor produce a real, committed branch so it can reach L6. A live forensic
probe showed `agy` reliably **edits** the worktree but its **git/commit operations do not persist**
to the worktree repo, and its self-reported `BRANCH:` line is unreliable. So — exactly as task 106
did for the ollama-native harness — the **executor** must own the branch + commit, not the agent.

## Context (verified live, 2026-06-29)

Two probes driving the task-133 invocation against a real git worktree: agy created files in-place
(✅) but left the repo on `main` with no branch and no commit (❌, both runs); one run hallucinated
success, one produced empty output. agy's terminal/shell tool is sandboxed away from the host
`.git`. Conclusion: trust agy for edits, not for git.

## Scope

- **`internal/executor/antigravity_cli.go`:**
  - After `agy` exits 0, resolve the branch name: prefer a `BRANCH: <name>` line in stdout, else
    fall back to `task/<task.ID>` (REQ-134-02). Remove the hard `ErrAntigravityMissingBranch`
    failure on missing-branch (agy output is now advisory).
  - If the worktree has no changes (nothing for agy produced), return `{OK:false}` + descriptive
    error; do not create an empty branch (REQ-134-03).
  - Otherwise commit the worktree onto the branch: `git checkout -B <branch> && git add -A && git
    commit -m "agent-builder: complete task <id>"`, with `-c user.email=…`/`-c user.name=…`
    fallbacks so it works on a repo without a configured user (REQ-134-01). **Reuse the
    `OllamaNative.commitWorktree` logic** — strongly prefer extracting a shared helper (e.g.
    `executor.commitWorktree(worktree, branch, taskID)` or a small internal func) used by BOTH
    OllamaNative and AntigravityCLI rather than duplicating; if extraction risks regressing
    OllamaNative, mirror the logic and note the dup for a later cleanup. A "no changes to commit"
    from git is the REQ-134-03 error path.
  - The subprocess `cmd.Dir` is already the worktree; run the git commands in the worktree too.
- **Investigation first (brief):** confirm there is no `agy` flag/config that makes its shell/git
  persist to the worktree (the probe strongly indicates not — its shell is sandboxed). Default to
  executor-side commit regardless; record the finding in the ADR/spec.
- **Spec (same commit):** update `docs/spec/interfaces.md` (the AntigravityCLI contract: executor
  owns the commit; `BRANCH:` advisory + `task/<id>` fallback) and ADR 057 (amend the
  branch/commit-ownership decision with the forensic finding).

## Out of scope

- Changing OllamaNative's behavior beyond a safe shared-helper extraction (if done).
- The dead `GeminiCLI` executor.
- Router/model tuning.

## Verification plan

- **Highest level achievable now: L6** (`agy` works headless on this host).
- **L2/L3:** unit tests assert the commit lands (verified via `git show <branch>:<file>`), the
  fallback branch name, the no-changes error, and the no-git-user commit. `make check` +
  `make fitness-supervisor-isolation` green.
- **Producer→consumer trace:** agy edits worktree files → `run` resolves branch (BRANCH: or
  `task/<id>`) → `commitWorktree` (`checkout -B`/`add -A`/`commit`) → `{Branch, OK:true}` → gate
  verifies the committed branch.
- **L6 (in-session):** live agy run against a target worktree → executor-committed branch → gate
  passes. This is the L6 that closes out task 133 (and 134) — promote both rows on this evidence.

## Boundaries

- Do not regress the task-133 argv / subscription-mode / PriorFailure / config tests.
- Do not trust agy's self-reported success — the executor must verify there are real changes and own
  the commit.
- Test-spec-first; spec/ADR updated in the same commit; commit at 🟡 on the task branch.
