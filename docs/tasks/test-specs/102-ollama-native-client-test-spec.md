# Test spec — Task 102: Ollama native API client

**Linked task:** `docs/tasks/backlog/102-ollama-native-client.md`
**Written:** 2026-06-28
**Status:** ready

## Context

Task 094's live investigation confirmed that the LiteLLM `openai/<model>` path
(Ollama OpenAI-compat `/v1`) returns tool calls as plain-text JSON, preventing the
Claude Code CLI from executing them. LiteLLM's `ollama_chat/<model>` path is a
brittle workaround. ADR 051 decides to drive Ollama's `/api/chat` natively.

This task produces `internal/executor/ollamaclient/` — a thin, stdlib-only HTTP
client for the Ollama `/api/chat` endpoint. It covers:
- Request construction (model, messages, tools, `stream:false`)
- Response parsing (`message.tool_calls`, `message.content`)
- Error handling (non-200, malformed JSON, empty response)

It does NOT contain the agentic loop or tool execution (task 103), the tool set
(task 104), or registry wiring (task 105).

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-102-01 | TC-102-01, TC-102-02           | yes      |
| REQ-102-02 | TC-102-03                      | yes      |
| REQ-102-03 | TC-102-04, TC-102-05           | yes      |
| REQ-102-04 | TC-102-06                      | yes      |

---

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-102-01 — Chat sends correct JSON body and parses structured tool_calls response

- **Requirement:** REQ-102-01
- **Level:** L2 (unit test with stubbed HTTP server)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** Start an `httptest.NewServer` handler that:
1. Asserts the request method is `POST` and path is `/api/chat`.
2. Decodes the request body and asserts:
   - `body.Model == "qwen3:8b"`
   - `body.Stream == false`
   - `len(body.Messages) == 2` (system + user)
   - `len(body.Tools) == 1` (a write_file tool schema)
3. Writes a `200 OK` response with body:
   ```json
   {
     "message": {
       "role": "assistant",
       "tool_calls": [
         {
           "function": {
             "name": "write_file",
             "arguments": "{\"path\":\"hello.txt\",\"content\":\"hello\"}"
           }
         }
       ]
     }
   }
   ```

Call `client.Chat(ctx, ChatRequest{Model:"qwen3:8b", Messages:[...], Tools:[...], Stream:false})`.

**Expected output:**
- `Chat` returns `(ChatResponse, nil)`.
- `resp.Message.Role == "assistant"`.
- `len(resp.Message.ToolCalls) == 1`.
- `resp.Message.ToolCalls[0].Function.Name == "write_file"`.
- `resp.Message.ToolCalls[0].Function.Arguments == "{\"path\":\"hello.txt\",\"content\":\"hello\"}"`.
- `resp.Message.Content == ""` (no content field when tool_calls present).

---

### TC-102-02 — Chat parses a plain-text content response (no tool_calls)

- **Requirement:** REQ-102-01
- **Level:** L2 (unit test with stubbed HTTP server)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** Stub server returns:
```json
{
  "message": {
    "role": "assistant",
    "content": "Task complete. Branch: task/102-test"
  }
}
```

**Expected output:**
- `Chat` returns `(ChatResponse, nil)`.
- `resp.Message.Content == "Task complete. Branch: task/102-test"`.
- `len(resp.Message.ToolCalls) == 0` (empty slice, not nil-panic on range).

---

### TC-102-03 — Chat returns a descriptive error on non-200 HTTP response

- **Requirement:** REQ-102-02
- **Level:** L2 (unit test with stubbed HTTP server)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input A (500):** Stub server writes `500 Internal Server Error` with body
`{"error":"model not found"}`.

**Expected output A:**
- `Chat` returns a non-nil error.
- The error message contains `"500"` and (optional) the body text or truncation
  of the body up to a reasonable limit (e.g. 256 bytes).
- `ChatResponse` is the zero value.

**Input B (404):** Stub server writes `404 Not Found` with body `"unknown endpoint"`.

**Expected output B:**
- `Chat` returns a non-nil error containing `"404"`.

**Rationale:** Non-200 responses from Ollama indicate model unavailability or
misconfiguration. The error must be distinguishable from a parse error.

---

### TC-102-04 — Chat returns a descriptive error on malformed JSON response

- **Requirement:** REQ-102-03
- **Level:** L2 (unit test with stubbed HTTP server)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** Stub server returns `200 OK` with body `{not valid json`.

**Expected output:**
- `Chat` returns a non-nil error containing `"json"` or `"unmarshal"` (the stdlib
  error text from `json.Decoder`).
- `ChatResponse` is the zero value.

---

### TC-102-05 — Chat returns a descriptive error when the context is cancelled

- **Requirement:** REQ-102-03
- **Level:** L2 (unit test with stubbed HTTP server)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** Pass an already-cancelled `context.Context` to `client.Chat`.

**Expected output:**
- `Chat` returns a non-nil error.
- `errors.Is(err, context.Canceled)` is true (the ctx error propagates).

**Rationale:** The supervisor's wall-clock kill sends context cancellation. The client
must not block past the deadline.

---

### TC-102-06 — NewClient rejects a blank base URL

- **Requirement:** REQ-102-04
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** Call `ollamaclient.NewClient("")`.

**Expected output:**
- Returns a non-nil error containing `"blank"` or `"base URL"` (exact text is
  implementation-defined but must be a non-empty descriptive message).
- The returned `*Client` is nil.

**Rationale:** A blank base URL produces a silent failure at call time (malformed
URL). Reject it eagerly at construction.

---

## Verification plan

- **Highest level achievable in CI:** L2 — all tests use an `httptest.NewServer`
  stub; no real Ollama process is required.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/ollamaclient/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L3 import-graph:** `go list -deps ./internal/executor/ollamaclient/...` must
  show no `internal/supervisor`, `internal/registry`, `internal/runtime`, or
  `internal/executor` imports. The client is a leaf; it imports only stdlib.
- **L6 (deferred, operator-run):** point `NewClient` at a running Ollama instance
  at `http://localhost:11434` and call `Chat` with `qwen3:8b` and a trivial message.
  Confirm a non-error response is returned with a non-empty `message.content` or
  `message.tool_calls`. Hardware-specific; not CI-automatable.

## Out of scope

- The agentic loop that calls this client in a cycle (task 103).
- The tool execution set (task 104).
- Registry wiring and the new `HarnessDriver` constant (task 105).
- Streaming responses (`stream:true`) — not needed for the agentic loop.
- Authentication with Ollama — Ollama runs on localhost and requires no auth token.
