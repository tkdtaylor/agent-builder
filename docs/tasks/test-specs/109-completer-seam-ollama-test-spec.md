# Test spec — Task 109: single-shot `Completer` seam + ollama-native completer

**Linked task:** `docs/tasks/backlog/109-completer-seam-ollama.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 053 §1/§2 (the `Completer` seam in `internal/executor`, the
`ollama-native` concrete over `ollamaclient.Chat`, the `CompleterForEntry` fail-closed
dispatcher). ADR 051 (ollamaclient leaf). ADR 043 (executor registry + harness drivers).

## Context

Task 100 shipped the `LLMPlanner` behind the `orchestrator.Planner` seam. It defines an
`Invoker func(ctx, registry.RegistryEntry, prompt string) (string, error)` seam for the
single model call that decomposes a goal, but there is no production `Invoker` — only test
stubs. ADR 053 resolves the missing piece: a narrow, **non-agentic** model-invocation
primitive that sends one prompt and returns raw text, with no worktree, no tools, no
verification gate, and no branch.

This task builds that primitive in `internal/executor` (beside the harness adapters, NOT a
new package — ADR 053 §1 rejected Options B and C). It is the **prerequisite** for task 110,
which closes over it at the CLI to produce the planner's `Invoker`. This task does NOT touch
`internal/cli` or `internal/orchestrator/planner`; it only adds the `Completer` interface,
the `ollama-native` concrete, and the `CompleterForEntry` dispatcher.

### The `Completer` seam (ADR 053 §1)

```go
// Completer sends ONE prompt to the model behind a registry entry and returns the raw
// text — the non-agentic counterpart to supervisor.Executor.Run. No worktree, no tools,
// no gate, no branch.
type Completer interface {
    Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)
}
```

### The ollama-native completer (ADR 053 §2)

The single completer built now wraps the existing single-turn `Chatter` seam
(`Chat(ctx, ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error)`, already satisfied
by `*ollamaclient.Client`). One round-trip: a `ChatRequest` with exactly ONE
`{Role: "user", Content: prompt}` message, `Tools: nil`, `Stream: false`; the completer
returns `resp.Message.Content`. The construction mirrors `buildExecutorForEntry`'s ollama arm
(`entry.Endpoint` + `entry.ModelID`). The completer threads the caller's `context.Context`
into `Chat` (which already honors cancellation per `ollamaclient` `TestChatContextCancelled`),
so a hung local model cannot wedge the caller.

### The fail-closed dispatcher (ADR 053 §2)

```go
// CompleterForEntry returns the single-shot Completer for the entry's harness, or a typed
// "harness X single-shot completion not yet supported" error (fail-closed).
func CompleterForEntry(entry registry.RegistryEntry, ...) (Completer, error)
```

Dispatch is by `entry.Harness`:
- `HarnessOllamaNative` → the ollama completer.
- `HarnessClaudeCLI` / `HarnessCodexCLI` / `HarnessGeminiCLI` → a typed, sentinel-backed
  error *"harness <X> single-shot completion not yet supported"*. **Fail-closed, never
  silently wrong** — the planner gets an error and decomposition halts, rather than a cloud
  CLI being driven through its agentic `Run` or an empty string being parsed as a
  zero-sub-goal plan. The error is matchable with `errors.Is` against an exported sentinel
  (`ErrSingleShotUnsupported` or equivalent) so callers can branch on it.

## Requirements coverage

| Req ID      | Description                                                                                                                    | Test cases               |
|-------------|------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-109-01  | A `Completer` interface with `Complete(ctx, entry, prompt) (string, error)` exists in `internal/executor`                     | TC-109-01                |
| REQ-109-02  | The ollama-native completer returns the model's text on a single-turn round-trip via a stub `Chatter`                         | TC-109-02                |
| REQ-109-03  | The ollama completer sends EXACTLY ONE `user` message with the prompt, `Tools == nil`, `Stream == false` (no agentic loop)    | TC-109-03                |
| REQ-109-04  | `CompleterForEntry` returns the ollama completer for `HarnessOllamaNative`                                                    | TC-109-04                |
| REQ-109-05  | `CompleterForEntry` returns a typed, `errors.Is`-matchable error for the three cloud harnesses (fail-closed, never silently wrong) | TC-109-05            |
| REQ-109-06  | A `Chatter` error / cancelled context is propagated by `Complete` (not swallowed); no zero-value text on the error path        | TC-109-06                |

---

## Test cases

### TC-109-01 — `Completer` interface exists and ollama completer satisfies it (L2)

- **Requirement:** REQ-109-01
- **Level:** L2 (compile-time interface assertion)

**Input:** In `internal/executor` (or its test file), assert the concrete ollama completer
type satisfies the seam:

```go
var _ Completer = (*ollamaCompleter)(nil) // or whatever the concrete is named
```

**Expected output (assertions):**
- The package compiles with the assertion present — `Complete` has the exact signature
  `Complete(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)`.
- The interface is named `Completer` and lives in `internal/executor` (same package as
  `ollama_native.go`), NOT a new top-level package.

---

### TC-109-02 — ollama completer returns the model's text via a stub `Chatter` (L2)

- **Requirement:** REQ-109-02
- **Level:** L2 (unit; stub `Chatter` returns canned text)

**Input:** Construct the ollama completer with a stub `Chatter` whose `Chat` returns
`ollamaclient.ChatResponse{Message: ollamaclient.Message{Role: "assistant", Content: "coding-agent: do the thing"}}`
and `nil` error. Call
`Complete(context.Background(), registry.RegistryEntry{Harness: registry.HarnessOllamaNative, Endpoint: "http://localhost:11434", ModelID: "qwen3:8b"}, "decompose this goal")`.

**Expected output (assertions):**
- Returns the string `"coding-agent: do the thing"` exactly (verbatim `resp.Message.Content`).
- Returns `nil` error.
- The stub `Chatter.Chat` was called **exactly once** (a single round-trip — no loop).

---

### TC-109-03 — the request carries exactly one user message, no tools, no stream (L2)

- **Requirement:** REQ-109-03
- **Level:** L2 (unit; capturing stub `Chatter`)

**Input:** A capturing stub `Chatter` that records the `ollamaclient.ChatRequest` it
receives, then returns a canned non-empty `Content`. Call `Complete(ctx, ollamaEntry,
"PROMPT-SENTINEL")`.

**Expected output (assertions on the captured `ChatRequest`):**
- `req.Model == "qwen3:8b"` (taken from `entry.ModelID`).
- `len(req.Messages) == 1`.
- `req.Messages[0].Role == "user"`.
- `req.Messages[0].Content == "PROMPT-SENTINEL"` (the prompt is passed verbatim, unwrapped).
- `req.Messages[0].ToolCalls` is empty (`len == 0`).
- `req.Tools == nil` (or `len(req.Tools) == 0`) — NO tool schemas are attached.
- `req.Stream == false`.

This is the assertion that the single-shot path is genuinely non-agentic — it diverges from
`OllamaNative.Run`, which attaches `toolDispatcher.ToolSchemas()` and loops.

---

### TC-109-04 — `CompleterForEntry` returns the ollama completer for `HarnessOllamaNative` (L2)

- **Requirement:** REQ-109-04
- **Level:** L2 (unit)

**Input:** Call
`CompleterForEntry(registry.RegistryEntry{Harness: registry.HarnessOllamaNative, Endpoint: "http://localhost:11434", ModelID: "qwen3:8b"})`.

**Expected output (assertions):**
- Returns a non-nil `Completer` and a `nil` error.
- The returned value's dynamic type is the ollama completer concrete (type assertion
  succeeds, e.g. `_, ok := c.(*ollamaCompleter); ok == true`).
- A blank `entry.Endpoint` or blank `entry.ModelID` on an ollama entry yields a non-nil
  error (mirrors `NewOllamaNative`'s blank-field guards) — the dispatcher does not return a
  completer that will fail opaquely at call time.

---

### TC-109-05 — `CompleterForEntry` fails closed for cloud harnesses (L2)

- **Requirement:** REQ-109-05
- **Level:** L2 (unit; table over the three cloud harnesses)

**Input:** For each of `registry.HarnessClaudeCLI`, `registry.HarnessCodexCLI`,
`registry.HarnessGeminiCLI`, call `CompleterForEntry(registry.RegistryEntry{Harness: h, ID: "cloud-x"})`.

**Expected output (assertions, each row):**
- Returns a `nil` `Completer` (no degenerate completer is handed back).
- Returns a non-nil error.
- `errors.Is(err, executor.ErrSingleShotUnsupported)` is `true` (the error is the exported
  sentinel, matchable by callers — not a bare `fmt.Errorf` string).
- The error message names the offending harness (e.g. contains `"claude-cli"` /
  `"codex-cli"` / `"gemini-cli"`) and the phrase `"not yet supported"` (or equivalent), so an
  operator reading the log knows which harness fell closed.
- An entry with an **unknown** harness driver (e.g. `registry.HarnessDriver("made-up")`)
  also returns a non-nil error (default arm) — never a nil-error nil-completer.

---

### TC-109-06 — `Chatter` error / cancelled context propagates (L2)

- **Requirement:** REQ-109-06
- **Level:** L2 (unit)

**Sub-case A — `Chatter` returns an error:**
- Stub `Chatter.Chat` returns `("", someErr)` with a sentinel `someErr`.
- `Complete(ctx, ollamaEntry, "p")` returns `("", err)` where the returned `err` is non-nil
  and wraps `someErr` (`errors.Is(err, someErr) == true`).
- The returned string is empty — no partial/zero text leaks on the error path.

**Sub-case B — cancelled context:**
- Construct a context, cancel it, pass it in. The stub `Chatter` returns
  `context.Canceled` (mirroring `ollamaclient`'s real cancellation behavior).
- `Complete(ctx, ollamaEntry, "p")` returns a non-nil error with
  `errors.Is(err, context.Canceled) == true`.
- This proves `Complete` threads the caller's context into `Chat` (ADR 053
  "Timeout/cost" cross-cutting risk) rather than calling with `context.Background()`.

---

## Verification plan

- **Highest level achievable: L6** — the ollama completer is exercisable against the
  operator's local ollama (`qwen3:8b`, the same model that drove task 108's L6 run). A
  validation-harness / live-binary run can observe a real prompt-in → text-out round-trip
  with no cloud credential and no sandbox. The dispatcher's fail-closed cloud arms are
  L2-asserted (TC-109-05).
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/executor/...
  make check
  ```
  Expected: `ok …/internal/executor` (and sub-packages); `All checks passed.`
