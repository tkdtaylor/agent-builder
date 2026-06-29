# Test spec — Task 108: Gate-failure feedback: harness prompt injection (all 4 executors)

**Linked task:** `docs/tasks/backlog/108-gate-failure-harness-injection.md`  
**Written:** 2026-06-28  
**Status:** ready  
**Governing ADR:** ADR 052 (gate-failure feedback contract)  
**Depends on:** Task 107 (`supervisor.Task.PriorFailure` field + `loop.FormatFailure`)  

---

## Context

Task 107 adds `supervisor.Task.PriorFailure` and wires the retry loop to populate it.
This task makes all four prompt builders consume the field: each builder must include a
clearly-delimited failure section when `PriorFailure` is non-empty, and must OMIT the
section entirely when it is empty (so first-attempt prompts are unchanged). The test for
each harness covers both the "present" and "absent" sub-cases with exact substring checks.

Harnesses in scope:
1. `internal/executor/claude_cli.go` → `buildClaudePrompt`
2. `internal/executor/codex_cli.go` → `buildCodexPrompt`
3. `internal/executor/gemini_cli.go` → `buildGeminiPrompt`
4. `internal/executor/ollama_native.go` → initial `user` message content in `Run`

---

## Requirements coverage

| Req ID      | Description                                                                                                                           | Test cases           |
|-------------|---------------------------------------------------------------------------------------------------------------------------------------|----------------------|
| REQ-108-01  | `buildClaudePrompt` includes the gate-failure section when `task.PriorFailure != ""`; the section starts with the delimiter text specified in ADR 052 §4 | TC-108-01 |
| REQ-108-02  | `buildClaudePrompt` OMITS any failure section when `task.PriorFailure == ""` (first-attempt prompt is unchanged)                     | TC-108-02            |
| REQ-108-03  | `buildCodexPrompt` includes the failure section when `task.PriorFailure != ""`; OMITS it when empty                                  | TC-108-03, TC-108-04 |
| REQ-108-04  | `buildGeminiPrompt` includes the failure section when `task.PriorFailure != ""`; OMITS it when empty                                  | TC-108-05, TC-108-06 |
| REQ-108-05  | `OllamaNative.Run` includes the failure section in the initial `user` message when `task.PriorFailure != ""`; OMITS it when empty    | TC-108-07, TC-108-08 |
| REQ-108-06  | The failure section delimiter is consistent across all four harnesses: all include the text `"previous attempt"` and `"verification gate"` when `PriorFailure` is non-empty (harness-agnostic framing per ADR 052 §4) | TC-108-09 |
| REQ-108-07  | `make fitness-supervisor-isolation` passes after all four harness changes — no new forbidden import enters the supervisor package     | TC-108-10            |
| REQ-108-08  | `docs/spec/behaviors.md` notes that all four executors propagate gate-failure detail into their prompts on retry attempts             | TC-108-11            |

---

## Test cases

### TC-108-01 — `buildClaudePrompt` includes failure section when `PriorFailure` non-empty (L2)

- **Requirement:** REQ-108-01
- **Level:** L2 (unit test in `internal/executor`)

**Input:**
```go
task := supervisor.Task{
    ID:           "001",
    Repo:         "exec-sandbox",
    Spec:         "/tasks/001.md",
    PriorFailure: "Failed step: go-fmt\nOutput:\nbad_file.go\nFix these issues before producing the branch.",
}
prompt := buildClaudePrompt(task, "/worktree", "/tmp/branch.txt")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `true`.
- `strings.Contains(prompt, "verification gate")` is `true`.
- `strings.Contains(prompt, "go-fmt")` is `true` — the step name from `PriorFailure` appears.
- `strings.Contains(prompt, "bad_file.go")` is `true` — the step output from `PriorFailure` appears.
- `strings.Contains(prompt, "Fix these issues")` is `true`.

---

### TC-108-02 — `buildClaudePrompt` OMITS failure section when `PriorFailure` is empty (L2)

- **Requirement:** REQ-108-02
- **Level:** L2 (unit test in `internal/executor`)

**Input:**
```go
task := supervisor.Task{ID: "001", Repo: "exec-sandbox", Spec: "/tasks/001.md"}
// PriorFailure is zero-value ""
prompt := buildClaudePrompt(task, "/worktree", "/tmp/branch.txt")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `false`.
- `strings.Contains(prompt, "verification gate")` is `false`.
- `strings.Contains(prompt, "Fix these issues")` is `false`.
- The prompt still contains `"Task ID: 001"` and `"/worktree"` (core content unchanged).

