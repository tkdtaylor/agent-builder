# Test Spec 010: Roadmap task-source reader (read-only)

**Linked task:** [`docs/tasks/completed/010-roadmap-task-source.md`](../completed/010-roadmap-task-source.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-005, TC-006 | ✅ |
| REQ-002 | TC-002, TC-003, TC-007 | ✅ |
| REQ-003 | TC-004 | ✅ |

## Test cases
### TC-001: parse fixture task files into candidate Tasks
- **Requirement:** REQ-001
- **Input:** `fstest.MapFS` containing `docs/plans/roadmap.md` plus task files under `docs/tasks/backlog/` and `docs/tasks/completed/`. The fixture includes ready, completed, active, and blocked task statuses plus dependencies declared as `Dependencies: 001, 002`.
- **Expected output:** `Candidates()` returns one candidate per task file, sorted deterministically by task ID. Each candidate carries the existing `supervisor.Task` shape (`ID`, `Repo`, `Spec`), normalized status, and normalized dependency IDs.
- **Assertions:** `Repo` is parsed from `**Project:**`; `Spec` is the task file path; `ID` is parsed from the `# Task NNN:` heading; dependencies are parsed from the task context; `none` / `No blocking tasks` becomes an empty dependency set.

### TC-002: select deterministically-first ready task
- **Requirement:** REQ-002
- **Input:** fixture with several ready backlog tasks whose dependencies are completed.
- **Expected output:** `Next()` returns the lowest task ID among ready candidates.
- **Assertions:** repeated calls against the same source return the same task ID and task spec path.

### TC-003: exclude tasks with unmet deps or non-ready status
- **Requirement:** REQ-002
- **Input:** fixture where the lowest-ID backlog task depends on an incomplete task, and another candidate is active or completed.
- **Expected output:** blocked, active, and completed candidates are skipped; the next eligible ready candidate is selected.
- **Assertions:** when all candidates are blocked by unmet dependencies or non-ready statuses, `Next()` returns an empty/sentinel result without an error.

### TC-004: read-only — zero writes during parse + select
- **Requirement:** REQ-003
- **Input:** a read-only `fs.FS` implementation that exposes task fixtures through `Open`/`ReadDir` and has no write method.
- **Expected output:** parse + selection complete successfully without creating or mutating files.
- **Assertions:** the production API accepts only an `fs.FS` reader and paths; the test fixture confirms every observed filesystem interaction is a read-side open/read.

### TC-005: reject malformed or incomplete task metadata
- **Requirement:** REQ-001
- **Input:** fixtures missing the `# Task NNN:` heading, `**Project:**`, or `**Status:**` field; fixture with an unrecognized status marker.
- **Expected output:** `Candidates()` returns an error naming the bad file and the missing or malformed field.
- **Assertions:** no partial candidate set is treated as authoritative after a malformed file.

### TC-006: reject dependencies that reference no parsed task
- **Requirement:** REQ-001
- **Input:** fixture where a task declares `Dependencies: 999`, but no parsed candidate has ID `999`.
- **Expected output:** `Candidates()` returns an error naming the missing dependency and the task that declared it.
- **Assertions:** dependency typos fail loudly instead of making a task permanently unready.

### TC-007: cyclic ready dependencies yield no next task
- **Requirement:** REQ-002
- **Input:** fixture where ready task `010` depends on ready task `011`, and ready task `011` depends on ready task `010`.
- **Expected output:** `Next()` returns the empty/sentinel result because neither dependency is completed.
- **Assertions:** cycles are not special-cased into a panic or infinite loop; they are naturally not ready until a dependency is completed.

## Notes
Framework: Go `testing` (table-driven). Fixture: `testing/fstest.MapFS` or a tiny read-observing `fs.FS`; no real loop, executor, gate, or status writer involved. This task adds a read-only task-source package only.
