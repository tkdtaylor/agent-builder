# Test Spec 155: thread `context.Context` through `supervisor.Executor` and `supervisor.InBoxLoop`

**Linked task:** [`docs/tasks/backlog/155-executor-context-threading.md`](../backlog/155-executor-context-threading.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

`supervisor.Executor.Run(t Task) (Result, error)` (`internal/supervisor/supervisor.go:69-71`)
takes no context. Every concrete executor's `Run` method calls its own
`context.Background()`-rooted internal implementation instead of an inbound context:

- `ClaudeCLI.Run` → `e.RunContext(context.Background(), task)` (`internal/executor/claude_cli.go:203-205`)
- `CodexCLI.Run` → `c.run(context.Background(), task)` (`internal/executor/codex_cli.go:67-69`)
- `GeminiCLI.Run` → `g.run(context.Background(), task)` (`internal/executor/gemini_cli.go:67-68`)
- `AntigravityCLI.Run` → `a.run(context.Background(), task)` (`internal/executor/antigravity_cli.go:63-64`)
- `OllamaNative.Run` builds `ctx := context.Background()` directly with a `// TODO:
  context should come from supervisor` comment (`internal/executor/ollama_native.go:112-113`)

`supervisor.InBoxLoop.RunInside(BoxHandle, Task, RunStreams) error`
(`internal/supervisor/supervisor.go:124-126`) is equally contextless: `Supervisor.Run`'s
`runLoop` goroutine calls `s.loop.RunInside(handle, s.task, streams)` with no ctx
(`internal/supervisor/supervisor.go:397-411`), even though `Supervisor.Run(ctx
context.Context)` already receives and selects on a caller-supplied `ctx` for its
cancel arm (`case <-ctx.Done():`, line 338). The plumbing gap means a caller
cancellation currently only reaches `box.Kill`/`box.Teardown` (both no-ops in
production — task 156's scope) and never reaches the in-flight executor subprocess.

This task threads ONE context end-to-end: `Supervisor.Run`'s `ctx` →
`InBoxLoop.RunInside(ctx, ...)` → `agentloop.RetryingLoop.RunOnce(ctx)` →
`agentloop.Loop.RunOnce(ctx)` (the cycle used inside `RetryingLoop.RunOnce`) →
`supervisor.Executor.Run(ctx, task)` → each concrete executor's existing
`RunContext`/`run(ctx, task)` internal implementation (already correct — they already
build `cmd.Cancel` correctly per `internal/sandbox/podman/run.go:121-130`'s pattern
via `exec.CommandContext`).

After this task, a context cancelled by the CALLER (e.g. a goal `cancel <goalID>`,
which already derives and cancels a per-goal context — ADR 054 §5) will, for the
first time, actually reach and cancel the in-flight executor subprocess. The
WALL-CLOCK TIMEOUT arm does **not** yet gain this effect — `Supervisor.Run`'s local
`timer.C` fires independently of the passed-in `ctx` and does not cancel it. That gap
is task 156's scope (a supervisor-owned derived cancellable context, cancelled on
BOTH the cancel arm and the timeout arm).

**Module boundaries touched:** `internal/supervisor` (two interface signatures),
`internal/executor` (5 concrete `Run` methods), `internal/loop` (`Loop.RunOnce`,
`RetryingLoop.RunOnce` gain a `ctx` parameter), `internal/runtime`
(`retryingInBoxLoop.RunInside` threads the received ctx through), and every test
double across `internal/supervisor/*_test.go`, `tests/supervisor/*_test.go`,
`internal/executor/*_test.go`, `internal/loop/*_test.go`, `tests/loop/*_test.go`,
`internal/runtime/run_audit_test.go`, `internal/router/resolve_test.go` that
implements `supervisor.Executor` or `supervisor.InBoxLoop`.

F-003 (`fitness-supervisor-isolation`) must remain green — this only changes a
method signature on two existing seam interfaces already declared inside
`internal/supervisor`; it adds no new import into that package (`context` is
already imported there).

---

## Requirements coverage

| Req ID     | Description                                                                                                  | Test cases           |
|------------|----------------------------------------------------------------------------------------------------------------|-----------------------|
| REQ-155-01 | `supervisor.Executor`'s method signature becomes `Run(ctx context.Context, t Task) (Result, error)`; all five concrete executors (Claude, Codex, Gemini, Antigravity, Ollama-native) implement it by using the PASSED-IN ctx (not a freshly-built `context.Background()`) | TC-155-01, TC-155-02 |
| REQ-155-02 | `supervisor.InBoxLoop`'s method signature becomes `RunInside(ctx context.Context, handle BoxHandle, t Task, streams RunStreams) error`; `Supervisor.Run`'s `runLoop` goroutine passes its own `ctx` parameter through, not `context.Background()` | TC-155-03             |
| REQ-155-03 | `internal/loop.Loop.RunOnce` and `internal/loop.RetryingLoop.RunOnce` gain a `ctx context.Context` parameter and pass it to every `executor.Run` call they make (including each retry attempt inside `RetryingLoop.RunOnce`'s loop) | TC-155-04, TC-155-05 |
| REQ-155-04 | `internal/runtime`'s `retryingInBoxLoop.RunInside` uses its now-received `ctx` (not `context.Background()`) when constructing and running the `agentloop.RetryingLoop` | TC-155-06             |
| REQ-155-05 | End-to-end: cancelling the context passed into `Supervisor.Run(ctx)` while a context-aware fake executor is mid-`Run` call causes that executor's `Run` to observe `ctx.Done()` and return promptly — proving the full plumbing chain is live, not merely type-correct | TC-155-07             |
| REQ-155-06 | All five concrete executor packages' pre-existing test suites, `internal/loop`, `tests/loop`, `internal/supervisor`, `tests/supervisor`, and `internal/runtime` continue to pass after the signature change (mechanical caller-site updates only, no behavior regression) | TC-155-08             |

---

## Pre-implementation checklist

- [x] Task 116 merged (`Supervisor.Run(ctx context.Context)` and the cancel arm already exist)
- [x] `RunContext`/`run(ctx, task)` internal implementations already exist and correctly
  wire `exec.CommandContext` on all four CLI-shaped executors (Claude/Codex/Gemini/Antigravity)
- [ ] `make check` green before branching

---

## Test cases

### TC-155-01 — Each concrete executor's `Run` uses the passed context, not `context.Background()`

- **Requirement:** REQ-155-01
- **Level:** L2 (unit test, one sub-test per executor)
- **Test files:** `internal/executor/claude_cli_test.go`, `codex_cli_test.go`, `gemini_cli_test.go`, `antigravity_cli_test.go`, `ollama_native_test.go`

**Setup:** For each of the four CLI-shaped executors, construct it with a stubbed
`cmdFactory`/`commandCreator` that records the `ctx` it was invoked with (e.g. returns
a command whose `Process` never actually runs, or a short-lived `exec.CommandContext(ctx,
"true")`). For `OllamaNative`, inject a stub `Chatter` whose `Chat(ctx, req)` records
the received `ctx`.

**Step:** Call `Run(ctx, task)` with a `ctx` derived from `context.WithValue(context.Background(),
someKey, "marker")` (a context distinguishable from a bare `context.Background()`).

**Expected output:** The stub records a `ctx` whose `Value(someKey)` equals `"marker"`
for all five executors — proving the passed-in context (not a fresh `Background()`)
reached the subprocess/HTTP-call boundary in every implementation.

---

### TC-155-02 — A cancelled context aborts an in-flight CLI executor's subprocess promptly

- **Requirement:** REQ-155-01
- **Level:** L2 (unit test, mirrors the existing `RunContext` cancellation coverage but now exercised through the renamed `Run(ctx, ...)` entry point)
- **Test file:** `internal/executor/claude_cli_test.go` (representative; at least one other CLI-shaped executor gets an equivalent case)

**Setup:** Stub `cmdFactory` to launch a long-sleeping real subprocess (e.g. `sleep 30`,
matching the existing `RunContext` cancellation test pattern already in this file).

**Step:** Call `Run(ctx, task)` with a `ctx` that is cancelled ~100ms after the call
starts (via `context.WithTimeout` or an explicit goroutine calling `cancel()`).

**Expected output:** `Run` returns within a bounded time (well under the subprocess's
30s sleep) with a non-nil error wrapping `context.Canceled` or reflecting the killed
subprocess — proving `Run(ctx, ...)` is not merely type-compatible but functionally
delegates to the existing correct `cmd.Cancel`-based termination path.

---

### TC-155-03 — `Supervisor.Run` passes its own `ctx` into `InBoxLoop.RunInside`

- **Requirement:** REQ-155-02
- **Level:** L2 (unit test, `internal/supervisor`)
- **Test file:** `internal/supervisor/supervisor_test.go` (extend the existing `fakeInBoxLoop`)

**Setup:** A `fakeInBoxLoop` whose `RunInside(ctx, handle, task, streams)` records the
received `ctx`. Construct `Supervisor` with `WithTask`, a `fakeBox`, and this fake loop.

**Step:** Call `sup.Run(ctx)` with `ctx := context.WithValue(context.Background(),
someKey, "marker-155-03")`.

**Expected output:** `fakeInBoxLoop.RunInside` recorded a `ctx` whose
`Value(someKey)` equals `"marker-155-03"` — not `context.Background()`.

---

### TC-155-04 — `loop.Loop.RunOnce(ctx)` passes ctx to `executor.Run`

- **Requirement:** REQ-155-03
- **Level:** L2 (unit test, `internal/loop` or `tests/loop`)
- **Test file:** `tests/loop/loop_test.go`

**Setup:** A fake `supervisor.Executor` whose `Run(ctx, task)` records the received
`ctx`. Construct a `Loop` via `loop.New(...)`.

**Step:** Call `l.RunOnce(ctx)` with a marked `ctx`.

**Expected output:** The fake executor's recorded `ctx` carries the same marker value.

---

### TC-155-05 — `RetryingLoop.RunOnce(ctx)` passes the SAME ctx to every retry attempt

- **Requirement:** REQ-155-03
- **Level:** L2 (unit test, `tests/loop`)
- **Test file:** `tests/loop/retry_policy_test.go`

**Setup:** A fake executor that fails the gate (via a fake `supervisor.Gate` that
always returns `OK: false`) on every attempt and records the `ctx` it received on each
of `MaxAttempts` (e.g. 3) calls. Build a `RetryingLoop` with `MaxAttempts: 3` and
`agentloop.BootstrapEscalationHook`.

**Step:** Call `l.RunOnce(ctx)` with a marked `ctx`.

**Expected output:** All 3 recorded `ctx` values on the fake executor carry the same
marker — the retry loop threads ONE ctx through every attempt, not a fresh
`context.Background()` per attempt.

---

### TC-155-06 — `retryingInBoxLoop.RunInside` uses its received ctx, not `context.Background()`

- **Requirement:** REQ-155-04
- **Level:** L2 (unit test, `internal/runtime`)
- **Test file:** `internal/runtime/run_audit_test.go` (extend) or a new `internal/runtime/run_context_test.go`

**Setup:** A fake `supervisor.Executor` (already used by the existing `run_audit_test.go`
harness) whose `Run(ctx, task)` records the received `ctx`. Wire it into a
`retryingInBoxLoop` alongside a passing fake gate/publisher.

**Step:** Call `l.RunInside(ctx, handle, task, streams)` with a marked `ctx`.

**Expected output:** The fake executor's recorded `ctx` carries the marker — proving
`retryingInBoxLoop` (the production `InBoxLoop` implementation used by both `run` and
`orchestrate`) forwards the real ctx rather than rebuilding `context.Background()`.

---

### TC-155-07 — End-to-end: a cancelled `Supervisor.Run(ctx)` context reaches and stops an in-flight fake executor

- **Requirement:** REQ-155-05
- **Level:** L5 (fake-executor harness spanning `Supervisor.Run` → `InBoxLoop` → `RetryingLoop`/`Loop` → `Executor.Run`)
- **Test file:** `internal/supervisor/context_threading_155_test.go` (new)

**Setup:** A `slowContextAwareExecutor` whose `Run(ctx, task)` blocks on `<-ctx.Done()`
(with a long fallback timeout as a test safety net) and, when `ctx.Done()` fires,
returns `(Result{}, ctx.Err())` immediately. Wire it through the REAL
`retryingInBoxLoop` (not a fake `InBoxLoop`) so the full production chain is exercised:
`Supervisor` → `retryingInBoxLoop.RunInside` → `agentloop.RetryingLoop.RunOnce` →
`agentloop.Loop.RunOnce` → `slowContextAwareExecutor.Run`.

**Step:** Start `sup.Run(ctx)` in a goroutine with a cancellable `ctx`; once the test
observes (via a synchronization channel set inside the fake executor) that `Run` has
been entered, cancel `ctx`.

**Expected output:** `slowContextAwareExecutor.Run` observes `ctx.Done()` and returns
within a bounded time (e.g. under 1s), and `sup.Run` returns
`supervisor.ErrRunCancelled` (joined with the loop's `ctx.Err()`) within a bounded
time — proving the plumbing is functionally live end-to-end, not merely
type-threaded. Contrast: on the PRE-155 code, this same fake executor (ignoring an
internally-built `context.Background()`) would never observe cancellation and the test
would need to wait for its long fallback timeout — this test is written to fail loudly
(not hang silently) against the old code by asserting the bounded-time completion.

---

### TC-155-08 — Full regression: pre-existing suites pass unchanged elsewhere

- **Requirement:** REQ-155-06
- **Level:** L2/L3 (regression run)

**Step:**
```
go test -race -count=1 ./internal/executor/... ./internal/loop/... ./tests/loop/... ./internal/supervisor/... ./tests/supervisor/... ./internal/runtime/... ./internal/router/...
make check
```

**Expected output:** All packages `ok`. Only mechanical call-site updates (adding a
`ctx` argument to `Run`/`RunInside`/`RunOnce` calls in existing tests) are expected to
change — no assertion on branch names, gate verdicts, or audit output changes.
`make fitness-supervisor-isolation` and `make fitness-orchestrator-no-executor` both
still PASS (interface signature change only; no new import edges). `make check` →
`All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5 — a fake-executor harness spanning the full
  production call chain (`Supervisor.Run` → `retryingInBoxLoop` → `agentloop` →
  `Executor.Run`) proves the plumbing is live, not merely type-correct. No live
  Claude/Codex/Ollama subprocess is required to prove this task's REQs; task 156
  covers the wall-clock-timeout-specific behavior and the sandboxBox/false-comment
  cleanup.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/executor/... ./internal/loop/... ./tests/loop/...
  ```
  Expected: all packages `ok`; TC-155-01/02/04/05 pass.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/supervisor/... -run TestTC155_07
  ```
  Expected: the slow context-aware fake executor observes cancellation and `Supervisor.Run`
  returns `ErrRunCancelled` within the test's bounded-time assertion.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Making the WALL-CLOCK TIMEOUT arm cancel the threaded context — `timer.C` firing
  today does not cancel `ctx`; this task threads the plumbing, task 156 makes the
  timeout arm actually cancel it.
- Fixing `sandboxBox.Kill`/`Teardown`'s no-op status or the false comment in
  `internal/supervisor/cancel_test.go:42-43` — task 156.
- Any change to `router`'s escalation/quota machinery (task 160-162).
- A live Claude/Codex/Ollama L6 run — no new runtime-observable surface beyond the
  fake-executor L5 harness is needed to prove this task's REQs.
