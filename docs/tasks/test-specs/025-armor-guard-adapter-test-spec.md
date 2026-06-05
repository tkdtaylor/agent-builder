# Test Spec 025: armor guard adapter

**Linked task:** [`docs/tasks/backlog/025-armor-guard-adapter.md`](../backlog/025-armor-guard-adapter.md)
**Written:** 2026-06-05
**Status:** stub — flesh out fully before implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |
| REQ-004 | TC-005 | ❌ |

## Test cases
### TC-001: adapter invokes armor behind the guard seam
- **Requirement:** REQ-001
- **Input:** scripted armor command/service fake and a task 024 content candidate.
- **Expected output:** adapter sends the candidate through the external invocation seam and returns a boundary decision.
- **Edge cases:** candidate correlation ID is preserved in request/decision metadata.

### TC-002: benign armor result maps to allow
- **Requirement:** REQ-002
- **Input:** armor-compatible benign result fixture.
- **Expected output:** adapter returns `allow` and does not invent a block/quarantine reason.
- **Edge cases:** warnings that are not findings remain visible as metadata.

### TC-003: armor findings map to block or quarantine
- **Requirement:** REQ-002
- **Input:** injection, exfiltration, and unsafe tool-call result fixtures.
- **Expected output:** adapter returns `block` or `quarantine` with finding category and reason metadata.
- **Edge cases:** multiple findings preserve all categories or a documented highest-severity category.

### TC-004: armor invocation failures fail closed
- **Requirement:** REQ-003
- **Input:** missing command/service, timeout, malformed output, and non-zero exit fixtures.
- **Expected output:** adapter returns a fail-closed boundary decision; no candidate is allowed.
- **Edge cases:** timeout test uses a deterministic fake clock or context cancellation.

### TC-005: armor remains external
- **Requirement:** REQ-004
- **Input:** implementation diff.
- **Expected output:** no armor source tree is created, vendored, or modified in agent-builder.
- **Edge cases:** generated fixtures are clearly test fixtures, not copied armor implementation.

## Notes
Framework: Go `testing`. Strategy: fake process/service runner for deterministic adapter tests; optional real-armor fixture harness only when the runtime is installed.
