# Test Spec 004: golangci-lint gate step

**Linked task:** [`docs/tasks/backlog/004-gate-golangci-lint.md`](../backlog/004-gate-golangci-lint.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-001, TC-002 | ❌ |
| REQ-003 | TC-002 | ❌ |
| REQ-004 | TC-003 | ❌ |

## Test cases
### TC-001: Clean repo passes the lint step
- **Requirement:** REQ-001, REQ-002
- **Input:** clean testdata Go module
- **Expected output:** Step ok
- **Edge cases:** module with no lintable files

### TC-002: Lint violation fails the step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** fixture with a known violation (e.g. unchecked error)
- **Expected output:** Step fails; StepResult output contains the finding
- **Edge cases:** multiple findings all surfaced

### TC-003: Missing golangci-lint is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH without golangci-lint (or stubbed lookpath)
- **Expected output:** Step fails and names the missing binary; never silently passes
- **Edge cases:** binary present but config absent (define behaviour)

## Notes
Framework: Go `testing` (table-driven). Fixtures: `testdata/` clean + violating Go modules. Skip gracefully (with a recorded reason) if the binary is unavailable in CI only where the absence itself is the assertion (TC-003).
