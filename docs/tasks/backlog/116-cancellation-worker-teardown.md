# Task 116: Cancellation + worker teardown (the `runtime.Run` ctx thread)

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Make `cancel <goalID>` stop a goal and tear down its in-flight sub-goal workers/sandboxes.
Thread a `context.Context` through `runtime.Run` → `Supervisor.Run` → the run-loop `select`,
add a `case <-ctx.Done():` arm beside the existing wall-clock `case <-timer.C:` arm that
reuses the **same** `box.Kill`/`Teardown` path, derive a per-goal cancel context in the
control plane, and route `cancel` to it (state → `Cancelled`, plan consumed from the
PlanStore, worker-semaphore permits released, partial-teardown failures `errors.Join`'d +
reported as leaks). **This is the largest task** in the ADR 054 decomposition because of the
signature change and its caller fan-out — it must not be under-sized.

## Context

ADR 054 (the authoritative design) §5 implements **product decision 2** (already made — do
not re-litigate): cancellation is in scope. Task 113 delivers the `MsgCancel` to the goal's
command mailbox; this task is the teardown.

### The gating finding — `runtime.Run` has no `context.Context` (ADR 054 §Context/§5)

`runtime.Run(config Config, stdout io.Writer) error` (`internal/runtime/run.go` ~L535) and
`(*Supervisor).Run() (err error)` (`internal/supervisor/supervisor.go` ~L234) take **no ctx**
today. A worker is stopped only by the supervisor's own **wall-clock timeout**
(`WithRunTimeout` → `box.Kill(handle)` on timer fire, supervisor.go ~L301–324). Cancelling an
in-flight worker mid-run is therefore **not** a free `ctx.Done()` plumb-through: thread a
`context.Context` through `runtime.Run` → `Supervisor.Run` → the run-loop `select`, adding a
`case <-ctx.Done():` arm **beside** the existing `case <-timer.C:` arm so cancel triggers the
**same** `box.Kill`/`Teardown` path the timeout already uses. **No new teardown mechanism is
invented** — cancel becomes a second trigger for the kill path that already exists.

### The signature change ripples to callers (ADR 054 §Consequences — Modules touched calls it out)

Adding `ctx` to `runtime.Run`/`Supervisor.Run` touches every caller: **`RunFromEnv`**,
**`defaultDispatch`**, **`newTransportDispatch`**, and **tests**. The `DispatchFunc` type
(`func(ctx, SubGoal, runtime.Config) error`) **already carries a ctx** — today
`defaultDispatch`/`newTransportDispatch` *ignore* it past the transport step; the fix is to
pass it into `runtime.Run`. This is the single largest mechanical cost of the ADR.

### Per-goal cancel context + routing (ADR 054 §3/§5)

