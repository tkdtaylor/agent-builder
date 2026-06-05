# Task 010: Roadmap task-source reader (read-only)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (code merged + green; pending formal spec-verifier pass before ✅)

## Goal
A read-only component that parses the roadmap and task files and yields the next actionable `Task` whose dependencies are satisfied and whose status is not-started.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§6 the internal design hub read-mostly; §2 pick task)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.2)
- Related ADRs: none yet
- Dependencies: 001

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Parse task files / roadmap into a set of candidate `Task` values (ID, Repo, Spec) with their declared dependencies and status | must have |
| REQ-002 | Select the next ready task — dependencies all complete, status not-started (❌/ready) — using a deterministic ordering | must have |
| REQ-003 | Strictly read-only: open no file for writing, create no file, mutate nothing on disk | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 001

## Acceptance criteria
- [x] [REQ-001] Given a fixture directory of task files + a roadmap, the reader returns the expected candidate `Task` set with parsed status and dependencies
- [x] [REQ-002] `Next()` (or equivalent) returns the deterministically-first ready task; tasks with unmet deps or non-ready status are excluded; same input yields same selection every run
- [x] [REQ-003] No file is opened for writing during parse or selection; a read-only fixture (or write-detecting fake FS) confirms zero writes

## Verification plan
- **Highest level achievable:** L5 — pure-Go component driven by a fixture set of task files; selection order and read-only behaviour are observable in test.
- Harness: `go test ./internal/tasksource/...`. Expected final assertion: selected task ID matches the expected deterministic next, and a write-tripwire records zero write opens.
- **Cross-module state risk:** none (read-only; consumes `Task` from the supervisor seam).
- **Runtime-visible surface:** none.

## Out of scope
- Writing task status back to disk (task 011)
- The agent loop / state machine itself (task 012)

## Notes
- Read-only is the load-bearing invariant (autonomous-builder.md §6): the agent consumes the roadmap, it does not author or reprioritize it.
- Deterministic ordering matters so the loop's behaviour is reproducible and testable.
- Reuses `Task{ID,Repo,Spec}` from `internal/supervisor`.
