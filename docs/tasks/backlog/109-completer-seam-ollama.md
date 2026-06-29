# Task 109: single-shot `Completer` seam + ollama-native completer

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Add a narrow, non-agentic `Completer` seam in `internal/executor` and an `ollama-native`
concrete that wraps the existing `ollamaclient.Chat` single-turn primitive (one user
message, no tools, no loop, no worktree/gate/branch), reached through a `CompleterForEntry`
dispatcher that fails closed for the three cloud harnesses. This is the production backing
for the `LLMPlanner`'s `Invoker` seam (task 100) and the prerequisite for wiring
`AGENT_BUILDER_PLANNER=llm` live (task 110).

## Context

ADR 053 (the authoritative design for this work) resolves how the orchestrate CLI's
`Invoker` reaches a model for a single-shot, non-agentic decomposition call. Decomposition is
a pure text query: no sandbox, no worktree, no tools, no gate, no branch. Forcing it through
`supervisor.Executor.Run` (the agentic loop) would spin a box and a gate to ask a model one
question (ADR 053 Option B, rejected).

The decision (ADR 053 §1/§2, Option A): introduce a `Completer` interface — the non-agentic
counterpart to `Executor.Run` — **in `internal/executor`**, beside the harness adapters
(`ollama_native.go`). A new top-level package was considered and rejected (Option C): it
buys no isolation the existing boundary already gives and fragments the `ollamaclient` reuse.
`internal/executor` is already off the planner's and orchestrator's direct-import graph (that
is exactly what F-010/F-014 assert), so adding the `Completer` there changes nothing about
those invariants — the planner still only ever sees the `Invoker` func type.

### The seam and concrete

```go
// Completer sends ONE prompt to the model behind a registry entry and returns the raw
// text. No worktree, no tools, no verification gate, no branch.
type Completer interface {
    Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)
}
```

The only completer built now is `ollama-native`, via the existing `Chatter` seam
(`Chat(ctx, ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error)`, already satisfied
by `*ollamaclient.Client`): one `ChatRequest` with a single `{Role: "user", Content: prompt}`
message, `Tools: nil`, `Stream: false`; return `resp.Message.Content`. It mirrors
`buildExecutorForEntry`'s ollama arm for construction (`entry.Endpoint` + `entry.ModelID`)
and threads the caller's `context.Context` into `Chat` so a hung model cannot wedge the
caller (ADR 053 "Timeout/cost" cross-cutting risk).

### The fail-closed dispatcher

```go
func CompleterForEntry(entry registry.RegistryEntry, ...) (Completer, error)
```

Dispatches on `entry.Harness`:
- `HarnessOllamaNative` → the ollama completer.
- `HarnessClaudeCLI` / `HarnessCodexCLI` / `HarnessGeminiCLI` → a typed,
  `errors.Is`-matchable sentinel error *"harness <X> single-shot completion not yet
  supported"*. **Fail-closed, never silently wrong** (ADR 053 §2): the planner gets an error
  and decomposition halts cleanly, rather than a cloud CLI being driven through its agentic
  `Run` or an empty string parsed as a zero-sub-goal plan. The cloud print-mode completers
  (`claude -p` / `gemini` / `codex` print mode) are deferred until there is a concrete need;
  this task only reserves their dispatcher slots with the explicit error arm.

### Why this keeps F-010 and F-014 green

The `Completer` interface and concretes live in `internal/executor`, which
`internal/orchestrator` and `internal/orchestrator/planner` do not import directly (the exact
invariants F-010/F-014 assert). The planner never names `Completer`; it sees only the
`Invoker` func type and `ExecutorResolver` interface it already defines. The closure that
adapts `CompleterForEntry` to `Invoker` is constructed in `internal/cli` (task 110), the
blessed wiring layer that already imports executor-adjacent code. No new edge is added to the
orchestrator's or planner's direct-import set.

## Requirements