- **L3 fitness commands (regression — the new type must not perturb the boundaries):**
  ```
  make fitness-supervisor-isolation
  make fitness-orchestrator-no-executor
  make fitness-llm-planner-no-executor
  ```
  Expected: `PASS fitness-supervisor-isolation`; `PASS F-010 …`; `PASS F-014 …`. The
  `Completer` lives in `internal/executor`, which is already off the orchestrator/planner
  direct-import graph, so F-010/F-014 are unaffected by construction (ADR 053 §"Why F-010 and
  F-014 stay green").
- **L6 (operator-run, dev host):** point the ollama completer at the operator's local ollama
  (`http://localhost:11434`, `qwen3:8b`); call `Complete` with a short decomposition prompt
  (e.g. via a tiny temporary `main` or a `//go:build manual` test); observe the model return
  non-empty text in one round-trip. Record the model, the prompt, and the first line of the
  returned text in the verify commit.

## Out of scope

- Wiring the completer into `internal/cli` / the planner `Invoker` (task 110).
- Cloud print-mode completers (`claude -p`, `gemini`, `codex` print mode) — ADR 053 defers
  these; this task only reserves their dispatcher slots with the fail-closed error arm.
- Prompt construction / decomposition prompt text (the planner owns `buildPrompt`; the
  completer is prompt-agnostic and passes the string through verbatim).
- Any change to `OllamaNative.Run` or the agentic loop.
- Streaming responses (`Stream: false` only).
```
