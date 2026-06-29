# ADR 052 — Gate-failure feedback contract for retry attempts

**Status:** Proposed  
**Date:** 2026-06-28  
**Author:** task-planner  
**Architect review required:** yes — this changes the executor/Task contract and the retry-loop threading  

---

## Context

The retry loop in `internal/loop/retry_policy.go` (`RetryingLoop.RunOnce`) retries a task
up to `MaxAttempts` times. On each failed attempt the `Outcome` value already carries the
full `gate.Verdict` (field `Outcome.Verdict`), including the name and output of every step
that ran. The escalation hook receives this via `EscalationRequest.Outcome`, but the next
executor's `Run(Task)` call receives only the original `supervisor.Task` — no indication of
what the previous attempt's verification gate failed on.

For capable (cloud) executors the cost of a blind retry is one extra API round-trip. For
weaker or local executors (`qwen3:8b` and similar, validated in tasks 102–106 and 094), a
blind retry resamples at random: the model never learns whether it produced unformatted code,
a build error, a lint warning, or a test failure. The retry loop converges only by luck
rather than by correction.

The `internal/runtime/run.go` module already contains two helpers that format gate verdicts
for human-readable log output:

- `summarizeVerdict(gate.Verdict) string` — formats passing/failing step names
- `writeFailureEvidence(streams, outcome)` — writes the failing step name and its output to
  the run-record stderr stream

These must be reused (or the formatting logic extracted) rather than duplicated in the loop.

---

## Decision

### 1. Where the feedback field lives

Add a `PriorFailure string` field to `supervisor.Task`. This is the only type threaded from
the retry loop to every `Executor.Run` call. A plain string avoids importing structured gate
types into executor implementations.

```go
// in internal/supervisor/supervisor.go

type Task struct {
    ID           string
    Repo         string
    Spec         string
    PriorFailure string // non-empty only on retry attempt N≥2; formatted gate-failure detail
}
```

A plain string field on `supervisor.Task` does NOT violate F-003 (supervisor must not import
executor/LLM/web packages) — it adds only a primitive field to a value type the supervisor
already owns. Confirm with `make fitness-supervisor-isolation` after the change.

### 2. Who formats the failure detail

Extract a package-level helper `FormatFailure(outcome loop.Outcome) string` in
`internal/loop` (same package as `RetryingLoop`). This helper reads `outcome.Failure.Reason`
and `outcome.Verdict.Results` and produces the formatted string that will be injected as
`task.PriorFailure`. It reuses the same formatting logic as `runtime.summarizeVerdict` and
`runtime.writeFailureEvidence` without importing `internal/runtime`.

The existing `runtime.summarizeVerdict` and `runtime.writeFailureEvidence` remain in
`internal/runtime/run.go` and continue to serve the run-record / audit-stream path. There is
no circular dependency: `internal/loop` imports `internal/gate` and `internal/supervisor`
(already the case); `internal/runtime` imports `internal/loop` (already the case).

### 3. Truncation cap

Gate step output can be large (e.g. test output with many failures, lint output with many
findings). The `PriorFailure` string is injected into the next attempt's prompt, so prompt
size must be bounded. Cap the `Output` slice per step at **2 000 characters** before
formatting. Only the first failing step's output is included (matching the existing
`writeFailureEvidence` behavior: the first failing step is the most actionable signal).

The constant `MaxFailureOutputBytes = 2000` is exported from `internal/loop` so callers can
reference it in tests and documentation.

### 4. What is included in `PriorFailure`

When the previous attempt produced an `OutcomeFail` with `FailureReason == FailureGate`:

```
Your previous attempt failed the verification gate.

Failed step: <step-name>
Output:
<step-output-truncated-to-2000-chars>

Fix these issues before producing the branch.
```

When the previous attempt produced an `OutcomeFail` with `FailureReason == FailureExecutorError`:

```
Your previous attempt failed: the executor encountered an error.
Fix any issues and retry.
```

When the previous attempt produced an `OutcomeFail` with `FailureReason == FailureExecutorIncomplete`:

