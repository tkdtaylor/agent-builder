# Test Spec 156: wall-clock timeout and cancel actually stop the running executor subprocess

**Linked task:** [`docs/tasks/backlog/156-supervisor-cancel-timeout-stops-executor.md`](../backlog/156-supervisor-cancel-timeout-stops-executor.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

Task 155 threads `context.Context` from `Supervisor.Run(ctx)` down to
`Executor.Run(ctx, task)`, so a CALLER-cancelled `ctx` now reaches the in-flight
executor. Two gaps remain, both closed by this task:

1. **The wall-clock timeout arm does not cancel anything.** `Supervisor.Run`'s
   `select` (`internal/supervisor/supervisor.go:335-350`) has a `case <-timerC:` arm
   that calls `s.killAndJoin(handle, timeoutErr, loopResult)`
   (`internal/supervisor/supervisor.go:371-391`). `killAndJoin` calls `s.box.Kill(handle)`
   — in production, `sandboxBox.Kill` is a literal no-op (`internal/runtime/run.go:1143-1145`,
   `return nil`) — and then blocks UNCONDITIONALLY on `<-loopResult` (line 376) with no
   cancellation signal sent anywhere. The threaded `ctx` from task 155 is never touched
   by the timer firing, so the in-flight executor's subprocess runs to completion
   regardless of the configured wall-clock deadline. `killAndJoin` returns only after
   the (uncancelled, still-running) loop goroutine finishes on its own.
2. **A false comment misrepresents current behavior.**
   `internal/supervisor/cancel_test.go:41-42` states `// A killed box's in-box loop
   terminates — unblock the loop so killAndJoin completes (the production box.Kill
   stops the real process).` — this is FALSE for production: `sandboxBox.Kill` does
   nothing; the test's own `fakeBox.onKill` callback is what unblocks the fake loop,
   not anything `box.Kill` does in production.

The fix: `Supervisor.Run` derives its OWN cancellable child context
(`runCtx, cancelRun := context.WithCancel(ctx)`) and passes `runCtx` (not the raw
caller `ctx`) into `InBoxLoop.RunInside`. The cancel arm already gets this for free
(`runCtx` is a child of `ctx`, so `ctx.Done()` firing cascades to `runCtx.Done()`
automatically). The timeout arm additionally calls `cancelRun()` before/alongside
`killAndJoin`, so a wall-clock timeout ALSO reaches and cancels the in-flight
executor's subprocess — closing the actual dead-wire gap the review identified.

`sandboxBox.Kill`/`Teardown` remain no-ops after this task (there is currently no
persistent box-level process for them to kill — the CLI-shaped executors invoke their
subprocess directly via `exec.CommandContext`, not through a wrapped, separately
killable containment-box process); the real termination mechanism this task wires is
context-cancellation of the executor, not a box-level kill. This task requires the
no-op status be accurately DOCUMENTED (not silently misleading), and requires the
`cancel_test.go:41-42` comment be corrected to state the true mechanism.

**Module boundaries touched:** `internal/supervisor` only (the `Run`/`killAndJoin`
implementation, `sandboxBox`'s comment via its production home in `internal/runtime`,
and the `cancel_test.go` comment fix). `internal/executor`'s
`ollama_native.go:112-113` `// TODO: context should come from supervisor` comment is
removed (now literally true — the context DOES come from the supervisor as of task 155).

---

## Requirements coverage

| Req ID     | Description                                                                                                                       | Test cases            |
|------------|--------------------------------------------------------------------------------------------------------------------------------------|-------------------------|
| REQ-156-01 | `Supervisor.Run` derives a cancellable child context (`context.WithCancel(ctx)`) and passes it (not the raw `ctx`) into `InBoxLoop.RunInside`, so the executor sees the SAME context whether cancellation originates from the caller or from the supervisor's own timeout | TC-156-01              |
| REQ-156-02 | The wall-clock TIMEOUT arm (`case <-timerC:`) calls the derived context's cancel function BEFORE OR AS PART OF `killAndJoin`, so a context-aware in-flight executor observes cancellation on a timeout — not only on a caller cancel | TC-156-02, TC-156-03  |
| REQ-156-03 | The pre-existing cancel-arm behavior (task 116) is unchanged in externally observable outcome: `ErrRunCancelled`, `box.Kill`/`Teardown` call ordering, and the partial-teardown-leak error-join behavior all still hold | TC-156-04              |
| REQ-156-04 | The false comment at `internal/supervisor/cancel_test.go:41-42` ("the production box.Kill stops the real process") is corrected to accurately describe the real mechanism (context cancellation of the executor, not a box-level kill) | TC-156-05              |
| REQ-156-05 | `sandboxBox.Kill`/`Teardown` in `internal/runtime/run.go` remain no-ops but their doc comments accurately state WHY (no persistent box-level process exists to kill; termination is via the executor's cancelled context) rather than implying they do real work | TC-156-06              |
| REQ-156-06 | `internal/executor/ollama_native.go`'s `// TODO: context should come from supervisor` comment is removed, since as of tasks 155-156 the context genuinely does come from the supervisor on every call path | TC-156-07              |
| REQ-156-07 | Pre-existing suites in `internal/supervisor` and `tests/supervisor` continue to pass unchanged in behavior (only the one corrected comment and any necessarily-updated fake-loop wiring change) | TC-156-08              |

---

## Pre-implementation checklist

- [ ] Task 155 merged (`Executor.Run(ctx, ...)` and `InBoxLoop.RunInside(ctx, ...)`
  plumbing already threads a context end-to-end)
- [ ] `make check` green before branching

---

## Test cases

### TC-156-01 — `Supervisor.Run` derives and passes a child context, not the raw caller ctx

- **Requirement:** REQ-156-01
- **Level:** L2 (unit test)
- **Test file:** `internal/supervisor/supervisor_test.go` or a new `cancel_timeout_156_test.go`

**Setup:** A `fakeInBoxLoop.RunInside(ctx, ...)` that records the received `ctx` and
returns it (or a derived comparison) so the test can assert it is NOT the exact same
`ctx` value passed to `sup.Run` (i.e., it is a child of it) while still being
"Done" whenever the parent is Done.

**Step:** Call `sup.Run(callerCtx)`; inside the fake loop, verify
`errors.Is(recordedCtx.Err(), nil)` initially, then cancel `callerCtx` externally (from
a second goroutine, simulating an unrelated caller-side cancel) and confirm
`recordedCtx.Done()` fires.

**Expected output:** The recorded ctx is a distinct (child) context object from
`callerCtx`, and cancelling `callerCtx` correctly cascades to the recorded ctx's
`Done()` channel — proving `context.WithCancel(ctx)` parent/child semantics are wired
correctly, not a coincidental pass-through.

---

### TC-156-02 — The wall-clock timeout cancels the context an in-flight executor observes

- **Requirement:** REQ-156-02
- **Level:** L5 (real production chain: `Supervisor` → `retryingInBoxLoop` → `agentloop` → context-aware fake executor)
- **Test file:** `internal/supervisor/cancel_timeout_156_test.go` (new)

**Setup:** A `slowContextAwareExecutor` (same shape as task 155's TC-155-07 fixture)
whose `Run(ctx, task)` blocks on `<-ctx.Done()` and returns `(Result{}, ctx.Err())`
immediately once it fires, with a long fallback safety timeout. Wire it through the
REAL `retryingInBoxLoop`. Configure `Supervisor` via `WithRunTimeout(50 * time.Millisecond)`
— short enough that the timer fires well before the executor's fallback timeout, and
the executor never finishes "naturally."

**Step:** Call `sup.Run(context.Background())` (an un-cancelled caller ctx — ONLY the
wall-clock timer can trigger termination here).

**Expected output:** `sup.Run` returns `supervisor.ErrRunTimedOut` (joined with the
loop's error reflecting `context.Canceled` from the executor observing the derived
ctx's cancellation) within a bounded time close to the configured 50ms timeout — NOT
after the executor's long fallback safety timeout. This is the load-bearing assertion
distinguishing "the dead wire is now live" from "the select fired but nothing
downstream noticed" (the pre-156 bug: `killAndJoin` would block on `<-loopResult`
until the executor's OWN long fallback fired, proving the timeout was cosmetic).

---

### TC-156-03 — `killAndJoin` returns promptly on timeout without leaking the loop goroutine

- **Requirement:** REQ-156-02
- **Level:** L2/L5 (same harness as TC-156-02, additional assertion)
- **Test file:** `internal/supervisor/cancel_timeout_156_test.go`

**Step:** Reuse TC-156-02's setup. After `sup.Run` returns, assert (via a channel or
`sync/atomic` flag set at the top of the fake executor's goroutine and cleared just
before it returns) that the executor's `Run` call has ALREADY returned by the time
`sup.Run` returns — i.e., `killAndJoin`'s `<-loopResult` read is not still pending
when the outer call returns.

**Expected output:** No goroutine leak: the executor's `Run` observably completes
before or by the time `Supervisor.Run` returns, for both the timeout and (as a
regression check) the pre-existing cancel-arm path.

---

### TC-156-04 — Cancel-arm behavior (task 116) is unchanged

- **Requirement:** REQ-156-03
- **Level:** L2/L5 (regression)
- **Test file:** `internal/supervisor/cancel_test.go` (existing `TestTC116_02_...`, `TestTC116_05_...`)

**Step:** Run the existing task-116 cancel-arm tests unmodified (aside from the
`RunInside`/`Executor.Run` signature updates task 155 already applied) after this
task's `context.WithCancel` refactor.

**Expected output:** `TestTC116_02_CancelArmTearsDownBoxViaKillTeardown` and
`TestTC116_05_...` (partial-teardown-leak case) both still pass with IDENTICAL
externally observable behavior: `box.Kill`/`Teardown` call ordering and counts,
`ErrRunCancelled` returned, kill-error `errors.Join`'d and surfaced. No new failure
mode is introduced for the cancel arm by adding the child-context derivation.

---

### TC-156-05 — The false comment is corrected

- **Requirement:** REQ-156-04
- **Level:** L1 (source inspection, asserted via a lightweight test or reviewed directly)
- **Test file:** `internal/supervisor/cancel_test.go` (comment-only diff, verified by reading the file)

**Step:** Grep `internal/supervisor/cancel_test.go` for the string `"the production
box.Kill stops the real process"`.

**Expected output:** Zero matches — the comment is rewritten to state the true
mechanism (e.g. "a killed box's in-box loop terminates via the fake's `onKill`
callback here; in PRODUCTION, termination is driven by the run-scoped context being
cancelled — see task 156 — not by `sandboxBox.Kill`, which remains a no-op").

---

### TC-156-06 — `sandboxBox.Kill`/`Teardown` doc comments are accurate, not misleading

- **Requirement:** REQ-156-05
- **Level:** L1 (source inspection)
- **Test file:** `internal/runtime/run.go` (comment-only diff)

**Step:** Read `sandboxBox.Kill`/`Teardown`'s doc comments in `internal/runtime/run.go`.

**Expected output:** The comments state plainly that these remain intentional no-ops
because no separately-killable box-level process exists in the current architecture,
and that real subprocess termination is achieved via the run-scoped cancellable
context threaded through `Executor.Run` (tasks 155-156) — not implying, as the
pre-existing false test comment did, that `Kill` stops a real process today.

---

### TC-156-07 — Ollama's stale TODO is removed

- **Requirement:** REQ-156-06
- **Level:** L1 (source inspection)
- **Test file:** `internal/executor/ollama_native.go`

**Step:** Grep `internal/executor/ollama_native.go` for `"context should come from
supervisor"`.

**Expected output:** Zero matches — the TODO is removed (it is resolved by tasks
155-156: `Run(ctx, t)`'s `ctx` parameter genuinely comes from the supervisor now).

---

### TC-156-08 — Full regression: `internal/supervisor` and `tests/supervisor` pass unchanged

- **Requirement:** REQ-156-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/supervisor/... ./tests/supervisor/... ./internal/executor/...
make check
```

**Expected output:** All packages `ok`; `make check` → `All checks passed.`;
`make fitness-supervisor-isolation` still PASS (no new import into
`internal/supervisor` — `context` is already imported).

---

## Verification plan

- **Highest level achievable:** L5 — the production chain (`Supervisor` →
  `retryingInBoxLoop` → `agentloop` → context-aware fake executor) demonstrates the
  wall-clock timeout arm actually terminating an in-flight executor within a bounded
  time close to the configured deadline, not after a long fallback. This is the
  strongest evidence achievable without a live subprocess/containment box; a live L6
  run adds no additional confidence for this specific fix (the mechanism under test is
  Go-level context propagation, already exercised faithfully by the L5 harness).
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/supervisor/... ./tests/supervisor/... ./internal/executor/...
  ```
  Expected: all packages `ok`.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/supervisor/... -run 'TestTC156'
  ```
  Expected: TC-156-02's bounded-time timeout-triggered termination assertion passes
  (fails loudly, not by hanging, against the pre-156 code).
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Making `sandboxBox.Kill`/`Teardown` do real container-level work — no live Podman
  box currently wraps the executor subprocess persistently enough to make this
  meaningful; that would be a containment-architecture change, not this dead-wire fix.
- Any change to router escalation/quota machinery (tasks 160-162).
- A live Claude/Codex/Ollama L6 run.