| Req ID      | Description                                                                                                                    | Priority   |
|-------------|------------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-109-01  | A `Completer` interface (`Complete(ctx, entry, prompt) (string, error)`) is defined in `internal/executor`                     | must have  |
| REQ-109-02  | An `ollama-native` completer wraps `ollamaclient.Chat`; returns `resp.Message.Content` on a single round-trip                 | must have  |
| REQ-109-03  | The completer sends EXACTLY ONE `user` message with the prompt, `Tools == nil`, `Stream == false` (non-agentic — no loop)     | must have  |
| REQ-109-04  | `CompleterForEntry` returns the ollama completer for `HarnessOllamaNative`, validating endpoint/model                         | must have  |
| REQ-109-05  | `CompleterForEntry` fails closed with a typed `errors.Is`-matchable sentinel for the three cloud harnesses + unknown harness  | must have  |
| REQ-109-06  | A `Chatter` error / cancelled context is propagated by `Complete` (caller's ctx threaded into `Chat`); no zero-text on error  | must have  |

## Readiness gate

- [x] Task 100 merged (LLMPlanner + `Invoker` seam stable — this task supplies its production backing)
- [x] Task 102 merged (`ollamaclient` `/api/chat` client + `Chatter`-shaped `Chat`)
- [x] Task 105 merged (`HarnessOllamaNative` registry enum + `buildExecutorForEntry` ollama arm to mirror)
- [x] ADR 053 read in full and its §1/§2 decision adopted

## Acceptance criteria

- [ ] [REQ-109-01] TC-109-01: compile-time `var _ Completer = (*ollamaCompleter)(nil)`; `Complete` has the exact ADR 053 §1 signature; type lives in `internal/executor`
- [ ] [REQ-109-02] TC-109-02: stub `Chatter` returning canned `Content` → `Complete` returns that string verbatim, `nil` error, `Chat` called exactly once
- [ ] [REQ-109-03] TC-109-03: captured `ChatRequest` has `len(Messages)==1`, `Messages[0].Role=="user"`, `Content==prompt`, `Tools==nil`, `Stream==false`, `Model==entry.ModelID`
- [ ] [REQ-109-04] TC-109-04: `CompleterForEntry(ollamaEntry)` → non-nil ollama completer, `nil` error; blank endpoint/model → error
- [ ] [REQ-109-05] TC-109-05: each of claude-cli/codex-cli/gemini-cli + an unknown harness → nil completer, non-nil error, `errors.Is(err, ErrSingleShotUnsupported)`, message names the harness
- [ ] [REQ-109-06] TC-109-06: `Chatter` error → wrapped error + empty string; cancelled ctx → `errors.Is(err, context.Canceled)` (ctx threaded, not `context.Background()`)

## Verification plan

- **Highest level achievable: L6** — the ollama completer round-trips against the operator's
  local ollama (`qwen3:8b`) with no cloud credential and no sandbox, so a live-binary
  observation of prompt-in → text-out is reachable on the dev host. The cloud fail-closed arms
  are L2-asserted (TC-109-05). L2 (unit, stub `Chatter`) + L3 (fitness regression) are the
  CI-automatable ceiling.
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/executor/...
  make check
  ```
  Expected: `ok …/internal/executor`; `All checks passed.`
- **L3 fitness commands (regression):**
  ```
  make fitness-supervisor-isolation
  make fitness-orchestrator-no-executor
  make fitness-llm-planner-no-executor
  ```
  Expected: `PASS fitness-supervisor-isolation`; `PASS F-010 …`; `PASS F-014 …` (the new type
  is in `internal/executor`, off the orchestrator/planner direct-import graph — unchanged by
  construction).
- **L6 (operator-run, dev host):** point the ollama completer at `http://localhost:11434` /
  `qwen3:8b`, call `Complete` with a short decomposition prompt, observe non-empty text
  returned in one round-trip. Record model, prompt, and the first returned line in the verify
  commit.

## Modules touched

- `internal/executor` (new `Completer` interface, `ollama` completer concrete, and
  `CompleterForEntry` dispatcher — the single deliverable of this task, alongside the existing
  harness adapters; reuses `internal/executor/ollamaclient` already imported here).

(One module. The new files sit beside `ollama_native.go` in the same package; this does not
touch `internal/cli`, `internal/orchestrator`, or `internal/orchestrator/planner`. Within the
one-task / at-most-two-modules rule.)

## Out of scope

- Wiring the completer into `internal/cli` / the planner `Invoker` (task 110).
- Cloud print-mode completers — deferred by ADR 053; only the fail-closed dispatcher arm is
  built now.
- The decomposition prompt text (`buildPrompt` is the planner's; the completer is
  prompt-agnostic).
- Any change to `OllamaNative.Run` or the agentic loop.
- Streaming.

## Dependencies

- Task 100 (LLMPlanner + `Invoker` seam) — merged.
- Task 102 (ollamaclient) — merged.
- Task 105 (`HarnessOllamaNative` + ollama executor arm) — merged.
- **No task dependency beyond merged 100/102/105** — this is the prerequisite; task 110
  depends on it.
```