```
Your previous attempt did not complete: the executor did not produce a branch.
Ensure your implementation writes the produced branch name to the designated output file.
```

The first attempt always has `PriorFailure == ""`. Prompt builders MUST guard on empty string
and include no failure section when it is empty.

### 5. Where the field is populated

In `RetryingLoop.RunOnce`, after an `OutcomeFail` on attempt `N` that will be followed by
attempt `N+1` (i.e. `attempt < l.policy.MaxAttempts`), set:

```go
task.PriorFailure = loop.FormatFailure(outcome)
```

before constructing the `singleTaskSource{task: task}` for the next `cycle` (i.e. `New(...)`
call). The updated `task` is used for ALL subsequent attempts within the same `RunOnce` call.

Because `RunOnce` reuses one local `task` variable, the mutated `PriorFailure` also appears in
the subsequently-constructed `EscalationRequest.Task` and in the terminal `RetryOutcome.Task`
returned on escalation. This is a benign side effect — the escalation hook selects only an
executor (it does not read `PriorFailure`), and no `RetryOutcome.Task` consumer reads the
field (publish/audit paths use `Task.ID`/`Task.Repo` only). Tests must not assert
`RetryOutcome.Task.PriorFailure == ""` on the escalated path.

### 6. Which harnesses consume the field

All four prompt builders must include the feedback section when `task.PriorFailure != ""`:

- `internal/executor/claude_cli.go` → `buildClaudePrompt`
- `internal/executor/codex_cli.go` → `buildCodexPrompt`  
- `internal/executor/gemini_cli.go` → `buildGeminiPrompt`
- `internal/executor/ollama_native.go` → the initial `user` message content in `Run`

This is harness-agnostic: all executors receive the same field on the same `Task` value. No
harness-specific branching is needed in the loop; all branching is local to each prompt
builder (guard on empty string).

### 7. Fitness and import-graph safety

- `supervisor.Task` gains a plain string field: no new import into `internal/supervisor`.
- `internal/loop` already imports `internal/gate` and `internal/supervisor`: adding
  `FormatFailure` there introduces no new dependency.
- `internal/executor` already imports `internal/supervisor`: reading `task.PriorFailure`
  introduces no new dependency.
- `make fitness-supervisor-isolation` must pass before the 107 feat commit is merged.

---

## Alternatives considered

**A. Pass failure detail via a separate context/metadata map attached to Task.**  
Rejected — over-engineered. A plain string is sufficient for prompt injection; a structured
map adds type complexity with no benefit at the prompt boundary.

**B. Return failure detail from `Executor.Run` and let the loop repass it as a separate arg.**  
Rejected — would require changing the `supervisor.Executor` interface signature, touching all
existing executor tests and mocks. Adding a field to `supervisor.Task` is additive and
backward-compatible with all existing consumers (they simply ignore the new field).

**C. Keep formatting in `internal/runtime` and import it from `internal/loop`.**  
Rejected — `internal/loop` must not import `internal/runtime` (that would create a cycle:
`runtime` already imports `loop`).

**D. Put the formatter in `internal/gate`.**  
Rejected — `internal/gate` is a leaf package and should remain so. Formatting loop-level
failure signals is a loop responsibility.

---

## Consequences

- The `supervisor.Task` type gains one field (`PriorFailure string`). All existing
  construction sites that use struct literal syntax with named fields compile unchanged; sites
  using positional initialization would break — there are none in this codebase.
- All four prompt builders change. Task 108 updates them together to keep the failure section
  consistent across harnesses and to ensure the "omit when empty" guard is tested per harness.
- `docs/spec/data-model.md` must be updated (Task field) in the same commit as task 107's
  code change.
- `docs/spec/behaviors.md` must note that the retry loop now propagates gate-failure detail
  to the next executor attempt (a new behavior entry).
- L6 evidence: an operator run where attempt 1 fails the gate (e.g. on a deliberately
  gofmt-dirty or build-broken first output from a local model) and attempt 2 fixes the error
  using the fed-back failure detail. The existing 094/102–106 validation harness is the
  recommended vehicle for this evidence.
