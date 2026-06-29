# Task 108: Gate-failure feedback — Harness prompt injection (all 4 executors)

**Status:** backlog  
**Priority:** must-have  
**Project:** agent-builder  
**Depends on:** task 107 (`supervisor.Task.PriorFailure` field must be merged first)  
**Architect review:** not required (task 107 covers the ADR; this task is mechanical implementation)  
**Security review:** not required (no auth/secret surface; field is plain text, truncated)  

---

## Goal

Update all four prompt builders so that when `task.PriorFailure` is non-empty they include a
clearly-delimited failure section in the prompt (or initial user message for OllamaNative),
and OMIT that section entirely when `PriorFailure` is empty. This makes retries converge
instead of resample for every harness: Claude CLI, Codex CLI, Gemini CLI, and OllamaNative.

---

## Context

Task 107 adds `supervisor.Task.PriorFailure` and populates it in `RetryingLoop.RunOnce` after
a gate failure. The field arrives at each executor's `Run(task)` call but none of the four
current prompt builders reads it. This task closes the gap: all four builders conditionally
append a failure section when the field is non-empty.

Modules touched: `internal/executor` only (four files: `claude_cli.go`, `codex_cli.go`,
`gemini_cli.go`, `ollama_native.go`). Total module scope: 1.

---

## Requirements

### REQ-108-01 — `buildClaudePrompt` includes failure section when `PriorFailure` non-empty
When `task.PriorFailure != ""`, `buildClaudePrompt` appends a section that contains:
`"previous attempt"`, `"verification gate"`, and the verbatim content of `task.PriorFailure`.

### REQ-108-02 — `buildClaudePrompt` OMITS failure section when `PriorFailure` is empty
When `task.PriorFailure == ""`, the prompt does NOT contain `"previous attempt"` or
`"verification gate"`. Core prompt content (Task ID, Repo, Spec, Worktree, branch file path)
is unchanged.

### REQ-108-03 — `buildCodexPrompt` conditional failure section
Same as REQ-108-01/02 but for `buildCodexPrompt`.

### REQ-108-04 — `buildGeminiPrompt` conditional failure section
Same as REQ-108-01/02 but for `buildGeminiPrompt`.

### REQ-108-05 — `OllamaNative.Run` initial user message conditional failure section
When `task.PriorFailure != ""`, the first `ollamaclient.Message{Role: "user"}` in the
`messages` slice constructed in `OllamaNative.Run` includes the failure section. When
`task.PriorFailure == ""`, the initial user message is unchanged.

### REQ-108-06 — Cross-harness framing consistency
All four prompt builders include both `"previous attempt"` and `"verification gate"` when
`PriorFailure` is non-empty. The exact framing text in ADR 052 §4 is the template:

```
Your previous attempt failed the verification gate.

Failed step: <from PriorFailure>
Output:
<from PriorFailure>

Fix these issues before producing the branch.
```

The implementation may embed `task.PriorFailure` directly (since `FormatFailure` already
produced that block with step name + output) and wrap it with the leading sentence and the
closing instruction. The key constraint: every harness uses identical framing so the
cross-harness consistency test (TC-108-09) passes.

### REQ-108-07 — `make fitness-supervisor-isolation` passes
No new import enters `internal/supervisor` or any existing package via the harness changes.
The executor files already import `internal/supervisor` — reading the new field requires no
new import. `make fitness-supervisor-isolation` and `make check` exit 0.

### REQ-108-08 — `docs/spec/behaviors.md` updated in the same commit
`docs/spec/behaviors.md` notes that all four executors propagate gate-failure detail into
their prompts on retry attempts (a new behavior row or paragraph in the relevant section).

---

## Acceptance criteria

Self-verify by running:
```
go test -count=1 ./internal/executor/...
make fitness-supervisor-isolation
make check
```
All must pass. Additionally confirm:
- `go test -count=1 -run TestClaudePromptIncludesFailureSectionWhenPriorFailureSet ./internal/executor/...`
- `go test -count=1 -run TestClaudePromptOmitsFailureSectionWhenPriorFailureEmpty ./internal/executor/...`
- `go test -count=1 -run TestCodexPromptIncludesFailureSectionWhenPriorFailureSet ./internal/executor/...`
- `go test -count=1 -run TestGeminiPromptIncludesFailureSectionWhenPriorFailureSet ./internal/executor/...`
- `go test -count=1 -run TestOllamaNativeInitialMessageIncludesFailureSectionWhenPriorFailureSet ./internal/executor/...`
- `go test -count=1 -run TestCrossHarnessFailureSectionConsistency ./internal/executor/...`

All must pass with exact substring assertions (no smoke tests).

---

## Verification plan

- **Highest level achievable:** L5 (an integration test that drives
  `RetryingLoop.RunOnce` with a stub executor that records the full `Task` received on each
  attempt and a stub gate that fails on attempt 1, then asserts the second attempt's
  `task.PriorFailure` appears verbatim in the prompt returned by `buildClaudePrompt`).
  L6 (operator-observed run where attempt 1 fails a gate step and the run record shows
  attempt 2's prompt referencing the failure) is achievable on the dev host post-merge.

- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/... ./internal/loop/...
  ```
  Expected: `ok` for both packages. (Running both together ensures the loop-threading tests
  from task 107 still pass after task 108's changes.)

- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  make check
  ```
  Expected: `PASS fitness-supervisor-isolation` + `All checks passed.`

- **L5 integration (loop → harness round-trip):**
  ```
  go test -count=1 -run TestRetryLoopPropagatesPriorFailureToPrompt ./tests/loop/...
  ```
  Asserts: the `PriorFailure` string set by the loop appears in the prompt passed to the
  Claude executor on the second attempt (tests/loop integration harness).

- **L6 (operator-observed):** run `agent-builder run` with `AGENT_BUILDER_MAX_ATTEMPTS=2`
  and a worktree containing a deliberately broken file (e.g. `gofmt` violation). Observe
  that the run record's attempt 2 references the gate step `"go-fmt"` in the executor's
  initial prompt. Record the run record excerpt in the verify commit.

---

## Implementation notes

- The implementation in each prompt builder is a straightforward guard:
  ```go
  if task.PriorFailure != "" {
      // append the failure section block
  }
  ```
- For `buildClaudePrompt`, `buildCodexPrompt`, and `buildGeminiPrompt`, the section is
  appended to the returned `fmt.Sprintf` string.
- For `OllamaNative.Run`, the guard wraps the initial `Content` construction of
  `messages[0]`.
- The framing wrapper text MUST contain `"previous attempt"` and `"verification gate"` — do
  not change these exact phrases (they are asserted in TC-108-09).
- Task 107's `loop.FormatFailure` already produces the inner content (`"Failed step: ...\n
  Output:\n...\nFix these issues..."`) so the prompt builders only need to wrap it with
  the outer sentence: `"Your previous attempt failed the verification gate.\n\n" +
  task.PriorFailure`.

## Test spec

`docs/tasks/test-specs/108-gate-failure-harness-injection-test-spec.md`

## ADR

`docs/architecture/decisions/052-gate-failure-feedback-contract.md`
