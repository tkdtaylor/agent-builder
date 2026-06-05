# Test Spec 023: CLI subcommand surface (run / version / verify)

**Linked task:** [`docs/tasks/backlog/023-cli-subcommands.md`](../backlog/023-cli-subcommands.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002 | ❌ |
| REQ-002 | TC-003, TC-004 | ❌ |
| REQ-003 | TC-005 | ❌ |

## Test cases
### TC-001: version prints the version, exits 0
- **Requirement:** REQ-001
- **Input:** `agent-builder version`
- **Expected output:** version string on stdout, exit code 0
- **Edge cases:** —

### TC-002: run dispatches the loop
- **Requirement:** REQ-001
- **Input:** `agent-builder run` (with stubbed supervisor)
- **Expected output:** the supervisor run path is invoked; exit code 0 on success
- **Edge cases:** run failure → exit code 1

### TC-003: verify on a clean (gate-passing) repo exits 0
- **Requirement:** REQ-002
- **Input:** `agent-builder verify <clean-repo>`
- **Expected output:** exit code 0
- **Edge cases:** —

### TC-004: verify on a dirty (gate-failing) repo exits non-zero
- **Requirement:** REQ-002
- **Input:** `agent-builder verify <dirty-repo>`
- **Expected output:** non-zero exit code; no flag exists to skip the gate
- **Edge cases:** missing repo path → usage error (exit 2)

### TC-005: unknown subcommand exits 2
- **Requirement:** REQ-003
- **Input:** `agent-builder bogus`
- **Expected output:** usage error on stderr, exit code 2
- **Edge cases:** missing required arg → exit 2

## Notes
Framework: Go `testing` for flag parsing + dispatch; L5/L6 shell harness invoking the built binary against fixture repos (one gate-passing, one gate-failing). Stub the Gate / supervisor at unit level; use the real Gate at harness level.
