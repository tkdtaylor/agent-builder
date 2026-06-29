# Test spec — Task 107: Gate-failure feedback: Task contract + loop threading

**Linked task:** `docs/tasks/backlog/107-gate-failure-loop-threading.md`  
**Written:** 2026-06-28  
**Status:** ready  
**Governing ADR:** ADR 052 (gate-failure feedback contract)  

---

## Context

`RetryingLoop.RunOnce` in `internal/loop/retry_policy.go` retries a task up to `MaxAttempts`
times. On each `OutcomeFail` that will be followed by another attempt it currently passes the
same original `supervisor.Task` unchanged to the next cycle. ADR 052 specifies that the loop
must populate `task.PriorFailure` before each retry so the next executor attempt receives
formatted gate-failure detail.

This spec covers two distinct scopes:

1. **`supervisor.Task` field** — a new `PriorFailure string` field added in
   `internal/supervisor/supervisor.go`.
2. **`internal/loop` formatting helper** — `FormatFailure(outcome Outcome) string` and the
   constant `MaxFailureOutputBytes` exported from `internal/loop`.
3. **`RetryingLoop.RunOnce` threading** — the loop populates `task.PriorFailure` from the
   previous attempt before constructing the next cycle, first attempt gets `""`.

---

## Requirements coverage

| Req ID       | Description                                                                                                           | Test cases          |
|--------------|-----------------------------------------------------------------------------------------------------------------------|---------------------|
| REQ-107-01   | `supervisor.Task` has a `PriorFailure string` field; existing code that does not set it compiles unchanged           | TC-107-01           |
| REQ-107-02   | `loop.FormatFailure` formats gate-fail outcomes: includes the failing step name and truncated output in the returned string | TC-107-02       |
| REQ-107-03   | `loop.FormatFailure` handles `FailureExecutorError` and `FailureExecutorIncomplete` outcomes without panicking, and returns a non-empty human-readable string | TC-107-03 |
| REQ-107-04   | `loop.MaxFailureOutputBytes == 2000`; output longer than the cap is truncated to exactly `MaxFailureOutputBytes` characters in `FormatFailure` output | TC-107-04 |
| REQ-107-05   | `RetryingLoop.RunOnce`: attempt 1 receives `task.PriorFailure == ""`; attempt 2 receives a non-empty `PriorFailure` containing the failed step name from attempt 1's verdict | TC-107-05 |
| REQ-107-06   | `RetryingLoop.RunOnce`: when `OutcomeDone` on attempt 1, `PriorFailure` is never set on any task | TC-107-06 |
| REQ-107-07   | `make fitness-supervisor-isolation` passes after the `PriorFailure` field is added — no forbidden package enters the supervisor import graph | TC-107-07 |
| REQ-107-08   | `docs/spec/data-model.md` and `docs/spec/behaviors.md` are updated in the same commit as the code change            | TC-107-08           |

---

## Test cases

### TC-107-01 — `supervisor.Task.PriorFailure` field compiles; zero-value is `""` (L2)

- **Requirement:** REQ-107-01
- **Level:** L2 (unit test in `internal/supervisor` or `internal/loop`)

**Input:** Construct a `supervisor.Task` using named fields without setting `PriorFailure`:
```go
t := supervisor.Task{ID: "001", Repo: "exec-sandbox", Spec: "/tasks/001.md"}
```

**Expected output (assertions):**
- `t.PriorFailure == ""` (zero value, no panic).
- The same `supervisor.Task` struct, when used as the argument to a stub `Executor.Run`, is
  accepted without error (the field is ignored by executors that have not yet been updated).
- `go build ./internal/supervisor/...` exits 0 with no new errors or warnings.

**Anti-goal:** this test must NOT touch executor files — the field exists on the Task; its
consumption is tested in task 108.

---

### TC-107-02 — `loop.FormatFailure` gate-fail output (L2)

- **Requirement:** REQ-107-02
- **Level:** L2 (unit test in `internal/loop`)

