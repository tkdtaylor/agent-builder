# Test Spec 011: Task status writer (status-only governance)

**Linked task:** [`docs/tasks/backlog/011-task-status-writer.md`](../backlog/011-task-status-writer.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003 | ❌ |

## Test cases
### TC-001: update status marker for a given task
- **Requirement:** REQ-001
- **Input:** fixture task file with status not-started; call writer with target status done/blocked/needs-human
- **Expected output:** file's status marker reflects the new status
- **Edge cases:** task ID not found; already at target status (no-op vs idempotent write)

### TC-002: minimal diff — only status line(s) change
- **Requirement:** REQ-002
- **Input:** fixture task file with prose, priority table, ordering
- **Expected output:** before/after text diff shows changes only on the status line(s); all other bytes identical
- **Edge cases:** status marker appears in multiple places; trailing-whitespace/newline preservation

### TC-003: refuse non-status edits
- **Requirement:** REQ-003
- **Input:** any attempt routed through the writer to alter prose/priority/ordering
- **Expected output:** request refused with an error; file unchanged
- **Edge cases:** malformed status value; concurrent write attempt

## Notes
Framework: Go `testing` (table-driven). Fixture: temp copy of a real-shaped task file; assert minimal diff via byte comparison or `git diff --` on a temp repo. No loop/executor/gate involved.
