# Test Spec 022: Claude Code CLI executor adapter

**Linked task:** [`docs/tasks/backlog/022-claude-cli-executor.md`](../backlog/022-claude-cli-executor.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |

## Test cases
### TC-001: Run invokes the CLI subprocess against the worktree
- **Requirement:** REQ-001
- **Input:** a trivial fixture Task with a worktree
- **Expected output:** the Claude Code CLI subprocess is invoked against that worktree
- **Edge cases:** subprocess non-zero exit surfaced as error / `Result.OK == false`

### TC-002: produced branch captured into Result.Branch
- **Requirement:** REQ-002
- **Input:** a CLI run (or stub) that produces a branch
- **Expected output:** `Result.Branch` equals the produced branch name
- **Edge cases:** no branch produced → error, not a silent empty Branch

### TC-003: Result.OK reflects subprocess success
- **Requirement:** REQ-002
- **Input:** successful vs failed subprocess run
- **Expected output:** `Result.OK` true on success, false on failure
- **Edge cases:** —

### TC-004: auth token supplied as revocable credential
- **Requirement:** REQ-003
- **Input:** executor configured with a token
- **Expected output:** token passed to the subprocess via the documented mechanism; not hard-coded
- **Edge cases:** missing token → fail fast with a clear error

## Notes
Framework: Go `testing`. Strategy: stubbed CLI subprocess (a fake binary/script writing a branch) for L5 unit/integration; L6 gated on interactive auth availability — record auth as a precondition.
