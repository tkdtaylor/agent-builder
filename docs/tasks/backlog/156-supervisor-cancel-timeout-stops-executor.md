# Task 156: wall-clock timeout and cancel actually stop the running executor subprocess

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Make `Supervisor.Run`'s wall-clock TIMEOUT arm — not just the cancel arm — actually
terminate the in-flight executor's subprocess, by deriving a supervisor-owned
cancellable child context and cancelling it on the timeout trigger. Fix the false
"production box.Kill stops the real process" comment in
`internal/supervisor/cancel_test.go:41-43`, document `sandboxBox.Kill`/`Teardown`'s
genuine no-op status accurately, and remove the now-resolved TODO in
`internal/executor/ollama_native.go`. This is step (b) of the two-step fix for the
dead-wire cancel/timeout finding (step (a), the context-threading plumbing, is task
155 — this task depends on it).

## Context

**Root cause (full-project review, verified 2026-07-02):** after task 155 threads a
context from `Supervisor.Run(ctx)` down to `Executor.Run(ctx, task)`, a CALLER
cancellation (e.g. `cancel <goalID>`) reaches the in-flight executor. But
`Supervisor.Run`'s wall-clock TIMEOUT arm (`internal/supervisor/supervisor.go:344-349`)
does nothing to that context — the local `timer.C` firing is unrelated to `ctx`. In
production, `sandboxBox.Kill`/`Teardown` (`internal/runtime/run.go:1143-1149`) are
literal no-ops (`return nil`), and `killAndJoin` (`internal/supervisor/supervisor.go:371-391`)
blocks unconditionally on `<-loopResult` with no cancellation signal reaching the
running executor — so a timed-out task's subprocess runs to completion regardless of
the configured wall-clock deadline. The ONLY thing the timeout arm currently
accomplishes is an early RETURN from `Supervisor.Run` to its own caller; the box and
the in-flight subprocess are unaffected, and `killAndJoin` cannot return until the
subprocess finishes on its own (potentially never, for a genuinely runaway process —
the exact failure mode a wall-clock kill exists to prevent).

Additionally, `internal/supervisor/cancel_test.go:41-42`'s comment ("A killed box's
in-box loop terminates — unblock the loop so killAndJoin completes (the production
box.Kill stops the real process)") is FALSE: in the test it is the FAKE's `onKill`
callback that unblocks the fake loop, and in production `box.Kill` does nothing at
all. This comment misleads a future reader into believing the dead wire this task
fixes was already live.

**The fix:** `Supervisor.Run` derives `runCtx, cancelRun := context.WithCancel(ctx)`
and passes `runCtx` (not the raw `ctx`) into `InBoxLoop.RunInside` (task 155's new
parameter). The cancel arm gets the fix for free (`runCtx` is a child of `ctx`, so
`ctx.Done()` cascades automatically — no change needed there). The timeout arm's
`case <-timerC:` branch additionally calls `cancelRun()` (before or alongside
`killAndJoin`), so a context-aware in-flight executor (every concrete executor, after
task 155) now observes cancellation on a TIMEOUT the same way it already observes a
caller cancel.

`sandboxBox.Kill`/`Teardown` remain no-ops after this task — there is currently no
persistent, separately-killable box-level process for them to terminate (the
CLI-shaped executors invoke their subprocess directly via `exec.CommandContext`, not
through a wrapped containment-box process). What changes is that the REAL termination
mechanism (context cancellation reaching the executor) is now genuinely wired, and the
no-op status of the box-level hooks is accurately documented rather than silently
misrepresented by a false test comment.

