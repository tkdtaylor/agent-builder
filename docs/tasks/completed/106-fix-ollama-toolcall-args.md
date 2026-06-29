# Task 106: Fix Ollama native tool-call arguments decoding (object, not string)

**Status:** completed (🟡 — merged; L6 native-harness round-trip PASSED)
**Priority:** must-have (blocks the native-harness L6 round-trip / task 094 closure)
**Created:** 2026-06-28
**Paired spec:** `docs/tasks/test-specs/106-fix-ollama-toolcall-args-test-spec.md`
**Review flags:** spec-verifier (executor decode path)

## Goal

Fix a bug found during the L6 validation of the native Ollama harness (tasks 102–105):
`ollamaclient.ToolCallFunction.Arguments` is typed `string`, but Ollama's native
`/api/chat` returns `function.arguments` as a JSON **object**, so the executor errors
on the first chat call (`cannot unmarshal object ... of type string`) and every run
escalates. The task-102 httptest stubs used string-form arguments, which masked the
mismatch in L2.

## Requirements

### REQ-106-01 — Client decodes object-form arguments
`ollamaclient.ToolCallFunction.Arguments` MUST be `json.RawMessage` (capturing the
raw object bytes), so decoding a real Ollama `/api/chat` tool-call response succeeds.

### REQ-106-02 — Loop forwards arguments as a JSON string
`executor.OllamaNative` MUST pass `string(tc.Function.Arguments)` to
`ToolDispatcher.Dispatch(toolName, argsJSON)`. The toolset unmarshals that JSON object
string into its typed args struct unchanged.

### REQ-106-03 — Tests use the real wire format
The task-102 and task-103 test stubs MUST emit `arguments` as a JSON object (the real
Ollama shape). All existing assertions remain real (no smoke-test downgrade).

### REQ-106-04 — Loop commits the worktree onto the produced branch
On terminal completion, `OllamaNative.Run` MUST create/reset the produced branch at the
current worktree state and commit all changes onto it (deterministic commit identity),
leaving the worktree on that branch — so the branch handed to the publisher actually
contains the model's work (`finish_branch` only records the name). Found by the first
L6 run: the gate had been verifying uncommitted worktree files and the pushed branch
was empty.

## Acceptance criteria

1. `ToolCallFunction.Arguments` is `json.RawMessage`.
2. The loop dispatches with the raw object JSON string.
3. `go test -count=1 ./internal/executor/...` passes with TC-106-01..03.
4. `make check` passes.
5. Spec docs that state the `Arguments` type (`docs/spec/interfaces.md`, the task-102
   spec note) updated to reflect `json.RawMessage`.

## Verification plan

| Level | Gate | Command | Expected |
|-------|------|---------|----------|
| L2 | Unit tests | `go test -count=1 ./internal/executor/...` | `ok` |
| L3 | Fitness + lint | `make check` | `All checks passed.` |
| L5/L6 | Native-harness live round-trip (operator-run) | `ollama-native` + `qwen3:8b` orchestrator run | first chat decodes; loop executes tools; branch produced |

**Highest CI-achievable:** L2/L3. L5/L6 is the task-094 closure path (operator-run).

## Modules touched

- `internal/executor/ollamaclient/client.go` — `Arguments` type → `json.RawMessage`.
- `internal/executor/ollama_native.go` — dispatch with `string(arguments)`.
- `internal/executor/ollamaclient/client_test.go`, `internal/executor/ollama_native_test.go` — object-form stubs + TC-106-01/02.
- `docs/spec/interfaces.md` — `Arguments` type note.

## Dependencies

- Tasks 102 (client), 103 (loop), 105 (wiring) — MERGED. This corrects 102/103.

## Out of scope

- Loop termination/iteration cap (103); tool-set security (104); OpenAI-compat
  string-form arguments (not used by the native path).
