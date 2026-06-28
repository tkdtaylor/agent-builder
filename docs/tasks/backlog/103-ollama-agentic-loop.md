# Task 103 — Ollama agentic tool-execution loop

**Status:** backlog
**ID:** 103
**Slug:** ollama-agentic-loop
**Priority:** must-have
**Dependencies:** task 102 (Ollama native client)
**Depends on tasks:** 102
**Blocks tasks:** 105

**Spec:** `docs/tasks/test-specs/103-ollama-agentic-loop-test-spec.md`
**ADR:** `docs/architecture/decisions/051-ollama-native-executor-harness.md`

---

## Goal

Produce `internal/executor/ollama_native.go` — the `OllamaNative` struct that
implements `supervisor.Executor` and drives the in-process agentic loop:

```
initial prompt → Chat → tool_calls? → dispatch to tool set → append result →
repeat until no tool_calls OR hard iteration cap → extract branch → Result{Branch, OK}
```

The Ollama HTTP client (task 102) and the tool set (task 104) are injected via
interface seams so each layer is independently testable. This task produces the
wiring loop between them.

---

## Background

ADR 051 §2 defines the loop contract. The key design decisions made there:

1. **Initial prompt** is built from `supervisor.Task` (spec text, repo, worktree path).
2. **Hard iteration cap** (`MaxIterations`, default 30) prevents runaway loops.
   When reached, return `Result{OK:false}` — this is the normal escalation signal,
   not an error.
3. **Tool results** are appended as `role:"tool"` messages before the next `Chat`
   call, maintaining conversation state.
4. **Branch extraction** reads the reserved branch file that the `finish_branch` tool
   (task 104) writes.
5. **Context cancellation** propagates from the supervisor's wall-clock kill.

---

## Requirements

### REQ-103-01 — OllamaNative satisfies supervisor.Executor and returns Result{Branch, OK}

`OllamaNative.Run(t supervisor.Task)` MUST implement `supervisor.Executor`. When the
model calls `finish_branch` (or the terminal response contains no further `tool_calls`),
`Run` MUST return `(Result{Branch: "<branch-name>", OK: true}, nil)`. The branch name
comes from the reserved branch file the tool set writes.

### REQ-103-02 — Loop maintains conversation state across turns

After each tool call, the loop MUST append the assistant's tool_call message and a
`role:"tool"` result message to the messages slice before the next `Chat` request.
The `role:"tool"` result message MUST include the tool name and a non-empty content
string (the tool's result). This is required for models to observe the results of
their own actions.

### REQ-103-03 — Hard iteration cap terminates the loop

When the loop reaches `MaxIterations` without reaching a terminal state, `Run` MUST
return `(Result{OK: false}, nil)` (not a non-nil error). The cap is an expected
termination condition; the supervisor's retry/escalation policy handles it.

### REQ-103-04 — Context cancellation is propagated

When the injected context is cancelled (supervisor wall-clock kill), `Run` MUST
return a non-nil error that satisfies `errors.Is(err, context.Canceled)` or
`errors.Is(err, context.DeadlineExceeded)`.

### REQ-103-05 — F-003 supervisor isolation is preserved

`internal/supervisor` MUST NOT import `internal/executor`, `internal/executor/ollamaclient`,
or `internal/executor/ollamatoolset`. Enforced by `make fitness-supervisor-isolation`.

---

## Interface seams

```go
package executor

// Chatter is the seam the loop uses to call the Ollama API.
// Implemented by *ollamaclient.Client in production; stub in tests.
type Chatter interface {
    Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error)
}

// ToolDispatcher is the seam the loop uses to execute tool calls.
// Implemented by *ollamatoolset.ToolSet in production; stub in tests.
type ToolDispatcher interface {
    Dispatch(toolName string, argsJSON string) (string, error)
    ToolSchemas() []ollamaclient.Tool
    ExtractBranch() (string, bool) // reads the reserved branch file
}

// OllamaNativeConfig configures the native executor.
type OllamaNativeConfig struct {
    Endpoint      string // Ollama base URL, e.g. "http://localhost:11434"
    Model         string // model ID, e.g. "qwen3:8b"
    MaxIterations int    // hard cap; 0 uses default (30)
    Worktree      string // absolute path to the task worktree
}

type OllamaNative struct { /* unexported */ }

func NewOllamaNative(cfg OllamaNativeConfig, toolset ToolDispatcher) *OllamaNative
func (o *OllamaNative) Run(t supervisor.Task) (supervisor.Result, error)
```

The `Chatter` is constructed from the config inside `NewOllamaNative` using
`ollamaclient.NewClient(cfg.Endpoint)`. Tests override by calling
`newOllamaNativeWithChatter(cfg, chatter, toolset)` (unexported test helper or
functional option).

---

## Acceptance criteria

- [ ] **AC-103-01:** TC-103-01 passes: `var _ supervisor.Executor = (*executor.OllamaNative)(nil)` compiles.
- [ ] **AC-103-02:** TC-103-02 passes: stub client → write_file dispatched → file exists at `<worktree>/BRANCH` with exact content → `Result{Branch:"task/103-test", OK:true}` → stub called exactly twice.
- [ ] **AC-103-03:** TC-103-03 passes: loop with `MaxIterations:3` and always-tool-call stub → stub called exactly 3 times → `Result{OK:false}` nil-error returned.
- [ ] **AC-103-04:** TC-103-04 passes: call 2's captured messages contain a `role:"tool"` message with the write_file tool name and non-empty content.
- [ ] **AC-103-05:** TC-103-05 passes: cancelled context → non-nil error → `errors.Is(err, context.Canceled)` true.
- [ ] **AC-103-06:** TC-103-06 passes: `make fitness-supervisor-isolation` exits 0 after adding `OllamaNative`.
- [ ] **AC-103-07:** `make check` passes without any new warnings.

---

## Verification plan

- **Highest level achievable in CI:** L2 — stub client + real temp worktree (from
  task 104 tool set). No real Ollama process is required.
- **L2 command:**
  ```
  go test -count=1 ./internal/executor/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
- **Full gate:**
  ```
  make check
  ```
- **L6 (deferred, operator-run):** after registry wiring (task 105), run
  `agent-builder run` with an `ollama-native` registry entry pointing at a live
  Ollama instance running `qwen3:8b`. Observe the full loop and branch production.
  Hardware-specific; not CI-automatable. Deferred in the same style as tasks 094/101.

---

## Out of scope

- The Ollama HTTP client type definitions (task 102).
- The tool set implementation (task 104).
- Registry wiring (task 105).
- Prompt engineering specifics — the initial prompt format is implementation-defined,
  not a spec-level contract.
