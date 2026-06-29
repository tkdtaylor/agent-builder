# Task 107: Gate-failure feedback — Task contract + loop threading

**Status:** backlog  
**Priority:** must-have  
**Project:** agent-builder  
**Depends on:** tasks 102–106 (OllamaNative harness, verified); task 013 (RetryingLoop)  
**Blocks:** task 108 (harness prompt injection requires the `PriorFailure` field)  
**Architect review:** required (ADR 052 — gate-failure feedback contract)  
**Security review:** not required (no auth/secret surface changed)  

---

## Goal

Add `supervisor.Task.PriorFailure string` and the `loop.FormatFailure` helper so the
`RetryingLoop` can populate the next attempt's task with formatted gate-failure detail from
the previous attempt. The executor `Run(Task)` interface is unchanged; the field is additive.
This is the data-contract half of the feature; harness consumption is task 108.

---

## Context

The retry loop in `internal/loop/retry_policy.go` (`RetryingLoop.RunOnce`) retries a failed
task up to `MaxAttempts` times but passes an identical `supervisor.Task` to each attempt. For
uneven/local executors (tasks 102–106, 094) this means the model cannot correct an error it
cannot see. ADR 052 specifies the fix: add `PriorFailure string` to `supervisor.Task` and
populate it in the loop before each retry.

Modules touched: `internal/supervisor` (field addition) + `internal/loop` (formatter +
loop threading). Total module scope: 2. F-003 / fitness-supervisor-isolation must still pass.

---

## Requirements

### REQ-107-01 — `supervisor.Task.PriorFailure` field
`supervisor.Task` gains a `PriorFailure string` field. All existing construction sites using
named-field syntax compile unchanged (zero-value `""`). The field is documented in
`docs/spec/data-model.md`.

### REQ-107-02 — `loop.FormatFailure` gate-fail output
`loop.FormatFailure(outcome Outcome) string` is exported from `internal/loop`. For a
`FailureGate` outcome it returns a string containing: the framing text `"previous attempt"`,
`"verification gate"`, the first failing step's `Name` and (truncated) `Output`, and a
closing `"Fix these issues"` instruction.

### REQ-107-03 — `loop.FormatFailure` non-gate failure reasons
For `FailureExecutorError` and `FailureExecutorIncomplete` outcomes, `FormatFailure` returns
a non-empty, human-readable string (does not panic). The string does NOT claim
`"verification gate"` for non-gate failure reasons.

### REQ-107-04 — Truncation cap
`loop.MaxFailureOutputBytes` is exported with value `2000`. `FormatFailure` truncates the
first failing step's `Output` to at most `MaxFailureOutputBytes` characters.

### REQ-107-05 — Loop threading: attempt 2 receives `PriorFailure` from attempt 1
In `RetryingLoop.RunOnce`, after an `OutcomeFail` on attempt N that will be retried (N <
`MaxAttempts`), `task.PriorFailure` is set to `loop.FormatFailure(outcome)` before the next
cycle is constructed. The executor stub in TC-107-05 must observe a non-empty `PriorFailure`
on the second call.

### REQ-107-06 — First attempt receives empty `PriorFailure`
The first attempt always has `task.PriorFailure == ""`. A success on the first attempt never
populates `PriorFailure` on any follow-on task.

### REQ-107-07 — Fitness: `make fitness-supervisor-isolation` passes
After adding the `PriorFailure` field, `make fitness-supervisor-isolation` exits 0. No new
import enters `internal/supervisor`.

### REQ-107-08 — Spec updated in the same commit
`docs/spec/data-model.md` documents the `PriorFailure` field. `docs/spec/behaviors.md`
documents that the retry loop propagates gate-failure detail to subsequent attempts.

---

## Acceptance criteria

Self-verify by running:
```
go test -count=1 ./internal/supervisor/... ./internal/loop/...
make fitness-supervisor-isolation
make check
```
All must pass. Additionally confirm:
- `go test -count=1 -run TestPriorFailureIsEmptyOnFirstAttempt ./internal/loop/...` passes.
- `go test -count=1 -run TestPriorFailureContainsStepNameOnSecondAttempt ./internal/loop/...` passes.
- `go test -count=1 -run TestFormatFailureTruncatesLongOutput ./internal/loop/...` passes.
- `go test -count=1 -run TestTaskContractAndBehaviorSpecUpdated ./internal/loop/...` passes
  (or whichever package hosts the doc-content assertions for TC-107-08).

---

## Verification plan

- **Highest level achievable:** L3 (`make fitness-supervisor-isolation` + `make check`
  green). Task 107 is an internal data-contract change with no directly observable runtime
  output. L5/L6 evidence (a full retry converging using fed-back failure) is claimed by
  task 108 after the harnesses consume the field.
- **L2 command:**
  ```
  go test -count=1 ./internal/supervisor/... ./internal/loop/...
  ```
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  make check
  ```
- **Spec doc assertion:**
  ```
  go test -count=1 -run TestTaskContractAndBehaviorSpecUpdated ./internal/loop/...
  ```

---

## Implementation notes

- Add `PriorFailure string` as the **last** field in `supervisor.Task` to avoid any positional
  initialization breakage.
- `FormatFailure` lives in `internal/loop` (same package as `RetryingLoop`). Do NOT put it in
  `internal/runtime` (that would create an import cycle: `runtime` already imports `loop`).
- The existing `runtime.summarizeVerdict` and `runtime.writeFailureEvidence` in
  `internal/runtime/run.go` are NOT changed — they serve the run-record/audit-stream path.
  `FormatFailure` produces a prompt-targeted string with the same information.
- Only the first failing step's output is included in `FormatFailure` output for
  `FailureGate` outcomes (matching `writeFailureEvidence` behavior).
- The `RetryingLoop.RunOnce` mutation is: after `case OutcomeFail:` and before
  `l.policy.Escalate(...)`, call `task.PriorFailure = FormatFailure(outcome)`. The mutated
  `task` is passed to the `singleTaskSource{task: task}` for the next cycle.

## Test spec

`docs/tasks/test-specs/107-gate-failure-loop-threading-test-spec.md`

## ADR

`docs/architecture/decisions/052-gate-failure-feedback-contract.md`
