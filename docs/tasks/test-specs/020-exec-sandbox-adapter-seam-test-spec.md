# Test Spec 020: exec-sandbox run() adapter seam

**Linked task:** [`docs/tasks/backlog/020-exec-sandbox-adapter-seam.md`](../backlog/020-exec-sandbox-adapter-seam.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003, TC-004 | ❌ |

## Test cases
### TC-001: run() interface returns result + exit code for a command
- **Requirement:** REQ-001
- **Input:** a command, a worktree path, a limits struct
- **Expected output:** a result carrying captured output and an exit code; nil error on success
- **Edge cases:** non-zero exit code surfaced (not swallowed); empty command rejected

### TC-002: fake backend satisfies the interface
- **Requirement:** REQ-002
- **Input:** fake backend constructed with canned responses
- **Expected output:** deterministic result without invoking any real isolation runtime
- **Edge cases:** fake can be configured to return an error / non-zero exit for failure-path tests

### TC-003: supervisor depends only on the interface
- **Requirement:** REQ-003
- **Input:** supervisor constructed with the fake backend
- **Expected output:** compiles and runs against the interface type
- **Edge cases:** —

### TC-004: no concrete backend imported by supervisor
- **Requirement:** REQ-003
- **Input:** static check of supervisor package imports
- **Expected output:** no concrete isolation-backend package referenced
- **Edge cases:** —

## Notes
Framework: Go `testing`. Fixture/mocking strategy: in-process fake backend implementing the run() interface; assert supervisor wires against the interface, not a concrete type.
