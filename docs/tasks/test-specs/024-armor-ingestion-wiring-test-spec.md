# Test Spec 024: armor on the web-ingestion / tool-call path

**Linked task:** [`docs/tasks/backlog/024-armor-ingestion-wiring.md`](../backlog/024-armor-ingestion-wiring.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003, TC-004 | ❌ |

## Test cases
### TC-001: benign ingested content passes through armor to the loop
- **Requirement:** REQ-001
- **Input:** a benign web-ingestion fixture
- **Expected output:** armor allows it; content reaches the executor's loop
- **Edge cases:** empty content handled without error

### TC-002: known injection is blocked / quarantined
- **Requirement:** REQ-002
- **Input:** a known prompt-injection fixture
- **Expected output:** armor flags it; content is blocked/quarantined and does not reach the loop
- **Edge cases:** obfuscated/encoded injection variant also caught (per armor capability)

### TC-003: armor invoked as a seam, not modified
- **Requirement:** REQ-003
- **Input:** the wiring code
- **Expected output:** armor is called as an external tool/service; no armor source edited
- **Edge cases:** —

### TC-004: armor unavailable fails closed
- **Requirement:** REQ-003
- **Input:** armor absent / misconfigured
- **Expected output:** ingestion is blocked (fail closed), not passed through (fail open)
- **Edge cases:** timeout treated as a block

## Notes
Framework: Go `testing`. Strategy: stub/fake armor backend for unit tests (configurable allow/flag/unavailable); L6 harness feeds a real known-injection fixture through the ingestion path and observes the block decision.