---

### TC-108-03 — `buildCodexPrompt` includes failure section when `PriorFailure` non-empty (L2)

- **Requirement:** REQ-108-03
- **Level:** L2 (unit test in `internal/executor`)

**Input:**
```go
task := supervisor.Task{
    ID:           "001",
    Repo:         "exec-sandbox",
    Spec:         "/tasks/001.md",
    PriorFailure: "Failed step: go-test\nOutput:\nFAIL TestBar\nFix these issues before producing the branch.",
}
prompt := buildCodexPrompt(task, "/worktree")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `true`.
- `strings.Contains(prompt, "verification gate")` is `true`.
- `strings.Contains(prompt, "go-test")` is `true`.
- `strings.Contains(prompt, "FAIL TestBar")` is `true`.

---

### TC-108-04 — `buildCodexPrompt` OMITS failure section when `PriorFailure` is empty (L2)

- **Requirement:** REQ-108-03
- **Level:** L2

**Input:**
```go
task := supervisor.Task{ID: "001", Repo: "exec-sandbox", Spec: "/tasks/001.md"}
prompt := buildCodexPrompt(task, "/worktree")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `false`.
- `strings.Contains(prompt, "verification gate")` is `false`.
- Prompt contains `"Task ID: 001"` (core content present).

---

### TC-108-05 — `buildGeminiPrompt` includes failure section when `PriorFailure` non-empty (L2)

- **Requirement:** REQ-108-04
- **Level:** L2 (unit test in `internal/executor`)

**Input:**
```go
task := supervisor.Task{
    ID:           "001",
    Repo:         "exec-sandbox",
    Spec:         "/tasks/001.md",
    PriorFailure: "Failed step: golangci-lint\nOutput:\nerr: unused variable\nFix these issues before producing the branch.",
}
prompt := buildGeminiPrompt(task, "/worktree")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `true`.
- `strings.Contains(prompt, "verification gate")` is `true`.
- `strings.Contains(prompt, "golangci-lint")` is `true`.
- `strings.Contains(prompt, "unused variable")` is `true`.

---

### TC-108-06 — `buildGeminiPrompt` OMITS failure section when `PriorFailure` is empty (L2)

- **Requirement:** REQ-108-04
- **Level:** L2

**Input:**
```go
task := supervisor.Task{ID: "001", Repo: "exec-sandbox", Spec: "/tasks/001.md"}
prompt := buildGeminiPrompt(task, "/worktree")
```

**Expected output (assertions):**
- `strings.Contains(prompt, "previous attempt")` is `false`.
- `strings.Contains(prompt, "verification gate")` is `false`.
- Prompt contains `"Task ID: 001"`.

---

### TC-108-07 — `OllamaNative.Run` initial user message includes failure section when `PriorFailure` non-empty (L2)

- **Requirement:** REQ-108-05
- **Level:** L2 (unit test in `internal/executor` using a stub `Chatter`)

**Setup:** Use a `capturingChatter` stub that records the first `ChatRequest` it receives
and returns a terminal (no-tool-calls) response so `Run` exits after one iteration.

**Input:**
```go
task := supervisor.Task{
    ID:           "001",
    Repo:         "exec-sandbox",
    Spec:         "/tasks/001.md",
    PriorFailure: "Failed step: go-build\nOutput:\n./main.go:5: undefined: Foo\nFix these issues before producing the branch.",
}
// Call OllamaNative.Run(task) with a capturingChatter that records messages[0]
```

**Expected output (assertions):**
- `capturingChatter.firstRequest.Messages[0].Role == "user"`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "previous attempt")` is `true`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "verification gate")` is `true`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "go-build")` is `true`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "undefined: Foo")` is `true`.

---

### TC-108-08 — `OllamaNative.Run` initial user message OMITS failure section when `PriorFailure` is empty (L2)

- **Requirement:** REQ-108-05
- **Level:** L2 (unit test using a `capturingChatter` stub)

**Input:**
```go
task := supervisor.Task{ID: "001", Repo: "exec-sandbox", Spec: "/tasks/001.md"}
// PriorFailure == ""
```

**Expected output (assertions):**
- `capturingChatter.firstRequest.Messages[0].Role == "user"`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "previous attempt")` is `false`.
- `strings.Contains(capturingChatter.firstRequest.Messages[0].Content, "verification gate")` is `false`.
- The message still contains `"Task ID: 001"`.