**Reference:**
- `internal/supervisor/supervisor.go` (`Run`, `killAndJoin`, the `select` block)
- `internal/supervisor/cancel_test.go:41-43` (the false comment)
- `internal/runtime/run.go:1143-1149` (`sandboxBox.Kill`/`Teardown`)
- `internal/executor/ollama_native.go:112-113` (the stale TODO)
- Task 155 (the context-threading plumbing this task builds on)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-156-01 | `Supervisor.Run` derives a cancellable child context (`context.WithCancel(ctx)`) and passes it — not the raw caller `ctx` — into `InBoxLoop.RunInside`, so both the cancel arm and the timeout arm can independently cancel the SAME context the in-flight executor observes. | must have |
| REQ-156-02 | The wall-clock TIMEOUT arm calls the derived context's cancel function so a context-aware in-flight executor is cancelled on a timeout — proven via a bounded-time L5 assertion (termination close to the configured timeout, not after a long executor-side fallback). | must have |
| REQ-156-03 | The pre-existing cancel-arm behavior (task 116: `ErrRunCancelled`, `box.Kill`/`Teardown` ordering, partial-teardown-leak `errors.Join` surfacing) is unchanged in externally observable outcome. | must have |
| REQ-156-04 | The false comment at `internal/supervisor/cancel_test.go:41-42` is corrected to accurately describe the real termination mechanism (context cancellation, not `box.Kill`). | must have |
| REQ-156-05 | `sandboxBox.Kill`/`Teardown`'s doc comments in `internal/runtime/run.go` accurately state they are intentional no-ops and why, rather than implying real container-kill behavior. | must have |
| REQ-156-06 | `internal/executor/ollama_native.go`'s `// TODO: context should come from supervisor` comment is removed (resolved by tasks 155-156). | must have |
| REQ-156-07 | `internal/supervisor` and `tests/supervisor` suites continue to pass unchanged in externally observable behavior. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/156-supervisor-cancel-timeout-stops-executor-test-spec.md` exists (written first)
- [ ] Task 155 merged (`Executor.Run(ctx, ...)`/`InBoxLoop.RunInside(ctx, ...)` plumbing live)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-156-01] TC-156-01: `Supervisor.Run` derives a child context and passes it into `InBoxLoop.RunInside`; cancelling the parent cascades to the child.
- [ ] [REQ-156-02] TC-156-02/03: an L5 harness proves the wall-clock timeout terminates an in-flight context-aware executor within a bounded time close to the configured deadline, with no goroutine leak.
- [ ] [REQ-156-03] TC-156-04: the pre-existing task-116 cancel-arm tests pass with identical externally observable behavior.
- [ ] [REQ-156-04] TC-156-05: the false comment string is no longer present in `cancel_test.go`.
- [ ] [REQ-156-05] TC-156-06: `sandboxBox.Kill`/`Teardown` doc comments accurately state the no-op rationale.
- [ ] [REQ-156-06] TC-156-07: the stale TODO string is no longer present in `ollama_native.go`.
- [ ] [REQ-156-07] TC-156-08: `go test -race -count=1 ./internal/supervisor/... ./tests/supervisor/... ./internal/executor/...` passes in full; `make check` passes; `make fitness-supervisor-isolation` still PASS.

## Verification plan

- **Highest level achievable:** L5 — the production chain demonstrates the wall-clock
  timeout arm terminating an in-flight executor within a bounded time close to the
  configured deadline (not a long fallback), which is the load-bearing proof this
  finding is fixed. A live L6 subprocess run adds no additional confidence here (the
  mechanism is Go-level context propagation).
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/supervisor/... ./tests/supervisor/... ./internal/executor/...
  ```
  Expected: all packages `ok`.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/supervisor/... -run 'TestTC156'
  ```
  Expected: bounded-time timeout-triggered termination observed; no hang.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/behaviors.md` B-013 (wall-clock timeout) — add: "as of task 156, the
  timeout also cancels the run-scoped context passed to the executor, so a
  context-aware executor's subprocess is terminated on timeout, not only on a caller
  cancel."
- `docs/spec/behaviors.md` B-031 (cancellation) — cross-reference the same context
  now being shared between the cancel and timeout triggers.
- `docs/spec/interfaces.md` — note near the `Executor`/`InBoxLoop` entries that
  `sandboxBox.Kill`/`Teardown` are intentional no-ops; termination is via context
  cancellation (tasks 155-156), not a box-level kill.

## Out of scope

- Making `sandboxBox.Kill`/`Teardown` do real container-level work.
- Any change to router escalation/quota machinery (tasks 160-162).
- A live Claude/Codex/Ollama L6 run.

## Dependencies

- **Blocks on:** task 155 (the context-threading plumbing this task's timeout-arm fix
  builds directly on).
- **Blocks:** none. Tasks 160-162 (router wiring) touch the same
  `internal/runtime/run.go` file as task 155/156's `sandboxBox` comment update — land
  after 155/156 to avoid conflicts, per the review's explicit ordering note.
