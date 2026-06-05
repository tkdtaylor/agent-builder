# Task 012: Agent loop state machine (pick → attempt → verify → next)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L5)

## Goal
Implement the inside-the-box cycle: pick a task via the task-source, run the Executor against the worktree, run the Gate; on pass record the branch and mark done, on fail emit a fail outcome for the escalation policy, then advance to the next task — happy-path state machine only.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 pick→attempt→verify; §3 verification gate is definition of done)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.2)
- Related ADRs: ADR 012 — agent-loop state-machine shape (states + outcome type)
- Dependencies: 002 (Gate), 010 (task source); uses the `Executor` interface (from 001)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A loop / state-machine type driving the pick → attempt → verify → advance cycle | must have |
| REQ-002 | On Gate pass, produce a done outcome that carries the resulting branch | must have |
| REQ-003 | On Gate fail, emit a fail outcome consumed by the escalation policy (013); the loop must not itself decide retry count | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 002, 010

## Acceptance criteria
- [x] [REQ-001] A loop type advances through pick → attempt → verify → advance; state transitions are observable in test
- [x] [REQ-002] With a fake Executor returning a branch and a fake Gate returning pass, the loop emits a done outcome carrying that branch
- [x] [REQ-003] With a fake Gate returning fail, the loop emits a fail outcome (no retry decision made in the loop) and the cycle is suspended pending the policy

## Verification plan
- **Highest level achievable:** L5 — driven with a fake `Executor` + fake `Gate`; state transitions and outcomes are observable in test for both pass and fail paths.
- Harness: `go test ./internal/loop/... ./tests/loop/...`. Expected final assertion: on pass, outcome == done with the expected branch; on fail, outcome == fail with no retry count set.
- **Cross-module state risk:** names the loop outcome / state type (new type in `internal/loop`). Updates `docs/spec/<file>.md` in same commit (new loop outcome contract).
- **Runtime-visible surface:** none (internal state machine; observability deferred).

## Out of scope
- Retry-N / mandatory stop condition / escalation (task 013)
- Real containment box (task 017)
- Real executor wiring (task 022)

## Notes
- The fail outcome is intentionally policy-free: the loop reports the failure; 013 decides what to do about it. Keeps the seam clean.
- `Executor.Run(Task)(Result,error)` and `Gate.Verify(repoPath) gate.Verdict` are the consumed seams.
