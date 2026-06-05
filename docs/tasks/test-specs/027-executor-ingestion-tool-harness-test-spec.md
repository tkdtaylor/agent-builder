# Test Spec 027: executor ingestion/tool-call harness

**Linked task:** [`docs/tasks/completed/027-executor-ingestion-tool-harness.md`](../completed/027-executor-ingestion-tool-harness.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002, TC-005 | ✅ |
| REQ-003 | TC-003, TC-004, TC-005 | ✅ |
| REQ-004 | TC-006 | ✅ |
| REQ-005 | TC-007 | ✅ |

## Test cases
### TC-001: executor-facing web event becomes a content candidate before release
- **Requirement:** REQ-001
- **Input:** executor-facing benign web-ingestion fixture with source URI, media type, bytes, retrieval time, task ID, and executor name.
- **Expected output:** the harness constructs an `ingestion.ContentCandidate` before any executor continuation receives content; the candidate preserves the fields and has a non-empty correlation ID.
- **Edge cases:** empty content is allowed; unsupported source URI is rejected before guard invocation.

### TC-002: executor-facing tool event becomes a tool-call candidate before execution
- **Requirement:** REQ-002
- **Input:** executor-facing tool-call fixture with tool name, JSON arguments, optional target URI, task ID, and executor name.
- **Expected output:** the harness constructs an `ingestion.ToolCallCandidate` before any execution callback runs; the candidate preserves the fields and has a non-empty correlation ID.
- **Edge cases:** blank tool name and malformed JSON arguments are rejected before guard invocation.

### TC-003: allow decisions release content and tool calls only after broker review
- **Requirement:** REQ-003
- **Input:** fake guard returns `allow` for benign content and safe tool-call fixtures.
- **Expected output:** the broker is called before release/execution; released content reaches the executor-facing continuation and released tool calls reach the execution callback exactly once.
- **Edge cases:** released bytes/arguments are not mutated by the harness or broker.

### TC-004: block, quarantine, guard error, and timeout fail closed
- **Requirement:** REQ-003
- **Input:** fake guard returns `block`, `quarantine`, error, unavailable/nil, and timeout outcomes for content and tool-call fixtures.
- **Expected output:** content does not reach executor context, tool-call execution callback is not invoked, and the decision/reason remains observable to the harness result.
- **Edge cases:** malformed guard decisions are treated as fail-closed blocks by the broker.

### TC-005: producer-consumer trace proves the live executor-facing path is wired
- **Requirement:** REQ-001, REQ-002, REQ-003
- **Input:** targeted validation harness or Go test exercising the real executor-facing candidate production path with benign content, flagged content, safe tool call, and unsafe tool call.
- **Expected output:** evidence shows producer constructs candidates before the broker consumes them, and the broker releases only `allow` decisions before continuation/execution.
- **Edge cases:** candidate IDs in guard decisions must match the produced candidate IDs and candidate kinds.

### TC-006: direct bypass fails the harness
- **Requirement:** REQ-004
- **Input:** fixture or fake executor path attempting to deliver web content or execute a tool call without broker review.
- **Expected output:** the harness/test fails or returns a blocking decision; bypass does not silently succeed.
- **Edge cases:** bypass detection covers both content continuation and tool execution callback paths.

### TC-007: supervisor import isolation remains intact
- **Requirement:** REQ-005
- **Input:** run `make fitness-supervisor-isolation`.
- **Expected output:** the supervisor import graph still contains no executor, LLM, web-fetch, ingestion, or armor dependencies.
- **Edge cases:** harness dependencies live on the in-box executor/loop side only.

## Notes
Framework: Go `testing`. Strategy: fake guard plus fake executor-facing event
producer/execution callbacks. The required L5 evidence must exercise the real
harness entrypoint added by this task; directly calling `ingestion.New*Candidate`
or `Broker.Review*` alone is not sufficient.
