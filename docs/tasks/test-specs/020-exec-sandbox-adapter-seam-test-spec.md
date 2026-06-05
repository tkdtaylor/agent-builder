# Test Spec 020: exec-sandbox run() adapter seam

**Linked task:** [`docs/tasks/backlog/020-exec-sandbox-adapter-seam.md`](../backlog/020-exec-sandbox-adapter-seam.md)
**Written:** 2026-06-04
**Status:** complete — implementation must satisfy every TC marker below before the feature commit

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-001A, TC-001B, TC-001C | ✅ |
| REQ-002 | TC-002, TC-002A, TC-002B | ✅ |
| REQ-003 | TC-003, TC-004 | ✅ |

## Test cases
### TC-001: run() interface returns result + exit code for a command
- **Requirement:** REQ-001
- **Input:** construct an adapter behind the exported run interface and call it with a request containing command argv, a worktree path, and a typed limits struct.
- **Expected output:** the returned result carries captured stdout, captured stderr, and a duration; the separate exit code is returned as an integer; success returns nil error.
- **Assertion requirement:** the Go test must name `TC-001` next to assertions that a backend typed as the interface can return a result, exit code, and nil error for a valid request.

### TC-001A: non-zero exit code is surfaced, not converted to adapter error
- **Requirement:** REQ-001
- **Input:** a configured backend response with `ExitCode` set to a non-zero value and no backend error.
- **Expected output:** `Run` returns the same non-zero exit code and a nil error; callers can distinguish process failure from backend failure.
- **Assertion requirement:** the Go test must name `TC-001A` next to assertions for non-zero exit code and nil error.

### TC-001B: empty command is rejected before backend execution
- **Requirement:** REQ-001
- **Input:** a request whose command argv is empty or whose first argv element is blank.
- **Expected output:** `Run` returns an invalid-command error, exit code `0`, an empty result, and the fake backend records no execution.
- **Assertion requirement:** the Go test must name `TC-001B` next to assertions for the error and unchanged fake call count.

### TC-001C: limits are typed, not an unstructured map
- **Requirement:** REQ-001
- **Input:** inspect the public request and limits shapes from Go tests.
- **Expected output:** the request contains a typed limits struct with named fields for wall-clock timeout, memory, CPU, and egress allowlist; no `map[string]...` limits field exists.
- **Assertion requirement:** the Go test must name `TC-001C` next to reflection assertions that the limits field is the typed limits struct and not a map.

### TC-002: fake backend satisfies the interface
- **Requirement:** REQ-002
- **Input:** fake backend constructed with a canned result and exit code.
- **Expected output:** deterministic result without invoking any real isolation runtime.
- **Assertion requirement:** the Go test must name `TC-002` next to assertions that the fake implements the interface, returns the canned response, and records exactly the request it received.

### TC-002A: fake backend can return backend errors
- **Requirement:** REQ-002
- **Input:** fake backend configured with an adapter/backend error.
- **Expected output:** `Run` returns that error, preserving deterministic failure-path tests.
- **Assertion requirement:** the Go test must name `TC-002A` next to assertions that the configured error is returned.

### TC-002B: fake backend can queue multiple deterministic responses
- **Requirement:** REQ-002
- **Input:** fake backend configured with two canned responses, then invoked twice.
- **Expected output:** first invocation returns the first response; second invocation returns the second response; both requests are recorded in order.
- **Assertion requirement:** the Go test must name `TC-002B` next to assertions for ordered responses and ordered request records.

### TC-003: supervisor depends only on the interface
- **Requirement:** REQ-003
- **Input:** supervisor constructed with a fake backend through the supervisor configuration API.
- **Expected output:** the supervisor compiles and stores the dependency behind the run interface type; existing stubbed `Run()` behaviour remains unchanged until the dispatch lifecycle task.
- **Assertion requirement:** the Go test must name `TC-003` next to compile/runtime assertions that the supervisor accepts a value typed as the interface, not a concrete backend.

### TC-004: no concrete backend imported by supervisor
- **Requirement:** REQ-003
- **Input:** static check of supervisor package imports
- **Expected output:** no concrete isolation-backend package referenced
- **Assertion requirement:** the Go test must name `TC-004` next to assertions over `go list` or parsed imports confirming the supervisor package imports the seam package and no concrete backend package.

## Fixture and harness contract
- Tests live under `tests/` when they need TC-marker visibility outside package-local assertions; package-local tests may also include TC markers directly when they assert package internals.
- The harness command is `go test -count=1 ./internal/sandbox/... ./internal/supervisor/... ./tests/...`.
- The fake backend must not shell out, spawn containers, read global configuration, or require network access.
- Empty-command validation must happen in the seam/fake path before appending a recorded request, so tests can prove invalid requests do not reach backend execution.

## Notes
Framework: Go `testing`. Fixture/mocking strategy: in-process fake backend implementing the run() interface; assert supervisor wires against the interface, not a concrete backend. This task does not verify real isolation.
