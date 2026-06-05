# Test Spec 026: armor on the web-ingestion / tool-call path

**Linked task:** [`docs/tasks/completed/026-armor-ingestion-wiring.md`](../completed/026-armor-ingestion-wiring.md)
**Written:** 2026-06-04
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002, TC-004, TC-005 | ✅ |
| REQ-003 | TC-003, TC-005 | ✅ |
| REQ-004 | TC-006 | ✅ |

## Test cases
### TC-001: benign ingested content passes through armor to executor context
- **Requirement:** REQ-001
- **Input:** an executor-facing benign web-ingestion fixture plus an armor runner returning `allow`.
- **Expected output:** the executor-facing harness constructs a content candidate, the armor runner receives a matching content request, the broker records an `allow` decision, and content reaches the continuation only after broker release.
- **Edge cases:** empty content is still reviewed by armor and released only for `allow`.

### TC-002: known injection is blocked / quarantined
- **Requirement:** REQ-002
- **Input:** a known prompt-injection web-ingestion fixture plus an armor runner returning `block` or `quarantine` with finding metadata.
- **Expected output:** the armor request preserves the content and provenance; the broker decision is `block` or `quarantine`; the continuation is not invoked.
- **Edge cases:** `allow` responses with findings are treated as blocks by the armor adapter.

### TC-003: unsafe tool call is blocked before execution
- **Requirement:** REQ-003
- **Input:** an executor-facing tool-call fixture plus an armor runner returning `block` or `quarantine` for an unsafe tool.
- **Expected output:** the executor-facing harness constructs a tool-call candidate, the armor runner receives a matching tool-call request, and the tool executor callback is not invoked for non-allow decisions.
- **Edge cases:** malformed tool-call arguments are rejected before armor invocation.

### TC-004: armor unavailable fails closed
- **Requirement:** REQ-002
- **Input:** armor absent, misconfigured, runner error, and timeout fixtures.
- **Expected output:** ingestion/tool-call candidates are blocked fail-closed; content continuations and tool executors are not invoked.
- **Edge cases:** timeout is reported as a fail-closed block decision.

### TC-005: live executor path uses the guarded boundary
- **Requirement:** REQ-001, REQ-003
- **Input:** the armor-guarded executor-facing harness with benign content, injection content, safe tool call, unsafe tool call, and armor-unavailable fixtures.
- **Expected output:** producer-consumer trace proves executor-facing events produce candidates before armor consumes them, and only armor `allow` decisions release to the continuation/executor.
- **Edge cases:** direct executor web/tool route bypassing the boundary fails through task 027 release-token checks.

### TC-006: armor invoked as a seam, not modified
- **Requirement:** REQ-004
- **Input:** the wiring code with a fake external armor runner.
- **Expected output:** wiring invokes the armor adapter through `armor.Runner` / external command configuration and does not vendor or edit armor source.
- **Edge cases:** process-backed armor command configuration remains available through `armor.Config.Command`.

## Notes
Framework: Go `testing`. Strategy: stub/fake armor backend for unit tests (configurable allow/flag/unavailable); L6 harness feeds real known benign/injection/tool-call fixtures through the executor-facing path and observes the block decision.
