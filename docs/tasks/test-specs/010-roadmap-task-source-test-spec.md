# Test Spec 010: Roadmap task-source reader (read-only)

**Linked task:** [`docs/tasks/backlog/010-roadmap-task-source.md`](../backlog/010-roadmap-task-source.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |

## Test cases
### TC-001: parse fixture task files into candidate Tasks
- **Requirement:** REQ-001
- **Input:** fixture dir with N task files + a roadmap, varied statuses and deps
- **Expected output:** candidate `Task` set with correct IDs, deps, and parsed statuses
- **Edge cases:** malformed/missing status marker; missing dependency reference

### TC-002: select deterministically-first ready task
- **Requirement:** REQ-002
- **Input:** fixture with several ready tasks (deps complete, status not-started)
- **Expected output:** the deterministically-first ready task is returned; repeat runs return the same task
- **Edge cases:** tie-break by ID ordering

### TC-003: exclude tasks with unmet deps or non-ready status
- **Requirement:** REQ-002
- **Input:** fixture where the lowest-ID task has an incomplete dependency / done status
- **Expected output:** that task is skipped; next eligible task selected; no ready task → empty/sentinel result
- **Edge cases:** all tasks blocked; cyclic dependency

### TC-004: read-only — zero writes during parse + select
- **Requirement:** REQ-003
- **Input:** read-only fixture (or write-tripwire fake FS)
- **Expected output:** parse + selection complete with zero write/create opens
- **Edge cases:** attempted write surfaces as a test failure, not a silent no-op

## Notes
Framework: Go `testing` (table-driven). Fixture: temp dir of task `.md` files; read-only enforcement via a write-detecting `fs.FS` wrapper or 0444-mode fixture. No real loop, executor, or gate involved.