---

### TC-108-09 — Cross-harness consistency: all four include `"previous attempt"` and `"verification gate"` when `PriorFailure` non-empty (L2)

- **Requirement:** REQ-108-06
- **Level:** L2 (table-driven test over all four prompt builders)

**Setup:** Define a shared `priorFailure` string:
```
"Failed step: go-test\nOutput:\nFAIL TestX\nFix these issues before producing the branch."
```

**Input:** Call each of the four builders with this `PriorFailure` value and collect the
resulting prompt/message strings:
- `buildClaudePrompt(task, worktree, branchPath)` → `claudeOut`
- `buildCodexPrompt(task, worktree)` → `codexOut`
- `buildGeminiPrompt(task, worktree)` → `geminiOut`
- `OllamaNative.Run` with `capturingChatter` → `ollamaOut`

**Expected output (assertions) — for each of the four outputs:**
- `strings.Contains(out, "previous attempt")` is `true`.
- `strings.Contains(out, "verification gate")` is `true`.
- `strings.Contains(out, "go-test")` is `true`.
- `strings.Contains(out, "FAIL TestX")` is `true`.

All four assertions must hold for all four outputs. The test fails if any harness omits the
section or uses different framing text that drops `"previous attempt"` or `"verification gate"`.

---

### TC-108-10 — `make fitness-supervisor-isolation` passes after all four harness changes (L3)

- **Requirement:** REQ-108-07
- **Level:** L3 (fitness)

**Input:** After implementing task 108's code changes, run:
```
make fitness-supervisor-isolation
make check
```

**Expected output (assertions):**
- `make fitness-supervisor-isolation` exits 0 with `PASS fitness-supervisor-isolation`.
- `make check` exits 0 with `All checks passed.`
- No new linter warnings are introduced versus the baseline (`go vet` + `golangci-lint`).

---

### TC-108-11 — `docs/spec/behaviors.md` updated to describe all-harness prompt injection (L2 documentary)

- **Requirement:** REQ-108-08
- **Level:** L2 (file content assertion in a Go test)

**Input:** Read `docs/spec/behaviors.md` from the repo root using `os.ReadFile`.

**Expected output (assertions):**
- The file contains `"PriorFailure"` OR `"gate-failure feedback"` OR `"prior failure"` (same
  guard as TC-107-08 — must still hold after task 108 updates the file further).
- The file contains text indicating the behavior applies to all harnesses / executors
  (e.g. `"Claude"` AND `"Codex"` AND `"Gemini"` AND `"Ollama"`, or a collective term like
  `"all executors"` or `"every harness"`).

---

## Verification plan

- **Highest level achievable:** L5 (an integration test that runs `RetryingLoop.RunOnce`
  with a stub executor that records the second-attempt prompt and asserts it contains the
  failure section; full loop → harness round-trip without a live LLM). L6 (operator-observed:
  a native-harness or local-model run where attempt 1 fails the gate and attempt 2's initial
  prompt contains the failure detail — observable via run record or debug logging) is
  achievable on the dev host post-merge.

- **L2 harness commands:**
  ```
  go test -count=1 ./internal/executor/... ./internal/loop/...
  ```
  Expected: `ok` for both packages.

- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  make check
  ```
  Expected: `PASS fitness-supervisor-isolation` + `All checks passed.`

- **L5 integration (loop → harness round-trip):**
  ```
  go test -count=1 -run TestRetryLoopPropagatesPriorFailureToClaudePrompt ./tests/loop/...
  ```
  (or the package that hosts integration tests for the loop + executor combination)
  Asserts: the string stored in `capturingExecutor.receivedTasks[1].PriorFailure` appears
  verbatim in the prompt that `buildClaudePrompt` produces for that task.

- **L6 (operator-observed):** run `agent-builder run` with `AGENT_BUILDER_MAX_ATTEMPTS=2`
  against a worktree where a trivially broken file causes the gate to fail on attempt 1
  (e.g. a `gofmt` violation intentionally introduced). Observe the run record: attempt 2's
  executor invocation should reference the gate step that failed. Record in the verify commit.

## Out of scope

- `supervisor.Task.PriorFailure` field and `loop.FormatFailure` implementation (task 107).
- Any change to escalation policy or `MaxAttempts` configuration.
- Changes to `internal/runtime/run.go` formatting helpers — they are not modified.
- The `StructuredPlanner` or `LLMPlanner` (tasks 099/100) — the feedback flows through the
  executor seam only.
