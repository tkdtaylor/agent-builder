# Test spec — Task 116: Cancellation + worker teardown (the `runtime.Run` ctx thread)

**Linked task:** `docs/tasks/backlog/116-cancellation-worker-teardown.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §5 (cancellation + teardown; thread `context.Context` through
`runtime.Run`/`Supervisor.Run`; the `case <-ctx.Done():` arm reusing `box.Kill`/`Teardown`;
per-goal cancel context; permit release; `errors.Join` partial-teardown).

## Context

ADR 054 §5 implements **product decision 2** (already made — do not re-litigate):
cancellation is in scope. A `cancel <goalID>` message stops a goal and tears down its
in-flight sub-goal workers/sandboxes. Task 113 delivers the `MsgCancel` to the goal's command
mailbox; this task is the teardown.

### The gating finding — `runtime.Run` has no `context.Context` (ADR 054 §Context/§5)

`runtime.Run(config Config, stdout io.Writer) error` (`internal/runtime/run.go` ~L535) and
`(*Supervisor).Run() (err error)` (`internal/supervisor/supervisor.go` ~L234) take **no
ctx** today. A worker is stopped only by the supervisor's own **wall-clock timeout**
(`WithRunTimeout` → `box.Kill(handle)` on timer fire, supervisor.go ~L301–324). Cancelling an
in-flight worker mid-run is therefore **not** a free `ctx.Done()` plumb-through: it requires
threading a `context.Context` through `runtime.Run` → `Supervisor.Run` → the run-loop
`select`, adding a `case <-ctx.Done():` arm **beside** the existing `case <-timer.C:` arm, so
cancel triggers the **same** `box.Kill`/`Teardown` path the timeout already uses. No new
teardown mechanism is invented.

### Signature change ripples to callers (ADR 054 §Consequences — call it out)

Adding `ctx` to `runtime.Run`/`Supervisor.Run` touches every caller: `RunFromEnv`,
`defaultDispatch`, `newTransportDispatch`, and tests. The `DispatchFunc` type
(`func(ctx, SubGoal, runtime.Config) error`) **already carries a ctx** — today
`defaultDispatch`/`newTransportDispatch` *ignore* it past the transport step; the fix is to
pass it into `runtime.Run`. This is the single largest mechanical cost of the ADR and the
reason this task is the largest — it must not be under-sized.

### Per-goal cancel context + routing (ADR 054 §3/§5)

Each goal actor runs under a `context.Context` derived from a per-goal `context.WithCancel`;
the `CancelFunc` lives in the registry. A `cancel` message looks up the goalID, calls
`CancelFunc`, sets state `Cancelled`, and **removes the plan from the PlanStore** (so a late
approval cannot resurrect it — ADR 054 §6 race (d): consume the plan under the same delete
path so a concurrent approval cannot double-dispatch). Cancelling G cancels only G's workers
(G's derived ctx); siblings' contexts are independent (no blast radius).

### Fail-safe on partial teardown + permit release (ADR 054 §5/§6 race (c))

If `box.Kill`/`Teardown` partially fails: `errors.Join` the errors, log loudly, mark the
sub-goal **`Failed`** (not silently `Cancelled`), and surface the teardown error in the
goal's final report as a **leak requiring operator attention** — never swallowed; the
wall-clock kill remains the backstop. The worker semaphore permit (task 112) **must be
released on the cancel/teardown return path** (deferred release), or the cap leaks permits.

## Requirements coverage

| Req ID      | Description                                                                                                                  | Test cases             |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-116-01  | `runtime.Run` and `Supervisor.Run` take a `context.Context`; all callers (`RunFromEnv`, `defaultDispatch`, `newTransportDispatch`, tests) updated; compiles + existing suites green | TC-116-01 |
| REQ-116-02  | The run-loop `select` has a `case <-ctx.Done():` arm beside the wall-clock arm; on ctx-cancel it calls the **same** `box.Kill`/`Teardown` path | TC-116-02 |
| REQ-116-03  | A `cancel <goalID>` looks up the per-goal `CancelFunc`, cancels only G's ctx (siblings unaffected), sets state `Cancelled`, removes the plan from the PlanStore | TC-116-03 |
| REQ-116-04  | The worker semaphore permit is **released** on the cancel/teardown return path (no permit leak)                              | TC-116-04              |
| REQ-116-05  | Partial-teardown failures `errors.Join` → sub-goal marked `Failed` (not `Cancelled`), error surfaced in the goal report as a leak (not swallowed); wall-clock backstop intact | TC-116-05 |
| REQ-116-06  | A cancel racing `Resume`-approve consumes the plan under the same delete path → no double-dispatch                          | TC-116-06              |

---

## Test cases

### TC-116-01 — Signature change threads through all callers; suites green (L2)

- **Requirement:** REQ-116-01
- **Level:** L2 (compile-time + existing suites)

**Input:** Build the module after `runtime.Run(ctx, cfg, stdout)` and `Supervisor.Run(ctx)`.

**Expected output (assertions):**
- The package compiles with `ctx context.Context` as the first parameter of both
  `runtime.Run` and `Supervisor.Run`.
- `RunFromEnv`, `defaultDispatch`, `newTransportDispatch` each pass a ctx through to
  `runtime.Run` (the `DispatchFunc`'s existing ctx is forwarded, not dropped).
- The existing supervisor/runtime/cli test suites pass (callers updated; no regression). A
  `passes context.Background()` baseline call from a non-cancelling caller still behaves as
  before.

---

### TC-116-02 — `ctx.Done()` arm tears down the box via `box.Kill`/`Teardown` (L2)

- **Requirement:** REQ-116-02
- **Level:** L2 (unit test with a **stub/fault box** recording `Kill`/`Teardown`)

**Input:** Run `Supervisor.Run(ctx)` with a stub box whose work blocks until cancelled, a
`ctx` from `context.WithCancel`, and a wall-clock timeout set far in the future (so the timer
arm cannot fire). Cancel the ctx mid-run.

**Expected output (assertions):**
- The run-loop selects the `case <-ctx.Done():` arm (not the timer arm — timer is far out).
- `box.Kill(handle)` is called exactly once and `box.Teardown(handle)` is called exactly once
  — the **same** path the wall-clock timeout uses (assert the stub box recorded both, in that
  order, on the cancel trigger).
- `Run` returns after teardown (does not hang past cancellation).

---

### TC-116-03 — `cancel` cancels only G's ctx, sets `Cancelled`, removes the plan (L2)

- **Requirement:** REQ-116-03
- **Level:** L2 (unit test, two concurrent goals)

**Input:** Two goals `G` and `H` running concurrently, each with an in-flight sub-goal held
at a latch, each registered with its own `CancelFunc` and a plan in the PlanStore. Deliver
`MsgCancel{GoalID: "G"}`.

**Expected output (assertions):**
- `G`'s derived ctx is cancelled (its worker's `ctx.Done()` arm fires → box torn down);
  `H`'s ctx is **not** cancelled (its worker remains held at the latch, untouched — no blast
  radius).
- `registry.Get("G").State == Cancelled`.
- `PlanStore.Get("G")` returns no plan (the plan was removed) — a subsequent
  `Resume(Approval{GoalID:"G", allow})` finds nothing to dispatch and does **not** dispatch.

---

### TC-116-04 — Permit released on the cancel/teardown path (no leak) (L2)

- **Requirement:** REQ-116-04
- **Level:** L2 (unit test + `-race`)

**Input:** `MAX_WORKERS=1`. Goal `G` holds the single permit in an in-flight sub-goal worker.
Cancel `G`. Then submit goal `H` needing a worker permit.

**Expected output (assertions):**
- After `G`'s cancel/teardown returns, the semaphore is available again: `H`'s sub-goal
  worker **acquires the permit and dispatches** (it would block forever if the permit
  leaked). Bounded-wait assertion: `H` reaches `Dispatching` within the timeout.
- A direct accounting check: `Acquire(MAX_WORKERS)` succeeds after the cancel drains (the
  permit `G` held was released on the cancel return path, not only on a normal completion).

---

### TC-116-05 — Partial teardown → `errors.Join`, sub-goal `Failed`, leak surfaced (L2)

- **Requirement:** REQ-116-05
- **Level:** L2 (unit test with a fault box)

**Input:** Cancel a goal whose stub box returns an **error from `Teardown`** (a partial-
teardown failure — the box cannot be confirmed torn down).

**Expected output (assertions):**
- The teardown error is `errors.Join`'d with any `Kill` error and **logged loudly** (not
  swallowed).
- The sub-goal is marked **`Failed`** in the registry/outcome — **not** silently `Cancelled`.
- The goal's final report (Reporter) contains the teardown error described as a **leak
  requiring operator attention** (substring assertion: the box/handle identity + a
  leak/attention phrase).
- The wall-clock timeout remains configured as the backstop (a cancel that fails to tear down
  still has the timer as a second trigger — assert the timer was not disabled by the cancel
  path).

---

### TC-116-06 — Cancel racing `Resume`-approve → no double-dispatch (L2)

- **Requirement:** REQ-116-06
- **Level:** L2 (unit test, `-race`, deterministic interleave via a latch)

**Input:** Goal `G` at `AwaitingApproval` with a plan in the PlanStore. Concurrently deliver
`MsgCancel{GoalID:"G"}` and `Resume(Approval{GoalID:"G", allow})`, with the plan-store
delete/consume path latched so the test can force both orderings.

**Expected output (assertions):**
- In **both** orderings the plan is consumed from the store under the **same delete path**
  exactly once — the dispatch spy is called for `G`'s sub-goals **at most once total** across
  the cancel+approve pair (never twice). If cancel wins, dispatch count is 0; if approve wins
  the consume, cancel finds no plan and tears down whatever is in flight — but there is never
  a double-dispatch.
- `-race` clean on the PlanStore consume + registry state transition.

---

## Verification plan

- **Highest level achievable: L6** — cancel an in-flight goal and observe the box torn down on
  the live binary (plus a `-race` build). L2 (the select arm, the per-goal cancel routing, the
  permit-release accounting, the `errors.Join` leak path, the cancel/approve race) is the CI
  ceiling.
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
  takes long enough to cancel mid-flight (a real box via `EXEC_BOX_RUNTIME=runc`); send
  `cancel <goalID>` over stdin; observe the box `Kill`/`Teardown` in the logs, the goal state
  → `Cancelled`, and the permit freed for a subsequent goal. Record the teardown log lines in
  the verify commit.

## Out of scope

- The `MsgCancel` **routing** to the mailbox + unknown-goalID graceful report — task 113.
- The registry **reserving** the `CancelFunc` field — task 112 (this task populates + uses it).
- A new teardown mechanism — none is invented; cancel reuses the existing `box.Kill`/`Teardown`
  path the wall-clock timeout already drives.
- Making `router.Select` or the planner context-cancellable — out of scope (the ctx thread is
  through the **worker** seam; selection cancellability is a separate concern).
- Telegram-formatted cancel acks — task 117.

## Sizing note (for the planner)

ADR 054 §Recommended-decomposition flags this as the one task that **legitimately spans three
modules** (`internal/runtime` + `internal/supervisor` + `internal/cli`) because the ctx thread
must reach the run-loop and the cancel routing lives in the control plane — it is **one
coherent responsibility (add cancellation)**. If a tighter cut is wanted, split the seam
change (the `runtime`/`supervisor` ctx plumb → **116a**) from the control-plane cancel routing
(`cli` → **116b**), but **the ctx plumb (116a) must land first**. As written this is a single
task that exceeds the at-most-two-modules rule by one module **by design** — flag for operator
review.
