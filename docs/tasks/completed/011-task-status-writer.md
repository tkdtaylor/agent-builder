# Task 011: Task status writer (status-only governance)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L5)

## Goal
Flip a task's STATUS (done / blocked / needs-human) in its source file, editing only the status field/marker and never the plan prose, priorities, or ordering — enforcing the the internal design hub read-mostly invariant.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§6 governance line 2 — the internal design hub read-mostly; status-only writes permitted)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.2)
- Related ADRs: none yet
- Dependencies: 010

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Update the status of a given task (done / blocked / needs-human) in its source file | must have |
| REQ-002 | A diff after the write touches only the status line(s); assert nothing else (prose, priority, ordering) changed | must have |
| REQ-003 | Reject/refuse any attempt to edit non-status content — the writer has no path that mutates anything but the status marker | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 010

## Acceptance criteria
- [x] [REQ-001] Calling the writer with a task ID + target status updates that task's status marker in its source file
- [x] [REQ-002] A text/`git diff` of the file before vs after shows only the status line(s) changed; all other bytes are byte-for-byte identical
- [x] [REQ-003] The API exposes no way to write non-status content; any such request is refused (error), not silently applied

## Verification plan
- **Highest level achievable:** L5 — writes to a fixture task file and asserts a minimal diff (status-only) against the original.
- Harness: `go test ./internal/tasksource/...` (or status-writer package). Expected final assertion: diff is non-empty only on the status line; all other lines unchanged.
- **Cross-module state risk:** writes to roadmap / task files on disk — names the status marker contract it edits. Updates `docs/spec/<file>.md` in same commit if it changes the documented status-write contract.
- **Runtime-visible surface:** file output (mutated task file).

## Out of scope
- Choosing which task to run (task 010)
- Escalation semantics / when needs-human is decided (task 013)

## Notes
- This is the only sanctioned write path into the internal design hub. The minimal-diff guarantee is the enforcement of the read-mostly invariant — a wide diff is a bug, not a style nit.
- Prefer a targeted line/marker rewrite over re-serializing the whole file, so prose and ordering cannot drift.
