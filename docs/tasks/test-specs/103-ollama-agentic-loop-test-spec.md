# Test spec — Task 103: Ollama agentic tool-execution loop

**Linked task:** `docs/tasks/backlog/103-ollama-agentic-loop.md`
**Written:** 2026-06-28
**Status:** ready

## Context

With the Ollama native client in place (task 102) and the tool set ready (task 104),
this task wires the in-process agentic loop: the component that receives `tool_calls`
from the client, dispatches them to the tool set, appends results as `role:"tool"`
messages, and iterates until the model signals completion or the hard iteration cap
is reached.

The loop lives in `internal/executor/ollama_native.go` as `OllamaNative`, implementing
`supervisor.Executor`. It owns:
- Initial prompt construction from `supervisor.Task`
- The `Chat → tool_calls → dispatch → append → repeat` cycle
- Hard iteration cap enforcement
- Branch extraction from the worktree (or from the model's final message)
- `supervisor.Result{Branch, OK}` return

The tool set itself (write_file, read_file, list_dir, run_command, branch finalization)
lives in `internal/executor/ollamatoolset/` (task 104). The loop calls the tool set
via an interface seam so each is independently testable.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-103-01 | TC-103-01, TC-103-02           | yes      |
| REQ-103-02 | TC-103-03                      | yes      |
| REQ-103-03 | TC-103-04                      | yes      |
| REQ-103-04 | TC-103-05                      | yes      |
| REQ-103-05 | TC-103-06                      | yes      |

---

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-103-01 — OllamaNative satisfies supervisor.Executor interface

- **Requirement:** REQ-103-01
- **Level:** L2 (compile-time + unit test)
- **Test file:** `internal/executor/ollama_native_test.go`

**Input:** Compile-time assertion:
```go
var _ supervisor.Executor = (*executor.OllamaNative)(nil)
```

**Expected output:**
- Compiles without error.
- `executor.NewOllamaNative(cfg, toolset)` returns a value satisfying `supervisor.Executor`.
- `cfg` must carry at minimum `Endpoint string` (Ollama base URL) and `Model string`.

---

### TC-103-02 — Loop executes a write_file tool call and returns the produced branch

- **Requirement:** REQ-103-01, REQ-103-02
- **Level:** L2 (unit test with stub client + real temp worktree)
- **Test file:** `internal/executor/ollama_native_test.go`

**Setup:** Create a real temp directory as the worktree. Construct an `OllamaNative`
with:
- A **stub Ollama client** (implements the `ollamaclient.Chatter` or equivalent seam)
  that returns the following sequence of responses:
  1. First call: `ChatResponse{Message:{ToolCalls:[{Function:{Name:"write_file", Arguments:"{\"path\":\"BRANCH\",\"content\":\"task/103-test\"}"}}]}}`
  2. Second call: `ChatResponse{Message:{Content:"Task complete."}}`
- A **real tool set** backed by the temp worktree (from task 104).

**Input:** Call `executor.Run(supervisor.Task{ID:"103", Repo:"test-repo", Spec:"test-spec.md"})`.

**Expected output:**
- After the first stub response, the loop dispatches `write_file` with
  `path="BRANCH"` and `content="task/103-test"`. Assert the file exists at
  `<worktree>/BRANCH` with exact content `"task/103-test"`.
- After the second stub response (no `tool_calls`), the loop extracts
  `Result.Branch = "task/103-test"` from the branch file.
- `Run` returns `(Result{Branch:"task/103-test", OK:true}, nil)`.
- The stub client was called exactly twice (assert call count == 2).

---

### TC-103-03 — Loop stops and returns OK:false at the hard iteration cap

- **Requirement:** REQ-103-03
- **Level:** L2 (unit test with stub client)
- **Test file:** `internal/executor/ollama_native_test.go`

**Setup:** Construct an `OllamaNative` with `MaxIterations: 3` and a stub client that
ALWAYS returns a `tool_calls` response (so the loop never reaches a terminal state
on its own):
```json
{"message":{"tool_calls":[{"function":{"name":"read_file","arguments":"{\"path\":\"x\"}"}}]}}
```
Use a stub tool set that returns `"(stub content)"` for every `read_file` call.

**Expected output:**
- The stub client is called exactly 3 times (at the cap).
- `Run` returns `(Result{OK:false}, nil)` — a nil error (the cap is not an error,
  it is a normal escalation signal; the supervisor's retry/escalation policy handles it).
- The branch file does not exist in the worktree (no write_file was called).

**Rationale:** Confirms the cap is enforced and the return is a `Result{OK:false}`, not a
panic or an error. This is the signal the supervisor uses to escalate to a stronger executor.

---

### TC-103-04 — Loop appends tool results as role:"tool" messages in the next request

- **Requirement:** REQ-103-02
- **Level:** L2 (unit test with stub client)
- **Test file:** `internal/executor/ollama_native_test.go`

**Setup:** Construct an `OllamaNative`. Stub client:
- Call 1 returns: `tool_calls: [{write_file, args: {path:"out.txt", content:"hello"}}]`
- Call 2 returns: `content: "Done."` (terminal)

Stub client captures the `ChatRequest.Messages` slice from each call.

**Expected output (assert on the captured request at call 2):**
- `len(messages) >= 3`: the initial messages PLUS the assistant's tool_call message
  PLUS at least one `role:"tool"` result message.
- The `role:"tool"` message contains `"name":"write_file"` (the tool name used in
  the result).
- The `role:"tool"` message contains a non-empty content string (the result of the
  `write_file` call — e.g. `"ok"` or `"wrote 5 bytes"` — exact text is
  implementation-defined, but it must be present and non-empty).

**Rationale:** This proves the loop maintains proper conversation state — the model
can observe the results of its tool calls in the next request.

---

### TC-103-05 — Loop returns an error when context is cancelled

- **Requirement:** REQ-103-04
- **Level:** L2 (unit test with stub client)
- **Test file:** `internal/executor/ollama_native_test.go`

**Setup:** Create a context that is already cancelled before calling `Run`. Stub
client is never expected to be called (the context check should fire before the first
network call, or immediately on the first `Chat` attempt).

**Expected output:**
- `Run` returns a non-nil error.
- `errors.Is(err, context.Canceled)` is true.

**Rationale:** The supervisor's wall-clock kill sends context cancellation. The loop
must not block past the deadline.

---

### TC-103-06 — F-003 supervisor isolation preserved after adding OllamaNative

- **Requirement:** REQ-103-05
- **Level:** L3 (fitness check)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after `OllamaNative` is added to
`internal/executor/`.

**Expected output:**
- The fitness check exits 0 with `PASS fitness-supervisor-isolation: …`.
- `internal/supervisor` does NOT import `internal/executor`,
  `internal/executor/ollamaclient`, or `internal/executor/ollamatoolset`.
- The import direction remains: `runtime` → `executor` → `supervisor` (never reverse).

---

## Verification plan

- **Highest level achievable in CI:** L2 — all tests use stub clients and a real
  temp worktree. No real Ollama process is required.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
  Expected: `PASS fitness-supervisor-isolation: …`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (deferred, operator-run):** Run `agent-builder run` with an Ollama-native
  registry entry pointing at a live Ollama instance running `qwen3:8b`. Observe the
  harness completing a trivial task (e.g. create `LIVE_OK.txt`) and producing a
  branch that passes the gate. Hardware-specific; not CI-automatable. Deferred
  following the same pattern as tasks 094/101.

## Out of scope

- The Ollama HTTP client itself (task 102).
- The tool set implementation (task 104, including path-confinement enforcement).
- Registry wiring — `HarnessOllamaNative` constant and `buildExecutorForEntry` case
  (task 105).
- Prompt engineering for specific model families.
- Authentication (Ollama on localhost requires none).
