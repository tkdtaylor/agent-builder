# Test Spec 006: code-scanner blocking gate step (malware/backdoor)

**Linked task:** [`docs/tasks/backlog/006-gate-code-scanner.md`](../backlog/006-gate-code-scanner.md)
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
### TC-001: Clean worktree passes the scan step
- **Requirement:** REQ-001, REQ-002
- **Input:** clean worktree fixture (or stubbed clean scanner output)
- **Expected output:** Step ok
- **Edge cases:** empty diff (define behaviour — scan worktree vs diff)

### TC-002: Flagged pattern fails the step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** worktree containing a benign-but-flagged pattern (e.g. a credential-harvest-shaped snippet) or stubbed scanner output reporting a finding
- **Expected output:** Step fails; StepResult output contains the finding
- **Edge cases:** multiple findings all surfaced

### TC-003: Missing code-scanner is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH without code-scanner (or stubbed lookpath miss)
- **Expected output:** Step fails and names the missing tool; no skip route exercised
- **Edge cases:** confirm there is no env/flag that converts absence into a pass

## Notes
Framework: Go `testing` (table-driven). Mocking: inject a fake scanner runner returning canned findings/exit code so assertions are deterministic without a live external tool; TC-003 asserts the hard-failure path explicitly.
