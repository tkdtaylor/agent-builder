# Test Spec 024: web-ingestion and tool-call boundary

**Linked task:** [`docs/tasks/completed/024-ingestion-tool-call-boundary.md`](../completed/024-ingestion-tool-call-boundary.md)
**Written:** 2026-06-05
**Status:** complete — implementation target

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-004 | ✅ |
| REQ-002 | TC-002, TC-004 | ✅ |
| REQ-003 | TC-003, TC-004, TC-005 | ✅ |
| REQ-004 | TC-006 | ✅ |

## Test cases
### TC-001: web content candidate carries provenance
- **Requirement:** REQ-001
- **Input:** benign fetched-content fixture with URI, media type, body, retrieval timestamp or equivalent metadata, and task/executor provenance.
- **Expected output:** `ingestion.NewContentCandidate` preserves the typed fields, copies content bytes, accepts a caller-supplied correlation ID, and deterministically derives the same non-empty ID for the same input when no ID is supplied.
- **Edge cases:** empty body is valid, missing media type is represented as `application/octet-stream`, and unsupported URI schemes are rejected before guard invocation.

### TC-002: tool-call candidate carries typed request data
- **Requirement:** REQ-002
- **Input:** tool-call fixture with tool name, typed arguments, target URI/resource when applicable, and task/executor provenance.
- **Expected output:** `ingestion.NewToolCallCandidate` preserves the tool name, JSON arguments, optional target, provenance, and accepts or deterministically derives a stable non-empty correlation ID before any execution occurs.
- **Edge cases:** blank tool name and malformed JSON arguments are rejected before guard invocation; blank target is allowed and non-blank target URI schemes are validated.

### TC-003: allowed candidates are released
- **Requirement:** REQ-003
- **Input:** fake guard returns `allow` for benign web and tool-call candidates.
- **Expected output:** `Broker.ReviewContent` and `Broker.ReviewToolCall` return decisions with outcome `allow`; `Release` returns the original candidate data.
- **Edge cases:** candidate body/arguments are not mutated by the broker or fake guard.

### TC-004: flagged candidates are blocked or quarantined
- **Requirement:** REQ-001, REQ-002, REQ-003
- **Input:** fake guard returns `block` or `quarantine` for web content and tool-call fixtures.
- **Expected output:** broker returns the guard decision, preserves the reason and candidate correlation ID, and `Release` returns `false` so the candidate cannot reach the executor path.
- **Edge cases:** block/quarantine reason is preserved.

### TC-005: guard failures fail closed
- **Requirement:** REQ-003
- **Input:** fake guard returns error, timeout, unavailable, and malformed-result responses.
- **Expected output:** broker emits a fail-closed `block` decision for guard error, nil/unavailable guard, context timeout, and malformed decision output; no candidate is released.
- **Edge cases:** timeout path uses context cancellation and does not rely on long sleeps.

### TC-006: supervisor stays dependency-free
- **Requirement:** REQ-004
- **Input:** production import graph after the boundary package is added.
- **Expected output:** `make fitness-supervisor-isolation` remains green; the supervisor has no web-fetch, LLM, executor-tooling, armor, or ingestion imports.
- **Edge cases:** dependency through an intermediate package is caught by F-003.

## Notes
Framework: Go `testing`. Strategy: deterministic fake guard and broker tests under `tests/ingestion`; F-003 covers supervisor dependency isolation. This task does not require the real armor runtime.
