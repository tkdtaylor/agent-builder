# Test Spec 011: Task status writer (status-only governance)

**Linked task:** [`docs/tasks/completed/011-task-status-writer.md`](../completed/011-task-status-writer.md)
**Written:** 2026-06-04
**Status:** complete — implementation must satisfy every TC marker below before the feature commit

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-001A, TC-001B | ✅ |
| REQ-002 | TC-002, TC-002A, TC-002B | ✅ |
| REQ-003 | TC-003, TC-003A, TC-003B | ✅ |

## Test cases
### TC-001: update status marker for a given task
- **Requirement:** REQ-001
- **Input:** fixture task file whose heading is `# Task 011:` and whose status line is `**Status:** backlog`; call the writer with task ID `011` and each target status: `done`, `blocked`, and `needs-human`.
- **Expected output:** the same fixture file contains exactly one rewritten status line, `**Status:** <target>`, and the writer reports the matched task path.
- **Assertion requirement:** the Go test must name `TC-001` next to assertions that read the file back from disk and compare the status line with the requested target.

### TC-001A: missing task ID is refused
- **Requirement:** REQ-001
- **Input:** fixture task directory with task `011`; call the writer for task ID `999`.
- **Expected output:** the call returns an error naming the missing task ID and no file content changes.
- **Assertion requirement:** the Go test must name `TC-001A` next to assertions for both the error and unchanged file bytes.

### TC-001B: already-at-target write is idempotent
- **Requirement:** REQ-001
- **Input:** fixture task file with status already `blocked`; call the writer for task ID `011` and target status `blocked`.
- **Expected output:** the call succeeds, reports the matched task path, and leaves file bytes unchanged.
- **Assertion requirement:** the Go test must name `TC-001B` next to a byte-for-byte before/after comparison.

### TC-002: minimal diff — only status line(s) change
- **Requirement:** REQ-002
- **Input:** fixture task file with prose sections, a requirements table, priorities, dependencies, blank lines, and a final newline.
- **Expected output:** after a successful status write, every byte outside the matched `**Status:**` line is identical to the original.
- **Assertion requirement:** the Go test must name `TC-002` next to an assertion that compares line prefixes/suffixes or a line-by-line diff and fails on any non-status-line change.

### TC-002A: trailing whitespace and final newline are preserved
- **Requirement:** REQ-002
- **Input:** fixture task file whose non-status lines include trailing spaces and whose file ends with a newline.
- **Expected output:** the writer preserves all non-status trailing spaces and preserves whether the original file ended with a newline.
- **Assertion requirement:** the Go test must name `TC-002A` next to exact byte comparisons for non-status content.

### TC-002B: duplicate status markers are refused
- **Requirement:** REQ-002
- **Input:** fixture task file with two `**Status:**` metadata lines for the same task.
- **Expected output:** the writer returns an error and leaves the file unchanged because it cannot prove a one-line minimal diff.
- **Assertion requirement:** the Go test must name `TC-002B` next to assertions for the error and unchanged file bytes.

### TC-003: refuse non-status edits
- **Requirement:** REQ-003
- **Input:** attempt to call the writer with a target value outside the allowed status set, such as `priority: high` or `completed`.
- **Expected output:** the writer returns an invalid-status error and leaves every candidate task file unchanged.
- **Assertion requirement:** the Go test must name `TC-003` next to assertions that malformed values cannot be used as a content-edit channel.

### TC-003A: public API accepts only task ID plus status
- **Requirement:** REQ-003
- **Input:** construct the writer through its exported API.
- **Expected output:** the only exported mutation method accepts a task ID and the constrained status type; callers cannot pass replacement prose, tables, priorities, or arbitrary patch text.
- **Assertion requirement:** the Go test must name `TC-003A` next to a compile-time call using only the status-write signature and a runtime assertion that no non-status bytes changed.

### TC-003B: malformed task status shape is refused
- **Requirement:** REQ-003
- **Input:** fixture task file for the target ID with no `**Status:**` metadata line.
- **Expected output:** the writer returns an error and leaves the file unchanged.
- **Assertion requirement:** the Go test must name `TC-003B` next to assertions for the error and unchanged file bytes.

## Fixture and harness contract
- Tests live under `tests/` so the task-executor TC-marker grep can find every `TC-*` marker in real Go assertions.
- The harness command is `go test -count=1 ./internal/tasksource/... ./tests/...`.
- Fixtures must write to `t.TempDir()` only; they must not mutate real task files.
- Minimal-diff assertions must compare bytes, not just parse status after the write.

## Notes
Framework: Go `testing` (table-driven). Fixture: temp copy of a real-shaped task file; assert minimal diff via byte comparison. No loop/executor/gate involved.