**Input:** Construct a `loop.Outcome` with:
```go
outcome := loop.Outcome{
    Kind: loop.OutcomeFail,
    Failure: loop.Failure{Reason: loop.FailureGate},
    Verdict: gate.Verdict{
        OK: false,
        Results: []gate.StepResult{
            {Name: "go-build", OK: true,  Output: ""},
            {Name: "go-test",  OK: false, Output: "FAIL: TestFoo panicked\nexit status 1"},
        },
    },
}
```
Call `loop.FormatFailure(outcome)`.

**Expected output (assertions):**
- Return value is a non-empty string.
- The string contains `"go-test"` (the failing step name).
- The string contains `"FAIL: TestFoo panicked"` (the step output).
- The string contains `"verification gate"` (confirms the framing text per ADR 052 §4).
- The string does NOT contain `"go-build"` — only the first failing step is included.
- The string contains `"Fix these issues"` (the closing instruction).

---

### TC-107-03 — `loop.FormatFailure` non-gate failure reasons (L2)

- **Requirement:** REQ-107-03
- **Level:** L2 (unit test in `internal/loop`)

**Sub-case A — `FailureExecutorError`:**

**Input:**
```go
outcome := loop.Outcome{
    Kind: loop.OutcomeFail,
    Failure: loop.Failure{
        Reason: loop.FailureExecutorError,
        Err:    errors.New("subprocess: exit status 2"),
    },
}
```
Call `loop.FormatFailure(outcome)`.

**Expected:**
- Return value is a non-empty string (does not panic).
- String contains `"error"` (indicates an executor error, per ADR 052 §4).
- String does NOT contain `"verification gate"` (wrong reason code — should not claim gate failure).

**Sub-case B — `FailureExecutorIncomplete`:**

**Input:**
```go
outcome := loop.Outcome{
    Kind: loop.OutcomeFail,
    Failure: loop.Failure{Reason: loop.FailureExecutorIncomplete},
}
```
Call `loop.FormatFailure(outcome)`.

**Expected:**
- Return value is a non-empty string.
- String contains `"branch"` (the produced-branch expectation, per ADR 052 §4).
- String does NOT contain `"verification gate"`.

---

### TC-107-04 — Truncation at `MaxFailureOutputBytes` (L2)

- **Requirement:** REQ-107-04
- **Level:** L2 (unit test in `internal/loop`)

**Input:** Verify `loop.MaxFailureOutputBytes == 2000` (exact constant value check).

Construct a `loop.Outcome` with a `FailureGate` outcome whose first failing step has
`Output` of length 3 000 bytes (3 000 ASCII characters, e.g. `strings.Repeat("x", 3000)`):
```go
outcome := loop.Outcome{
    Kind: loop.OutcomeFail,
    Failure: loop.Failure{Reason: loop.FailureGate},
    Verdict: gate.Verdict{
        OK: false,
        Results: []gate.StepResult{
            {Name: "golangci-lint", OK: false, Output: strings.Repeat("x", 3000)},
        },
    },
}
```
Call `loop.FormatFailure(outcome)`.

**Expected output (assertions):**
- `loop.MaxFailureOutputBytes == 2000` (compile-time constant, verified by value assertion).
- The returned string contains at most `MaxFailureOutputBytes` characters of the `"x"` run
  (i.e. exactly 2 000 `"x"` characters and no more).
- The returned string does NOT contain the full 3 000-character output (truncation occurred).

---

### TC-107-05 — Loop threads `PriorFailure` into attempt 2 from attempt 1's failed gate verdict (L2)

- **Requirement:** REQ-107-05
- **Level:** L2 (unit test in `internal/loop` using a stub executor and gate)

**Setup:** Create a `capturingExecutor` stub that records every `supervisor.Task` it receives
in `Run`. Create a `stubbedGate` that returns a failing verdict on attempt 1 with a known
step name (e.g. `"go-fmt"` with `Output: "file.go"`) and a passing verdict on attempt 2.
Construct a `RetryingLoop` with `MaxAttempts: 2`, the capturing executor, the stubbed gate,
a stub `StatusWriter`, and a stub `EscalationHook` that returns the same executor.

**Input:** Call `RunOnce()`.

