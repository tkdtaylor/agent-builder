# Test Spec 005: dep-scan blocking gate step (supply-chain CVE)

**Linked task:** [`docs/tasks/backlog/005-gate-dep-scan.md`](../backlog/005-gate-dep-scan.md)
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
### TC-001: Clean module passes the scan step
- **Requirement:** REQ-001, REQ-002
- **Input:** Go module with no high+ severity findings (clean fixture or stubbed clean scanner output)
- **Expected output:** Step ok
- **Edge cases:** low/medium-only findings do not fail (define threshold in impl)

### TC-002: High+ severity finding fails the step
- **Requirement:** REQ-002, REQ-003
- **Input:** module with a known-vulnerable dependency (or stubbed scanner output reporting a high+ CVE)
- **Expected output:** Step fails; StepResult output contains the finding
- **Edge cases:** mixed findings — any single high+ fails

### TC-003: Missing dep-scan is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH without dep-scan/`gods` (or stubbed lookpath miss)
- **Expected output:** Step fails and names the missing tool; no skip route exercised
- **Edge cases:** confirm there is no env/flag that converts absence into a pass

## Notes
Framework: Go `testing` (table-driven). Mocking: prefer a seam that lets tests inject a fake scanner runner returning canned output/exit code, so CVE assertions are deterministic without a live external tool; TC-003 asserts the hard-failure path explicitly.
