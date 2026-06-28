# Task 102 — Ollama native API client

**Status:** completed (🟡 — merged; L6 deferred)
**ID:** 102
**Slug:** ollama-native-client
**Priority:** must-have
**Dependencies:** ADR 051 (this feature's design decision — written in task 102 setup)
**Depends on tasks:** none (this is the base layer)
**Blocks tasks:** 103, 104, 105

**Spec:** `docs/tasks/test-specs/102-ollama-native-client-test-spec.md`
**ADR:** `docs/architecture/decisions/051-ollama-native-executor-harness.md`

---

## Goal

Produce a minimal, stdlib-only HTTP client for the Ollama `/api/chat` endpoint —
`internal/executor/ollamaclient/` — that covers request construction, structured
`tool_calls` parsing, content parsing, and error handling. No LiteLLM. No Claude
Code CLI. No agentic loop (that is task 103).

This is the leaf building block the agentic loop (task 103) uses to drive the model.

---

## Background

During the 2026-06-28 live investigation of task 094, the tool-call serialization
failure mode was confirmed end-to-end: LiteLLM's `openai/<model>` path (Ollama
OpenAI-compat `/v1`) returns tool calls as plain-text JSON; the Claude Code CLI
never executes them. ADR 051 decides to drive Ollama's `/api/chat` natively.
This task is the first implementation deliverable for ADR 051.

---

## Requirements

### REQ-102-01 — Chat request construction and structured tool_calls parsing

The `Client.Chat(ctx, ChatRequest)` method MUST:
- Send a `POST /api/chat` request with `Content-Type: application/json` to the
  configured base URL.
- Serialize the `ChatRequest` (model, messages, tools, `stream:false`) as JSON.
- On a `200 OK` response, decode the response body and return a `ChatResponse`
  whose `Message.ToolCalls` slice is non-nil when the model returned tool calls,
  and whose `Message.Content` is non-empty when the model returned a plain-text
  response.

### REQ-102-02 — Non-200 responses are surfaced as descriptive errors

`Client.Chat` MUST return a non-nil error on any non-200 HTTP status code. The error
message MUST include the status code. The `ChatResponse` returned is the zero value.

### REQ-102-03 — Malformed responses and context cancellation are surfaced as errors

`Client.Chat` MUST return a non-nil error when:
- The response body is not valid JSON (`json.Decoder` fails).
- The context is cancelled or its deadline is exceeded
  (`errors.Is(err, context.Canceled)` or `errors.Is(err, context.DeadlineExceeded)`
  is true).

### REQ-102-04 — Blank base URL is rejected at construction

`NewClient("")` MUST return `(nil, error)` with a non-empty descriptive message.
A blank base URL produces a silent URL-construction failure at call time; reject it
eagerly.

---

## Types and API

```go
package ollamaclient

// Tool describes one tool the model may call.
type Tool struct {
    Type     string       `json:"type"`     // "function"
    Function ToolFunction `json:"function"`
}

type ToolFunction struct {
    Name        string `json:"name"`
    Description string `json:"description"`
    Parameters  any    `json:"parameters"` // JSON Schema object
}

// Message is one turn in a conversation.
type Message struct {
    Role      string     `json:"role"`                // "system","user","assistant","tool"
    Content   string     `json:"content,omitempty"`
    ToolCalls []ToolCall `json:"tool_calls,omitempty"`
    Name      string     `json:"name,omitempty"` // tool name for role:"tool" messages
}

type ToolCall struct {
    Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
    Name      string `json:"name"`
    Arguments string `json:"arguments"` // raw JSON string
}

// ChatRequest is the payload sent to /api/chat.
type ChatRequest struct {
    Model    string    `json:"model"`
    Messages []Message `json:"messages"`
    Tools    []Tool    `json:"tools,omitempty"`
    Stream   bool      `json:"stream"`
}

// ChatResponse is what /api/chat returns (non-streaming).
type ChatResponse struct {
    Message Message `json:"message"`
}

// Client is a thin HTTP client for the Ollama /api/chat endpoint.
type Client struct { /* unexported */ }

func NewClient(baseURL string) (*Client, error)
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
```

The package imports only stdlib (`net/http`, `encoding/json`, `context`, `fmt`,
`strings`). No internal imports. It is a leaf.

---

## Acceptance criteria

- [ ] **AC-102-01:** TC-102-01 passes: `Chat` sends the correct JSON body and parses
  structured `tool_calls` from the stub server response.
- [ ] **AC-102-02:** TC-102-02 passes: `Chat` parses a plain-text content response
  with `len(ToolCalls) == 0`.
- [ ] **AC-102-03:** TC-102-03 passes: `Chat` returns a descriptive error containing
  the HTTP status code on a 500 and a 404 response.
- [ ] **AC-102-04:** TC-102-04 passes: `Chat` returns a descriptive error on malformed
  JSON.
- [ ] **AC-102-05:** TC-102-05 passes: `Chat` returns a wrapped `context.Canceled`
  error on a cancelled context.
- [ ] **AC-102-06:** TC-102-06 passes: `NewClient("")` returns `(nil, non-nil error)`.
- [ ] **AC-102-07:** `go list -deps ./internal/executor/ollamaclient/...` contains
  no `agent-builder/internal/` path (leaf check).
- [ ] **AC-102-08:** `make check` passes without any new warnings.

---

## Verification plan

- **Highest level achievable in CI:** L2 — all tests use an `httptest.NewServer`
  stub; no real Ollama process is required.
- **L2 command:**
  ```
  go test -count=1 ./internal/executor/ollamaclient/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient`
- **Full gate:**
  ```
  make check
  ```
- **L6 (deferred, operator-run):** point `NewClient` at `http://localhost:11434`
  and call `Chat` with `qwen3:8b` and a trivial message. Confirm a non-error response.
  Hardware-specific; not CI-automatable.

---

## Out of scope

- The agentic loop (task 103).
- The tool set (task 104).
- Registry wiring (task 105).
- Streaming responses.
- Authentication (Ollama on localhost requires none).
