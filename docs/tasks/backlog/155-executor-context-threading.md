# Task 155: thread `context.Context` through `supervisor.Executor` and `supervisor.InBoxLoop`

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Change `supervisor.Executor.Run` and `supervisor.InBoxLoop.RunInside` to accept a
`context.Context` as their first parameter, and thread the ONE context
`Supervisor.Run(ctx)` already receives all the way down to each concrete executor's
subprocess/HTTP call, replacing every internal `context.Background()` fallback on that
path. This is step (a) of the two-step fix for the dead-wire cancel/timeout finding
(see task 156 for step (b) — making the wall-clock timeout and `sandboxBox`/`killAndJoin`
wiring actually stop the running subprocess).

## Context

**Root cause (full-project review, verified 2026-07-02):** `supervisor.Executor`'s
interface (`internal/supervisor/supervisor.go:69-71`) is `Run(t Task) (Result, error)`
— no context. Every concrete executor already HAS a correctly-built,
context-cancellation-aware internal implementation (`RunContext`/`run(ctx, task)`,
which threads `ctx` into `exec.CommandContext` and — for the sandboxed backend —
`cmd.Cancel`, per `internal/sandbox/podman/run.go:121-130`'s SIGKILL pattern) but the
PUBLIC `Run` method every caller actually invokes discards that capability by calling
`context.Background()`:

- `ClaudeCLI.Run` (`internal/executor/claude_cli.go:203-205`)
- `CodexCLI.Run` (`internal/executor/codex_cli.go:67-69`)
- `GeminiCLI.Run` (`internal/executor/gemini_cli.go:67-68`)
- `AntigravityCLI.Run` (`internal/executor/antigravity_cli.go:63-64`)
- `OllamaNative.Run` (`internal/executor/ollama_native.go:111-113`, with a self-marked
  `// TODO: context should come from supervisor`)

Even if the interface accepted a context today, there is nowhere for it to come from:
`supervisor.InBoxLoop.RunInside(BoxHandle, Task, RunStreams) error`
(`internal/supervisor/supervisor.go:124-126`) also takes no context, and
`Supervisor.Run`'s `runLoop` goroutine calls `s.loop.RunInside(handle, s.task,
streams)` (`internal/supervisor/supervisor.go:397-411`) with no ctx argument at all —
even though `Supervisor.Run(ctx context.Context)` already receives and selects on a
real caller-supplied `ctx` for its cancel arm.

The net effect: a goal `cancel <goalID>` (ADR 054 §5) already derives and cancels a
real per-goal `context.Context`, and `Supervisor.Run`'s `case <-ctx.Done():` arm
already fires correctly — but nothing downstream of `runLoop` ever sees that
cancellation, because both interfaces on the path stop passing it forward. The
in-flight executor subprocess runs to completion regardless.

This task closes the PLUMBING gap only: after this task, a caller-cancelled `ctx`
(the cancel-arm case) will for the first time reach and cancel the in-flight
executor's subprocess. The wall-clock TIMEOUT arm (`case <-timerC:`) does **not**
gain this effect from this task alone — the local `timer.C` fires independently of
`ctx` and does not cancel it. Making the timeout arm ALSO cancel the threaded context
(via a supervisor-owned derived cancellable context), fixing the false "production
box.Kill stops the real process" comment in `internal/supervisor/cancel_test.go:42-43`,
and clarifying `sandboxBox.Kill`/`Teardown`'s no-op status are task 156's scope.

**Blast radius:** this task changes TWO interface signatures inside
`internal/supervisor` (`Executor`, `InBoxLoop`) and therefore touches every concrete
implementation and every test double across `internal/executor` (5 files),
`internal/loop`/`tests/loop` (2 methods gain a `ctx` parameter), `internal/runtime`
(the one production `InBoxLoop` implementation), and roughly a dozen test files that
implement either seam as a fake. This is wider than the project's usual "at most two
modules" guideline, but it is ONE coherent responsibility — thread a context through
an existing seam — not several unrelated changes. Every individual edit is mechanical
(add a parameter, forward it) except the five `context.Background()` replacements,
which are one-line changes each.

**Reference:**
- `internal/supervisor/supervisor.go` (`Executor`, `InBoxLoop` interfaces, `Run`, `runLoop`)
- `internal/executor/{claude_cli,codex_cli,gemini_cli,antigravity_cli,ollama_native}.go`
- `internal/loop/loop.go` (`Loop.RunOnce`), `internal/loop/retry_policy.go` (`RetryingLoop.RunOnce`)
- `internal/runtime/run.go` (`retryingInBoxLoop.RunInside`)
- `internal/sandbox/podman/run.go:121-130` (the correct `cmd.Cancel` pattern this task
  reuses rather than reinvents)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-155-01 | `supervisor.Executor`'s method becomes `Run(ctx context.Context, t Task) (Result, error)`. All five concrete executors implement it by forwarding the passed-in `ctx` into their existing `RunContext`/`run(ctx, task)` internal implementation — no executor builds its own `context.Background()` on this path anymore. | must have |
| REQ-155-02 | `supervisor.InBoxLoop`'s method becomes `RunInside(ctx context.Context, handle BoxHandle, t Task, streams RunStreams) error`. `Supervisor.Run`'s `runLoop` goroutine passes its own `ctx` parameter through to `RunInside`, not `context.Background()`. | must have |
| REQ-155-03 | `internal/loop.Loop.RunOnce` and `internal/loop.RetryingLoop.RunOnce` gain a `ctx context.Context` first parameter and pass that SAME ctx to every `Executor.Run` call they make, including every attempt inside `RetryingLoop`'s bounded retry loop. | must have |
| REQ-155-04 | `internal/runtime`'s `retryingInBoxLoop.RunInside` (the one production `InBoxLoop` implementation) uses its received `ctx` — not `context.Background()` — when constructing and driving the `agentloop.RetryingLoop`. | must have |
| REQ-155-05 | End-to-end: cancelling the `ctx` passed into `Supervisor.Run(ctx)` while a context-aware executor is mid-`Run` causes that executor to observe `ctx.Done()` and return promptly, proven through the REAL production chain (`Supervisor` → `retryingInBoxLoop` → `agentloop` → `Executor`), not a shortcut fake. | must have |
| REQ-155-06 | Every pre-existing test suite across the touched packages continues to pass after the signature change, with only mechanical call-site updates (an added `ctx` argument) — no behavior regression in branch selection, gate verdicts, or audit output. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/155-executor-context-threading-test-spec.md` exists (written first)
- [x] Task 116 merged (`Supervisor.Run(ctx context.Context)` and its cancel arm already exist)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-155-01] TC-155-01: each of the five concrete executors' `Run(ctx, task)` uses the passed-in `ctx` (not a fresh `context.Background()`), proven via a marker value round-tripped to the subprocess/HTTP-call boundary.
- [ ] [REQ-155-01] TC-155-02: a cancelled `ctx` aborts an in-flight CLI executor's real subprocess promptly via `Run(ctx, ...)` (not just the pre-existing `RunContext`), matching the existing `cmd.Cancel` termination behavior.
- [ ] [REQ-155-02] TC-155-03: `Supervisor.Run(ctx)` passes its own `ctx` (not `context.Background()`) into `InBoxLoop.RunInside`.
- [ ] [REQ-155-03] TC-155-04/05: `Loop.RunOnce(ctx)` and `RetryingLoop.RunOnce(ctx)` forward the same `ctx` to `Executor.Run` on every call, including every retry attempt.
- [ ] [REQ-155-04] TC-155-06: `retryingInBoxLoop.RunInside` forwards its received `ctx`, not `context.Background()`.
- [ ] [REQ-155-05] TC-155-07: an L5 harness spanning the full production chain proves a cancelled `Supervisor.Run(ctx)` context reaches and stops an in-flight fake executor within a bounded time.
- [ ] [REQ-155-06] TC-155-08: `go test -race -count=1 ./internal/executor/... ./internal/loop/... ./tests/loop/... ./internal/supervisor/... ./tests/supervisor/... ./internal/runtime/... ./internal/router/...` passes in full; `make check` passes; `make fitness-supervisor-isolation` and `make fitness-orchestrator-no-executor` both still PASS.

