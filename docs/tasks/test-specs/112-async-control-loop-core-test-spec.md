# Test spec — Task 112: Async control-loop core + actor-per-goal + status registry

**Linked task:** `docs/tasks/backlog/112-async-control-loop-core.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §1/§3 (async control plane: non-blocking control loop,
actor-per-goal, fleet-wide worker semaphore, goal-admission cap, live status registry).

## Context

ADR 054 §1 replaces the serial `runGoalIntakeLoop` (`for { Next(); Handle() }`) with a
**non-blocking control loop** that spawns one **goal-actor goroutine** per `new-goal` and
tracks each goal in a new mutex-guarded **status registry**. This task carries the
concurrency skeleton but still reads **goals only** from the existing
`supervisor.GoalSource` — the typed message protocol lands in task 113. It is shippable on
its own: it must prove that M top-level goals run concurrently while the fleet-wide worker
semaphore (`AGENT_BUILDER_MAX_WORKERS`) caps total in-flight sub-goal workers and a
goal-admission cap (`AGENT_BUILDER_MAX_GOALS`) bounds live goal actors.

### Grounded current state (ADR 054 §Context, verified against code)

- The fan-out across **sub-goals** already exists (task 086, `dispatchPlan` ~L389): one
  goroutine per sub-goal joined by a `sync.WaitGroup`, outcomes written into a pre-sized
  slice at the sub-goal index. This task adds fan-out across **top-level goals** and a
  semaphore *inside* the existing per-sub-goal goroutine — it does NOT reinvent the
  sub-goal fan-out.
- The audit sink (`BlockSink.Append`) is already mutex-guarded; the hash chain stays
  single-writer-correct across M goals × N workers with no sink change (ADR 054 §1). This
  invariant is now load-bearing across goals — assert it under concurrency (TC-112-06).
- Plan state is already goalID-keyed and concurrency-safe (`MemoryPlanStore`); the new
  registry **mirrors** that locking discipline and is a **projection only** — never the
  source of control-flow truth.

### Semaphore placement (load-bearing — ADR 054 §1)

The worker semaphore is acquired **inside `dispatchPlan`'s per-sub-goal goroutine**, with
`Acquire(1)` before `o.dispatch(...)` and `Release(1)` after (deferred), **not** at the
goal-actor level. This is what makes the bound *total live workers across all goals* rather
than per-goal. The goal-admission cap (`MAX_GOALS`) is a separate, looser bound enforced at
the control loop: excess `new-goal`s park with `Queued` status until a slot frees.

### Registry is a projection, never the control-flow source of truth (ADR 054 §3)

The goal actor transitions its own goalID's state at each lifecycle edge
(intake→`Planning`, `require_approval`→`AwaitingApproval`, allow→`Dispatching`, sub-goal
start/finish→`SubGoals[i]`, terminal→`Done`/`Failed`). A registry **write failure must
never halt a goal** — the PlanStore stays the source of truth. The `Cancelled` state and
the per-goal command mailbox / `CancelFunc` arrive in later tasks (113/116); this task
defines the registry type with room for them but only drives the states it owns.

## Requirements coverage

| Req ID      | Description                                                                                                                          | Test cases             |
|-------------|------------------------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-112-01  | The control loop is **non-blocking**: reading the next goal and processing a goal are decoupled; a goal in `Dispatching` does not stall intake of the next | TC-112-01              |
| REQ-112-02  | A `new-goal` spawns a **goal-actor goroutine** owning that goal's lifecycle via `Orchestrator.Handle`; M goals run concurrently     | TC-112-02              |
| REQ-112-03  | Fleet-wide `AGENT_BUILDER_MAX_WORKERS` semaphore acquired **inside** the per-sub-goal goroutine caps total live workers at the bound | TC-112-03              |
| REQ-112-04  | Goal-admission cap `AGENT_BUILDER_MAX_GOALS` bounds live goal actors; excess goals park with `Queued` state until a slot frees      | TC-112-04              |
| REQ-112-05  | The status registry is goalID-keyed, mutex-guarded, a **projection only**; a registry write failure never halts a goal; states transition at each lifecycle edge | TC-112-05              |
| REQ-112-06  | Audit hash chain stays valid and `verify`-clean under M goals × N workers concurrent append (single mutex-guarded `Append`)         | TC-112-06              |
| REQ-112-07  | Semaphore permits are balanced on every path (acquire→release) — no permit leak after all goals drain; `-race` clean                | TC-112-07              |

---

## Test cases

### TC-112-01 — Control loop is non-blocking: status read returns while a goal runs (L2)

- **Requirement:** REQ-112-01
- **Level:** L2 (unit test with a blocking stub dispatch + a `-race` run)

**Input:** Construct the control plane with a stub `GoalSource` that yields two goals
(`goal-A`, `goal-B`) then `ok=false`. Wire a stub dispatch that **blocks on a release
channel** until the test signals it. Submit `goal-A`; while its sub-goal dispatch is blocked
(actor in `Dispatching`), have the test read the registry for `goal-B`'s state and submit
`goal-B`.

**Expected output (assertions):**
- The control loop reads and starts `goal-B` while `goal-A` is still blocked in dispatch —
  i.e. `registry.Get("goal-B").State` advances past `Queued` (reaches at least `Planning`)
  **before** the test releases `goal-A`'s blocked dispatch. (Proves intake is not serialized
  behind processing.)
- A registry read (`registry.Get("goal-A")`) returns **without blocking** while `goal-A`'s
  dispatch is held — the read completes in the test with `State == Dispatching`.
- After releasing the blocked dispatch and draining, both goals reach a terminal state.

---

### TC-112-02 — Actor-per-goal: two goals reach `Dispatching` concurrently (L2)

- **Requirement:** REQ-112-02
- **Level:** L2 (unit test; `MAX_WORKERS` set high enough to admit both)

**Input:** `MAX_WORKERS=4`, `MAX_GOALS=8`. Stub `GoalSource` yields `goal-A` and `goal-B`,
each planning to a single sub-goal. Stub dispatch blocks on a per-goal latch the test
controls. Stub policy returns `allow`.

**Expected output (assertions):**
- Both `registry.Get("goal-A").State` and `registry.Get("goal-B").State` are observed
  `== Dispatching` **at the same time** (the test waits, with a bounded timeout, for both to
  be `Dispatching` before releasing either latch — confirming genuine overlap, not
  sequential).
- Each goal's actor calls `Orchestrator.Handle` (spy confirms exactly one `Handle` per
  goalID).
- After releasing both latches and draining, both reach `Done`; the control loop returns
  once the source is exhausted and all actors join.

---

### TC-112-03 — Worker semaphore caps total live workers at `MAX_WORKERS` (L2)

- **Requirement:** REQ-112-03
- **Level:** L2 (unit test; the semaphore is the bound under test)

**Input:** `MAX_WORKERS=2`, `MAX_GOALS=8`. Two goals, each planning **two** sub-goals (4
sub-goals total). Stub dispatch increments a shared atomic `live` counter on entry,
**blocks** on a shared release channel, decrements on exit; it records the maximum observed
`live` value.

**Expected output (assertions):**
- The recorded **maximum concurrent `live` workers is exactly 2**, never 3 or 4 — even
  though 4 sub-goals are eligible and 2 goal actors are running. (The bound is fleet-wide,
  enforced inside the per-sub-goal goroutine, not per-goal.)
- After the test releases workers in batches, all 4 sub-goals complete and both goals reach
  `Done`.
- A control case with `MAX_WORKERS=4` and the same input observes max concurrent
  `live == 4` (confirming the cap, not an unrelated serialization, is what bounds it).

---

### TC-112-04 — Goal-admission cap parks excess goals as `Queued` (L2)

- **Requirement:** REQ-112-04
- **Level:** L2 (unit test)

**Input:** `MAX_GOALS=1`, `MAX_WORKERS=4`. Stub `GoalSource` yields `goal-A` then `goal-B`.
`goal-A`'s actor is held in `Dispatching` by a latch.

**Expected output (assertions):**
- While `goal-A` occupies the single admission slot, `registry.Get("goal-B").State ==
  Queued` (it is registered but parked — not yet `Planning`).
- When `goal-A`'s latch is released and it reaches a terminal state (slot frees), `goal-B`
  advances out of `Queued` (reaches at least `Planning`) and ultimately `Done`.
- No more than `MAX_GOALS` actors are ever simultaneously in a non-`Queued`, non-terminal
  state (the test samples the registry while both are live and asserts the count of
  `{Planning, AwaitingApproval, Dispatching}` actors ≤ 1).

---

### TC-112-05 — Registry is a mutex-guarded projection; states transition at each edge; write failure never halts a goal (L2)

- **Requirement:** REQ-112-05
- **Level:** L2 (unit test)

**Input (state transitions):** One goal through a full `allow` path with one sub-goal. Stub
policy returns `allow`; stub dispatch succeeds.

**Expected output (state transitions):**
- The registry records the ordered transitions for the goalID: `Queued` (or `Planning` if
  admitted immediately) → `Planning` → `Dispatching` → `Done`. (If the path includes a
  `require_approval` pause it also records `AwaitingApproval` — but that gate path is
  exercised in task 115; here a straight-`allow` path is sufficient.)
- `SubGoals[0]` records the sub-goal moving `running` → `done` (written from inside the
  task-086 dispatch goroutine).

**Input (projection / write-failure isolation):** Inject a registry whose state-write
method is a no-op (or errors internally and swallows). Run the same goal.

**Expected output (projection isolation):**
- The goal still reaches a terminal outcome via the PlanStore-driven `Handle`/dispatch path
  (the dispatch spy is still called) — i.e. **the goal completes even though the registry
  recorded nothing**. This proves the registry is a projection and never gates control flow.

---

### TC-112-06 — Audit chain stays valid under M goals × N workers (L2/L3)

- **Requirement:** REQ-112-06
- **Level:** L2 (concurrent append) + L3 (`audit verify` over the produced chain)

**Input:** 3 goals × 2 sub-goals each, all dispatched concurrently (`MAX_WORKERS=6`), each
sub-goal emitting the normal `goal-intake`/`plan-decided`/`spawn-decided`/`completion`
fleet events through the shared mutex-guarded `audit.Sink`.

**Expected output (assertions):**
- After all goals drain, the audit chain `verify` (the existing `internal/audit` verify
  path) reports the chain **valid** — no broken hash links despite interleaved appends from
  many goroutines.
- The total number of appended events equals the expected count (no lost/dropped events
  under contention).
- Run under `-race` with no data-race report on the sink.

---

### TC-112-07 — Permits balanced; no leak after drain; `-race` clean (L2)

- **Requirement:** REQ-112-07
- **Level:** L2 (unit test + `-race`)

**Input:** `MAX_WORKERS=2`, several goals with mixed sub-goal counts; a fraction of
dispatches return an **error** (best-effort completion path) and a fraction succeed.

**Expected output (assertions):**
- After every goal drains, the semaphore is **fully available** again: a test
  `TryAcquire(MAX_WORKERS)` (or `Acquire(MAX_WORKERS)` with a short timeout) succeeds,
  proving all permits were released even on the error path (`Release` is deferred, not gated
  on success).
- The whole suite runs `-race` clean.

---

## Verification plan

- **Highest level achievable: L6** — run `agent-builder orchestrate` on the live binary with
  several env/stdin goals (`AGENT_BUILDER_MAX_WORKERS` set low) and observe concurrent
  dispatch under the cap, plus a `-race` build. L2 (the concurrency unit tests above) + L3
  (`audit verify` + `make check`) are the CI-automatable ceiling; the registry projection is
  L2.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/cli/... ./internal/orchestrator/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make fitness-audit-isolation
  make check
  ```
  Expected: `PASS …`; `All checks passed.`
- **L6 (operator-run, dev host):** export `AGENT_BUILDER_MAX_WORKERS=2`,
  `AGENT_BUILDER_MAX_GOALS=4`, feed several goals via env/stdin, run `agent-builder
  orchestrate`; observe (logs/registry trace) that no more than 2 sub-goal workers run at
  once and that goals overlap rather than running strictly one-at-a-time. Record the
  observed max-concurrency and the goal interleave in the verify commit.

## Out of scope

- The typed message protocol (`MessageSource`, `Message`, `MessageKind`) — task 113. This
  task reads `supervisor.GoalSource` only.
- The status-query handler / immediate Reporter answer — task 114 (this task defines the
  registry the handler will read).
- Apply-info-at-checkpoint and the pending-info queue — task 115.
- Cancellation, the per-goal `context.Context`/`CancelFunc`, the command mailbox, and the
  `Cancelled` state's wiring — tasks 113/116 (the registry type may reserve fields for them
  but this task does not drive them).
- Telegram wiring — task 117.
- Changing the `orchestrator.Planner` interface or the `Orchestrator.Handle`/`Resume`
  contract (the actor calls them as-is).
