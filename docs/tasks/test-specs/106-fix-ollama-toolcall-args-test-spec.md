# Test spec — Task 106: Fix Ollama native tool-call arguments decoding (object, not string)

**Linked task:** `docs/tasks/backlog/106-fix-ollama-toolcall-args.md`
**Written:** 2026-06-28
**Status:** ready

## Context

The L6 validation run of the native Ollama harness (tasks 102–105) against a live
`qwen3:8b` failed on the **first** chat call:

```
executor error: chat call 1: decode response: json: cannot unmarshal object
into Go struct field ToolCallFunction.message.tool_calls.function.arguments of type string
```

Root cause: `ollamaclient.ToolCallFunction.Arguments` is typed `string` (the
OpenAI/`chat/completions` convention, where `arguments` is a JSON-encoded string).
Ollama's **native** `/api/chat` endpoint — the one the harness uses — returns
`function.arguments` as a JSON **object**:

```json
{"function": {"name": "write_file", "arguments": {"path": "a.go", "content": "..."}}}
```

The task-102 unit tests used httptest stubs that shaped `arguments` as a string, so
L2 passed while the live API fails. This is a stub-vs-reality mismatch: the fix must
make the client decode Ollama's real wire format, and the tests must use that format.

## Fix

- `ollamaclient.ToolCallFunction.Arguments`: change type from `string` to
  `json.RawMessage` so it captures the raw object bytes verbatim (decode never fails
  on an object).
- `executor.OllamaNative` loop: when dispatching, pass `string(tc.Function.Arguments)`
  (the raw object JSON) to `ToolDispatcher.Dispatch(toolName, argsJSON)`. The toolset
  already `json.Unmarshal`s `argsJSON` into its typed args struct, so an object string
  decodes correctly.
- Update the task-102 and task-103 test stubs to emit `arguments` as a JSON object
  (the real Ollama shape).

## Requirements coverage

| Req ID     | Test cases            | Covered? |
|------------|-----------------------|----------|
| REQ-106-01 | TC-106-01             | yes      |
| REQ-106-02 | TC-106-02             | yes      |
| REQ-106-03 | TC-106-03             | yes      |
| REQ-106-04 | TC-106-04             | yes      |

## Second L6 finding — produced branch must contain the committed work

The first L6 run with the decode fix revealed a second integration gap: the native
harness recorded a branch *name* (via `finish_branch` → `.agent-branch`) and returned
`Result{Branch, OK:true}`, but **nothing committed the model's worktree edits onto that
branch** — `finishBranch` only writes the name, the loop only reads it, and the
publisher only `git push`es. The produced branch was an empty label and the gate
verified *uncommitted* worktree files. Fix: the agentic loop (`OllamaNative`), not the
model, must capture the worktree onto the produced branch — `git checkout -B <branch>;
git add -A; git commit` — so the branch handed to the publisher contains the work.

### REQ-106-04 — Loop commits the worktree onto the produced branch
On terminal completion, `OllamaNative.Run` MUST create/reset the produced branch at the
current worktree state and commit ALL changes onto it (with a deterministic commit
identity), leaving the worktree on that branch. The published branch therefore contains
the model's edits.

## Test cases

### TC-106-01 — Client decodes object-form tool-call arguments without error

- **Requirement:** REQ-106-01
- **Level:** L2
- **Test file:** `internal/executor/ollamaclient/client_test.go`

**Input:** A stub `/api/chat` server returns a response whose
`message.tool_calls[0].function.arguments` is a JSON **object**:
`{"path":"product.go","content":"package mathx"}`.

**Expected output:**
- `Chat(...)` returns a nil error (no "cannot unmarshal object ... of type string").
- `resp.Message.ToolCalls[0].Function.Name == "write_file"`.
- `string(resp.Message.ToolCalls[0].Function.Arguments)` is valid JSON that
  unmarshals to a map with `path == "product.go"` and `content == "package mathx"`
  (assert the decoded values, not just non-empty).

### TC-106-02 — Loop forwards object arguments to Dispatch as a decodable JSON string

- **Requirement:** REQ-106-02
- **Level:** L2
- **Test file:** `internal/executor/ollama_native_test.go`

**Input:** A stub `Chatter` returns one tool call `write_file` with object arguments
`{"path":"out.txt","content":"DONE"}` on the first turn, then a tool call
`finish_branch` with `{"branch":"task/106-test"}` on the second. Use the real
`ollamatoolset.ToolSet` over a temp worktree.

**Expected output:**
- The loop calls `Dispatch("write_file", argsJSON)` where `argsJSON` is the object
  JSON string, and the file `out.txt` is created in the worktree with exact content
  `"DONE"`.
- The run returns `Result{Branch: "task/106-test", OK: true}`.

### TC-106-03 — Regression: existing 102/103 tests pass with object-form stubs

- **Requirement:** REQ-106-03
- **Level:** L2/L3
- **Test file:** existing `client_test.go` / `ollama_native_test.go`

**Expected output:** After the stubs are migrated to object-form `arguments`, the full
suites still pass: `go test -count=1 ./internal/executor/...` → `ok`; `make check` →
`All checks passed.` No assertion is downgraded to a smoke test.

### TC-106-04 — Loop commits worktree edits onto the produced branch

- **Requirement:** REQ-106-04
- **Level:** L2
- **Test file:** `internal/executor/ollama_native_test.go`

**Input:** A real git repo in a temp worktree (one seed commit). A stub `Chatter`
drives the loop to write a file then terminate with the branch recorded as
`task/103-test`.

**Expected output:**
- After `Run`, the branch `task/103-test` exists and `git show task/103-test:<file>`
  returns the exact content the tool wrote (the work is committed on the branch).
- The worktree is left on `task/103-test` (`git rev-parse --abbrev-ref HEAD`).

## Verification plan

- **L2:** `go test -count=1 ./internal/executor/...` → `ok`
- **L3:** `make check` → `All checks passed.`
- **L5/L6 (operator-run):** re-run the native-harness orchestrator round-trip
  (`ollama-native` entry + `qwen3:8b`, no proxy) against a Go target; success =
  the first chat call decodes, the loop executes tools, and the run reaches a produced
  branch (and ideally a green gate). This is the task-094 L6 closure path.

## Out of scope

- Changes to the agentic loop's termination / iteration cap (task 103).
- Changes to the tool set's confinement/security (task 104).
- OpenAI-compat string-form `arguments` (not used by the native harness path).
