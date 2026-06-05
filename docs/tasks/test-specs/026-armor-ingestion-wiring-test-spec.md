# Test Spec 026: armor on the web-ingestion / tool-call path

**Linked task:** [`docs/tasks/backlog/026-armor-ingestion-wiring.md`](../backlog/026-armor-ingestion-wiring.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-005 | ❌ |
| REQ-002 | TC-002, TC-004 | ❌ |
| REQ-003 | TC-003, TC-005 | ❌ |
| REQ-004 | TC-006 | ❌ |

## Test cases
### TC-001: benign ingested content passes through armor to executor context
- **Requirement:** REQ-001
- **Input:** a benign web-ingestion fixture
- **Expected output:** armor allows it; content reaches executor context only after the guarded boundary releases it
- **Edge cases:** empty content handled without error

### TC-002: known injection is blocked / quarantined
- **Requirement:** REQ-002
- **Input:** a known prompt-injection fixture
- **Expected output:** armor flags it; content is blocked/quarantined and does not reach executor context
- **Edge cases:** obfuscated/encoded injection variant also caught (per armor capability)

### TC-003: unsafe tool call is blocked before execution
- **Requirement:** REQ-003
- **Input:** a tool-call fixture armor flags as unsafe
- **Expected output:** tool call is blocked/quarantined before execution
- **Edge cases:** malformed tool-call arguments rejected before guard invocation

### TC-004: armor unavailable fails closed
- **Requirement:** REQ-002
- **Input:** armor absent / misconfigured
- **Expected output:** ingestion/tool-call candidate is blocked (fail closed), not passed through (fail open)
- **Edge cases:** timeout treated as a block

### TC-005: live executor path uses the guarded boundary
- **Requirement:** REQ-001, REQ-003
- **Input:** executor-facing harness with benign web content and a safe tool-call fixture
- **Expected output:** producer-consumer trace proves executor path produces candidates that the broker/armor guard consumes before release/execution
- **Edge cases:** direct executor web/tool route bypassing the boundary fails the test

### TC-006: armor invoked as a seam, not modified
- **Requirement:** REQ-004
- **Input:** the wiring code
- **Expected output:** armor is called as an external tool/service; no armor source edited
- **Edge cases:** —

## Notes
Framework: Go `testing`. Strategy: stub/fake armor backend for unit tests (configurable allow/flag/unavailable); L6 harness feeds real known benign/injection/tool-call fixtures through the executor-facing path and observes the block decision.