## Verification plan

- **Highest level achievable:** L5 — a fake-executor harness spanning the full
  production call chain (`Supervisor.Run` → `retryingInBoxLoop` → `agentloop` →
  `Executor.Run`) proves cancellation propagation is live end-to-end. No live
  Claude/Codex/Ollama subprocess or containment box is required for this task's REQs.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/executor/... ./internal/loop/... ./tests/loop/...
  ```
  Expected: all packages `ok`.
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/supervisor/... -run TestTC155_07
  ```
  Expected: bounded-time cancellation observed; `ErrRunCancelled` returned.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md` line ~216-217 — `Executor` interface signature updates to
  `Run(ctx context.Context, t Task) (Result, error)`.
- `docs/spec/interfaces.md` — the `InBoxLoop` interface entry (grep for
  `RunInside(BoxHandle`) updates to the new signature.
- `docs/spec/behaviors.md` B-013/B-031 — add one sentence noting that as of task 155,
  a caller-cancelled context now reaches the in-flight executor subprocess (the
  wall-clock timeout arm gaining the same effect is task 156 — do not overclaim it
  here).

## Out of scope

- Making the wall-clock TIMEOUT arm cancel the threaded context — task 156.
- `sandboxBox.Kill`/`Teardown`'s no-op status and the false comment in
  `internal/supervisor/cancel_test.go:42-43` — task 156.
- Any change to router escalation/quota machinery (tasks 160-162).
- A live Claude/Codex/Ollama L6 run.

## Dependencies

- **Blocks on:** task 116 (already merged — provides `Supervisor.Run(ctx)` and the
  cancel arm this task threads context through).
- **Blocks:** task 156 (the timeout-arm + sandboxBox/false-comment fix builds directly
  on this task's plumbing); tasks 160-162 (router escalation/quota wiring touch the
  same `internal/runtime/run.go` file and should land after this task to avoid merge
  conflicts, per the review's explicit ordering note).