Each goal actor runs under a `context.Context` from a per-goal `context.WithCancel`; the
`CancelFunc` lives in the registry (the field task 112 reserved). A `cancel` message looks up
the goalID, calls `CancelFunc`, sets state `Cancelled`, and **removes the plan from the
PlanStore** so a late approval cannot resurrect it. Cancelling G cancels only G's workers
(G's derived ctx); siblings' contexts are independent — no blast radius.

### Fail-safe on partial teardown + permit release (ADR 054 §5 / §6 race (c))

If `box.Kill`/`Teardown` partially fails: `errors.Join` the errors, log loudly, mark the
sub-goal **`Failed`** (not silently `Cancelled`), and surface the teardown error in the
goal's final report as a **leak requiring operator attention** — never swallowed; the
wall-clock kill remains the backstop. The worker-semaphore permit (task 112) **must be
released on the cancel/teardown return path** (deferred release), or the cap leaks permits.

### Race surface (ADR 054 §6 race (d))

A cancel racing a `Resume`-approve must consume the plan from the store under the **same
delete path** so a concurrent approval cannot double-dispatch.

## Requirements

| Req ID      | Description                                                                                                                  | Priority   |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-116-01  | `runtime.Run` + `Supervisor.Run` take a `context.Context`; all callers (`RunFromEnv`, `defaultDispatch`, `newTransportDispatch`, tests) updated; compiles + existing suites green | must have |
| REQ-116-02  | The run-loop `select` has a `case <-ctx.Done():` arm beside the wall-clock arm; on ctx-cancel it calls the **same** `box.Kill`/`Teardown` path | must have |
| REQ-116-03  | `cancel <goalID>` looks up the per-goal `CancelFunc`, cancels only G's ctx (siblings unaffected), sets `Cancelled`, removes the plan from the PlanStore | must have |
| REQ-116-04  | The worker-semaphore permit is **released** on the cancel/teardown return path (no permit leak)                              | must have  |
| REQ-116-05  | Partial-teardown failures `errors.Join` → sub-goal `Failed` (not `Cancelled`), surfaced in the goal report as a leak (not swallowed); wall-clock backstop intact | must have |
| REQ-116-06  | A cancel racing `Resume`-approve consumes the plan under the same delete path → no double-dispatch                          | must have  |

## Readiness gate

- [ ] Task 112 merged (the registry + the worker semaphore + the reserved `CancelFunc`/`Cancelled` slots)
- [ ] Task 113 merged (the `MsgCancel` mailbox delivery this consumes)
- [x] Task 017/018 merged (`Supervisor.Run` run-loop + `WithRunTimeout` wall-clock kill + `box.Kill`/`Teardown`)
- [x] Task 086 merged (`dispatchPlan` fan-out + `DispatchFunc` ctx-carrying signature)
- [x] Task 099 merged (`defaultDispatch`/`newTransportDispatch`/`RunFromEnv` callers)
- [x] ADR 054 §5 read; product decision 2 confirmed (cancellation in scope)

## Acceptance criteria

- [ ] [REQ-116-01] TC-116-01: `runtime.Run(ctx,…)`/`Supervisor.Run(ctx)` compile; `RunFromEnv`/`defaultDispatch`/`newTransportDispatch` forward the ctx; existing suites green
- [ ] [REQ-116-02] TC-116-02: far-future timer + ctx cancel → run-loop selects `ctx.Done()`; stub box records `Kill` then `Teardown` once each; `Run` returns
- [ ] [REQ-116-03] TC-116-03: `cancel G` (G,H concurrent) → G's box torn down, H untouched; `Get("G").State==Cancelled`; `PlanStore.Get("G")` empty; later `Resume(G)` does not dispatch
- [ ] [REQ-116-04] TC-116-04: `MAX_WORKERS=1`, cancel G holding the permit → H acquires + dispatches within timeout; `Acquire(MAX_WORKERS)` succeeds post-drain
- [ ] [REQ-116-05] TC-116-05: `Teardown` error → `errors.Join` + loud log; sub-goal `Failed` (not `Cancelled`); report names the leak; wall-clock timer not disabled
- [ ] [REQ-116-06] TC-116-06: concurrent cancel + `Resume`-approve, both orderings → dispatch spy called **at most once** for G; `-race` clean on the consume + state transition

## Verification plan

- **Highest level achievable: L6** — cancel an in-flight goal and observe the box torn down on
  the live binary (plus a `-race` build). L2 (select arm, cancel routing, permit-release
  accounting, `errors.Join` leak path, cancel/approve race) is the CI ceiling.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/supervisor/... ./internal/cli/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make fitness-supervisor-isolation
  make fitness-orchestrator-no-executor
  make check
  ```
  Expected: `PASS …`; `All checks passed.`
- **L6 (operator-run, dev host):** run `agent-builder orchestrate` with a goal whose sub-goal
  runs long enough to cancel mid-flight (real box via `EXEC_BOX_RUNTIME=runc`); send
  `cancel <goalID>` over stdin; observe `box.Kill`/`Teardown` in the logs, the goal state →
  `Cancelled`, and the permit freed for a subsequent goal. Record the teardown log lines in
  the verify commit.

## Modules touched

- `internal/runtime` (`Run` gains a leading `ctx context.Context`; passes it to
  `Supervisor.Run`).
- `internal/supervisor` (`Run` gains `ctx`; the run-loop `select` gains the
  `case <-ctx.Done():` arm calling the existing `box.Kill`/`Teardown`; partial-teardown
  `errors.Join` convention).
- `internal/cli` (per-goal `context.WithCancel` + `CancelFunc` in the registry; the `cancel`
  handler — cancel ctx, set `Cancelled`, consume the plan from the PlanStore, release the
  permit, surface teardown leaks in the report; thread the ctx through
  `defaultDispatch`/`newTransportDispatch`/`RunFromEnv`).
- `docs/spec/interfaces.md` (the `runtime.Run`/`Supervisor.Run` signature change — `ctx` is
  now part of the worker-seam contract).
- `docs/spec/behaviors.md` + `docs/architecture/diagrams.md` (the cancellation path: a second
  trigger into the existing kill/teardown flow — a diagrammed runtime-flow change).

**Sizing note (operator review requested):** this task **legitimately spans three code
modules** (`internal/runtime` + `internal/supervisor` + `internal/cli`) — one more than the
at-most-two-modules rule — **by design**, because the ctx thread must reach the run-loop while
the cancel routing lives in the control plane: it is **one coherent responsibility (add
cancellation)**. ADR 054 §Recommended-decomposition sanctions this and offers a tighter cut if
preferred: split the `runtime`/`supervisor` ctx plumb (**116a**) from the `cli` cancel routing
(**116b**), with **116a landing first**. Flagging for the operator to decide whether to keep
116 whole or split into 116a/116b.

## Out of scope

- The `MsgCancel` **routing** to the mailbox + unknown-goalID graceful report — task 113.
- The registry **reserving** the `CancelFunc`/`Cancelled` slots — task 112 (this task
  populates + drives them).
- Inventing a new teardown mechanism — cancel reuses the existing `box.Kill`/`Teardown` path.
- Making `router.Select` / the planner context-cancellable — the ctx thread is through the
  **worker** seam only.
- Telegram-formatted cancel acks — task 117.

## Dependencies

- **Task 112 — HARD dependency** (the registry, the worker semaphore to release, the reserved
  `CancelFunc`/`Cancelled` slots).
- **Task 113 — HARD dependency** (the `MsgCancel` mailbox delivery).
- Task 017/018 (`Supervisor.Run` run-loop + wall-clock kill + `box.Kill`/`Teardown`), task 086
  (`DispatchFunc` ctx-carrying signature + fan-out), task 099 (the callers) — merged.
- ADR 054 §5 — the authoritative design.
- **Mutually independent of tasks 114 and 115** — different control-loop handler + different
  lower seam; may run in parallel on a separate branch once 113 merges.