**Expected output (assertions):**
- `RunOnce` returns `RetryOutcome{Kind: RetryOutcomeDone}` (attempt 2 succeeded after
  attempt 1 failed).
- The `capturingExecutor.receivedTasks` slice has exactly 2 entries.
- `receivedTasks[0].PriorFailure == ""` — the first attempt has no prior failure.
- `receivedTasks[1].PriorFailure != ""` — the second attempt has a non-empty prior failure.
- `strings.Contains(receivedTasks[1].PriorFailure, "go-fmt")` is `true` — the failing step
  name from attempt 1 is present in attempt 2's `PriorFailure`.
- `strings.Contains(receivedTasks[1].PriorFailure, "file.go")` is `true` — the step output
  from attempt 1 is present in attempt 2's `PriorFailure`.

---

### TC-107-06 — Loop does not set `PriorFailure` when attempt 1 succeeds (L2)

- **Requirement:** REQ-107-06
- **Level:** L2 (unit test in `internal/loop`)

**Setup:** Use a `capturingExecutor` and a `stubbedGate` that returns a PASSING verdict on
attempt 1. Construct `RetryingLoop` with `MaxAttempts: 3`.

**Input:** Call `RunOnce()`.

**Expected output (assertions):**
- `RunOnce` returns `RetryOutcome{Kind: RetryOutcomeDone}` with `Attempts == 1`.
- `receivedTasks` has exactly 1 entry.
- `receivedTasks[0].PriorFailure == ""` — no prior failure was set on the only attempt.

---

### TC-107-07 — `make fitness-supervisor-isolation` passes after the field addition (L3)

- **Requirement:** REQ-107-07
- **Level:** L3 (fitness)

**Input:** After implementing `supervisor.Task.PriorFailure`, run:
```
make fitness-supervisor-isolation
```

**Expected output (assertions):**
- Exit code 0.
- Stdout contains `PASS fitness-supervisor-isolation`.
- The `internal/supervisor` import graph contains no `executor/`, `runtime/`, `loop/`, or
  web-fetch packages (the field addition must not introduce any new import).

Additionally run:
```
make check
```
Expected: `All checks passed.` (lint, build, test, fitness all green).

---

### TC-107-08 — `docs/spec/data-model.md` and `behaviors.md` updated (L2 documentary)

- **Requirement:** REQ-107-08
- **Level:** L2 (file content assertions in a Go test)

**Input:** Read `docs/spec/data-model.md` and `docs/spec/behaviors.md` from the repo root
using `os.ReadFile` (repo-relative path via `runtime.Caller(0)`).

**Expected output (assertions):**

`data-model.md`:
- Contains `"PriorFailure"` in the `supervisor.Task` value table.
- The entry describes the field as non-empty only on retry attempts N≥2.

`behaviors.md`:
- Contains `"PriorFailure"` OR `"gate-failure feedback"` OR `"prior failure"` (case-insensitive),
  confirming that the retry-propagation behavior is documented.
- The relevant section describes that the first attempt has an empty value and subsequent
  attempts receive the formatted failure from the previous attempt.

---

## Verification plan

- **Highest level achievable:** L3 (`make fitness-supervisor-isolation` + `make check` green).
  L5/L6 evidence (an operator run where attempt 1 fails a gate step and attempt 2 fixes it
  using the fed-back failure) is tracked under task 108's L6 entry; task 107 is an internal
  data-contract change with no directly observable runtime output beyond what L3 covers.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/supervisor/... ./internal/loop/...
  ```
  Expected: `ok` for both packages.
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  make check
  ```
  Expected: `PASS fitness-supervisor-isolation` + `All checks passed.`
- **Spec documentary check:**
  ```
  go test -count=1 -run TestTaskContractAndBehaviorSpecUpdated ./internal/loop/...
  ```
  (or whichever package hosts the file-content assertions)

## Out of scope

- Harness prompt injection (task 108).
- The specific runtime observation of a retry converging on a gate failure (task 108 L6).
- Any change to `internal/runtime/run.go` formatting helpers — they are not touched here.
