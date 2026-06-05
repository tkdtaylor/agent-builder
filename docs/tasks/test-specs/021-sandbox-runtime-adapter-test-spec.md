# Test Spec 021: sandbox-runtime backing adapter (bootstrap isolation)

**Linked task:** [`docs/tasks/backlog/021-sandbox-runtime-adapter.md`](../backlog/021-sandbox-runtime-adapter.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |

## Test cases
### TC-001: command runs isolated in sandbox-runtime
- **Requirement:** REQ-001
- **Input:** a trivial command (e.g. `echo hello`) + an isolated worktree
- **Expected output:** exit code 0, expected stdout captured; execution confined to the worktree
- **Edge cases:** non-zero exit code surfaced; command not found surfaced as error

### TC-002: allowlisted egress permitted
- **Requirement:** REQ-002
- **Input:** a command reaching an allowlisted host
- **Expected output:** egress succeeds
- **Edge cases:** —

### TC-003: non-allowlisted egress blocked
- **Requirement:** REQ-002
- **Input:** a command reaching a host absent from the allowlist
- **Expected output:** egress denied by the dual proxy / allowlist
- **Edge cases:** DNS-only vs full-connect attempts both denied

### TC-004: swap-compatible behind the 020 interface
- **Requirement:** REQ-003
- **Input:** supervisor wired with this adapter in place of the fake backend
- **Expected output:** compiles and runs with no caller-side change
- **Edge cases:** —

## Notes
Framework: Go `testing` + an L5/L6 harness driving `@anthropic-ai/sandbox-runtime` as a subprocess. Strategy: integration test gated on sandbox-runtime + bubblewrap availability; assert both allow and deny egress paths. Unit-level interface conformance via the 020 contract.
