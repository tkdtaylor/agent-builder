# Task 022: Claude Code CLI executor adapter

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
A concrete `Executor` that drives the Claude Code CLI as a subprocess: hand it the task + worktree, let its own tool-loop run, and return the produced branch as a `Result`.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§5 executor model — cloud CLI bundles harness+model; single executor at bootstrap; ToS check on unattended subscription auth; §3 credential handling)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: <none yet> — record one if the subprocess-driving / auth approach is non-obvious
- Dependencies: 001 (Executor seam)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Implement `Executor.Run(Task)(Result,error)` by invoking the Claude Code CLI subprocess against the worktree. | must have |
| REQ-002 | Capture the branch produced by the CLI into `Result.Branch`. | must have |
| REQ-003 | Auth token mounted per the accepted token-in-box risk, independently revocable. Token handling documented in `docs/spec/configuration.md` in the same commit. | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 001

## Acceptance criteria
- [ ] [REQ-001] `Executor.Run(Task)` invokes the Claude Code CLI subprocess against the task's worktree and lets its tool-loop run to completion.
- [ ] [REQ-002] The branch the CLI produced is captured into `Result.Branch`; `Result.OK` reflects subprocess success.
- [ ] [REQ-003] The auth token is supplied to the subprocess as an independently-revocable credential; token handling is recorded in `docs/spec/configuration.md`.

## Verification plan
- **Highest level achievable:** L6 — run the executor against a trivial fixture task; observe a branch produced by the CLI subprocess. Quote the produced branch name.
- **Precondition:** this may be gated on interactive auth availability (unattended subscription auth subject to ToS check). Record that as a precondition; if auth is unavailable in the harness, drop to L5 against a stubbed CLI subprocess and note the gap explicitly — do not claim L6 without a real run.
- L5 harness: a stub standing in for the Claude Code CLI that writes a branch; expected final assertion — `Result.Branch` equals the stub-produced branch, `Result.OK == true`.
- **Cross-module state risk:** none new.
- **Runtime-visible surface:** subprocess invocation + produced git branch.

## Out of scope
- Gemini / local-LLM executors and the multi-provider router (deferred v1, designed against the seam, not built now).
- The agent loop (task 012).

## Notes
- Bootstrap uses this SINGLE executor; the multi-provider router is a deferred v1 feature designed against the `Executor` seam.
- Keep the executor seam clean for the north-star `(harness, model) → branch` end state — no router hacks here.
- Updates `docs/spec/configuration.md` in the same commit (token handling).
