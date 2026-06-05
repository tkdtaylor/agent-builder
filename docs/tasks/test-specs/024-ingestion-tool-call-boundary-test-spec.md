# Test Spec 024: web-ingestion and tool-call boundary

**Linked task:** [`docs/tasks/backlog/024-ingestion-tool-call-boundary.md`](../backlog/024-ingestion-tool-call-boundary.md)
**Written:** 2026-06-05
**Status:** stub — flesh out fully before implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-004 | ❌ |
| REQ-002 | TC-002, TC-004 | ❌ |
| REQ-003 | TC-003, TC-004, TC-005 | ❌ |
| REQ-004 | TC-006 | ❌ |

## Test cases
### TC-001: web content candidate carries provenance
- **Requirement:** REQ-001
- **Input:** benign fetched-content fixture with URI, media type, body, retrieval timestamp or equivalent metadata, and task/executor provenance.
- **Expected output:** the boundary preserves all typed fields and assigns or accepts a stable correlation ID.
- **Edge cases:** empty body, missing media type, and unsupported scheme are rejected or represented explicitly.

### TC-002: tool-call candidate carries typed request data
- **Requirement:** REQ-002
- **Input:** tool-call fixture with tool name, typed arguments, target URI/resource when applicable, and task/executor provenance.
- **Expected output:** the boundary preserves typed arguments and stable correlation ID before any execution occurs.
- **Edge cases:** blank tool name and malformed arguments are rejected before guard invocation.

### TC-003: allowed candidates are released
- **Requirement:** REQ-003
- **Input:** fake guard returns `allow` for benign web and tool-call candidates.
- **Expected output:** broker returns releasable content/tool-call data and records an allow decision.
- **Edge cases:** candidate body/arguments are not mutated by the broker.

### TC-004: flagged candidates are blocked or quarantined
- **Requirement:** REQ-001, REQ-002, REQ-003
- **Input:** fake guard returns `block` or `quarantine` for web content and tool-call fixtures.
- **Expected output:** broker does not release the candidate to the executor path and records the decision.
- **Edge cases:** block/quarantine reason is preserved.

### TC-005: guard failures fail closed
- **Requirement:** REQ-003
- **Input:** fake guard returns error, timeout, unavailable, and malformed-result responses.
- **Expected output:** broker blocks or quarantines each candidate; no candidate is released.
- **Edge cases:** timeout path is deterministic and does not rely on long sleeps.

### TC-006: supervisor stays dependency-free
- **Requirement:** REQ-004
- **Input:** production import graph after the boundary package is added.
- **Expected output:** `make fitness-supervisor-isolation` remains green; the supervisor has no web-fetch, LLM, executor-tooling, or armor imports.
- **Edge cases:** dependency through an intermediate package is caught by F-003.

## Notes
Framework: Go `testing`. Strategy: deterministic fake guard and broker tests under `tests/ingestion`; F-003 covers supervisor dependency isolation. This task does not require the real armor runtime.
