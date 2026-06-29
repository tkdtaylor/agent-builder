# Task 112: Async control-loop core + actor-per-goal + live status registry

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Replace the serial `runGoalIntakeLoop` (`for { Next(); Handle() }`) in
`internal/cli/orchestrate.go` with a **non-blocking control loop** that spawns one
**goal-actor goroutine** per `new-goal` and tracks each goal in a new mutex-guarded **status
registry**. Enforce a fleet-wide worker semaphore (`AGENT_BUILDER_MAX_WORKERS`) acquired
**inside** `dispatchPlan`'s per-sub-goal goroutine (composing with the task-086 fan-out) and
a goal-admission cap (`AGENT_BUILDER_MAX_GOALS`). This task carries the concurrency skeleton
but still reads **goals only** from the existing `supervisor.GoalSource` — the message
protocol lands in task 113.

## Context

ADR 054 (the authoritative design) §1 and §3 specify the async execution model. The live
orchestrate path today is a synchronous batch loop: `Orchestrator.Handle` blocks until a
goal is fully planned, gated, dispatched, and joined before the loop reads the next goal, so
the orchestrator cannot answer anything while a goal runs. ADR 054 makes the control loop
non-blocking and the per-goal lifecycle an actor goroutine.

### Grounded current state (ADR 054 §Context, verified against code)

- **Top-level intake is serial today.** `runGoalIntakeLoop` (`orchestrate.go` ~L236) reads
  one goal, calls `Handle`, and loops only when `Handle` returns. This task fans intake out.
- **Sub-goal dispatch is already concurrent** (task 086, `dispatchPlan` ~L389): one
  goroutine per sub-goal joined by a `sync.WaitGroup`, outcomes written at the sub-goal
  index. This task **reuses** that fan-out and adds the fleet semaphore inside the existing
  per-sub-goal goroutine — it does not rewrite the sub-goal fan-out.
- **Plan state is goalID-keyed + concurrency-safe** (`MemoryPlanStore`,
  `internal/orchestrator/store.go`). The new registry mirrors that mutex discipline.
- **Audit `Append` is already mutex-guarded** (`internal/audit/blocksink.go` ~L63); the hash
  chain stays single-writer-correct across M goals × N workers with **no sink change** (ADR
  054 §1). The invariant is now load-bearing across goals — assert it (REQ-112-06).

### The two concurrency bounds (ADR 054 §1)

Concurrency is two-dimensional: M concurrent goals × N sub-goal workers each.

- **`AGENT_BUILDER_MAX_WORKERS`** (default conservative, e.g. 4) — a **shared weighted
  semaphore** acquired at the point of worker dispatch, **inside** `dispatchPlan`'s
  per-sub-goal goroutine: `Acquire(1)` before `o.dispatch(...)`, `Release(1)` (deferred)
  after. This caps **total live workers across all goals** — the load-bearing bound on
  sandbox/box pressure. The existing WaitGroup still joins a goal's own sub-goals.
- **`AGENT_BUILDER_MAX_GOALS`** (default e.g. 8) — a looser **goal-admission cap** at the
  control loop; excess `new-goal`s park with `Queued` status until a slot frees. This is
  back-pressure on planning state, not the box bound.

### The status registry (ADR 054 §3)

A goalID-keyed, mutex-guarded registry of lifecycle state
(`Queued/Planning/AwaitingApproval/Dispatching/Done/Failed`; `Cancelled` is added when
cancellation lands in 116) with per-sub-goal progress. The goal actor transitions its own
goalID's state at each lifecycle edge; sub-goal progress is written from inside the
task-086 dispatch goroutines (same place outcomes are written today). **The registry is a
projection for observability — it is NOT the source of truth for control flow** (the
PlanStore remains that), so a registry write failure never halts a goal. The registry type
should reserve room for the per-goal command mailbox / `context.CancelFunc` / pending-info
queue that tasks 113/115/116 add, but this task drives only the states it owns.

### Security invariants carried forward (ADR 054 §6)

Concurrency must not open a path around the existing gates. The self-repo bright line, the
policy fail-closed gates, the SEC-003 deny-audit rule, and the ReplayCache ONE-cache-per-
direction singletons (083 SEC-001) all run per-sub-goal regardless of concurrency and are
preserved unchanged. The ReplayCaches stay **assembly-time singletons** — do NOT make them
per-goal or per-actor (that reopens the replay window). The audit chain interleaving across
goals on one mutex-guarded chain is acceptable and expected (events carry `TaskID`/`RunID`).

## Requirements

| Req ID      | Description                                                                                                                          | Priority   |
|-------------|------------------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-112-01  | The control loop is non-blocking: reading the next goal and processing a goal are decoupled; a goal in `Dispatching` does not stall intake of the next | must have |
| REQ-112-02  | A `new-goal` spawns a goal-actor goroutine owning that goal's lifecycle via `Orchestrator.Handle`; M goals run concurrently         | must have  |
| REQ-112-03  | Fleet-wide `AGENT_BUILDER_MAX_WORKERS` semaphore acquired **inside** the per-sub-goal goroutine caps total live workers at the bound | must have  |
| REQ-112-04  | Goal-admission cap `AGENT_BUILDER_MAX_GOALS` bounds live goal actors; excess goals park with `Queued` state until a slot frees      | must have  |
| REQ-112-05  | Status registry is goalID-keyed, mutex-guarded, a projection only (write failure never halts a goal); states transition at each lifecycle edge | must have  |
| REQ-112-06  | Audit hash chain stays valid + `verify`-clean under M goals × N workers (single mutex-guarded `Append`; no sink change)             | must have  |
| REQ-112-07  | Semaphore permits balanced on every path (acquire→deferred-release); no leak after drain; `-race` clean                            | must have  |

## Readiness gate

- [x] Task 081 merged (orchestrator core — `Handle`/`Resume`, `Planner`, `PlanStore`)
- [x] Task 086 merged (multi-worker concurrent sub-goal dispatch — `dispatchPlan` fan-out)
- [x] Task 099 merged (`orchestrate` subcommand + `assembleOrchestrate` + `runGoalIntakeLoop`)
- [x] Task 085 merged (containment policy + self-repo bright line + audit gates)
- [x] Task 083 merged (ReplayCache ONE-cache-per-direction singletons)
- [ ] Task 111 landed first (it edits `orchestrate_seams.go`/`orchestrate.go` — get the
      SEC-001 fail-fast keygen fix in before this restructures those files; see Dependencies)
- [x] ADR 054 §1/§3/§6 read

## Acceptance criteria

- [ ] [REQ-112-01] TC-112-01: with a blocking stub dispatch, `goal-B` advances past `Queued` while `goal-A` is held in `Dispatching`; a `registry.Get("goal-A")` read returns (`State==Dispatching`) without blocking
- [ ] [REQ-112-02] TC-112-02: `goal-A` and `goal-B` observed `Dispatching` simultaneously (bounded-wait for both before releasing either); exactly one `Handle` per goalID
- [ ] [REQ-112-03] TC-112-03: `MAX_WORKERS=2`, 2 goals × 2 sub-goals → max concurrent live workers **exactly 2**; control case `MAX_WORKERS=4` → max **4**
- [ ] [REQ-112-04] TC-112-04: `MAX_GOALS=1` → `goal-B` parked `Queued` while `goal-A` dispatches; `goal-B` advances after `goal-A` terminal; non-`Queued` non-terminal actor count ≤ 1
- [ ] [REQ-112-05] TC-112-05: ordered transitions `Planning→Dispatching→Done` (+ `SubGoals[0]` running→done) recorded; with a no-op registry the goal still completes via the dispatch spy
- [ ] [REQ-112-06] TC-112-06: 3 goals × 2 sub-goals concurrent → `audit verify` reports the chain valid; event count exact; `-race` clean on the sink
- [ ] [REQ-112-07] TC-112-07: after drain (with some dispatch errors), `Acquire(MAX_WORKERS)` succeeds (no permit leak); suite `-race` clean

## Verification plan

- **Highest level achievable: L6** — `agent-builder orchestrate` on the live binary with
  several env/stdin goals and a low `AGENT_BUILDER_MAX_WORKERS`, observing concurrent
  dispatch under the cap, plus a `-race` build. L2 (concurrency unit tests) + L3 (`audit
  verify` + fitness + `make check`) are the CI-automatable ceiling; the registry is L2.
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
  orchestrate`; observe no more than 2 concurrent sub-goal workers and overlapping (not
  strictly serial) goals. Record observed max-concurrency + interleave in the verify commit.

## Modules touched

- `internal/cli` (`orchestrate.go` — replace `runGoalIntakeLoop` with the non-blocking
  control loop + goal-admission cap; spawn goal-actor goroutines; assemble the registry +
  semaphore from `MAX_WORKERS`/`MAX_GOALS`).
- `internal/orchestrator` (the status-registry type + the worker semaphore acquire/release
  **inside** `dispatchPlan`'s per-sub-goal goroutine; registry write at sub-goal
  start/finish).
- `docs/spec/configuration.md` (`AGENT_BUILDER_MAX_WORKERS`, `AGENT_BUILDER_MAX_GOALS`).
- `docs/spec/behaviors.md` + `docs/architecture/diagrams.md` (the orchestrate runtime flow
  moves from serial loop to control-loop + actor-per-goal — a diagrammed flow change).

(Two code modules — `internal/cli` + `internal/orchestrator` — within the at-most-two rule.
The registry type lives in `internal/orchestrator` next to the PlanStore; the control loop
lives in `internal/cli` next to the assembly it replaces.)

## Out of scope

- The typed message protocol (`MessageSource`/`Message`/`MessageKind`) — task 113. This task
  reads `supervisor.GoalSource` only.
- The status-query handler + immediate Reporter answer — task 114.
- Apply-info-at-checkpoint + pending-info queue — task 115.
- Cancellation, the per-goal `context.Context`/`CancelFunc`, the command mailbox, and the
  `Cancelled` state wiring — tasks 113/116 (the registry type reserves fields for them only).
- Telegram wiring — task 117.
- Any change to `orchestrator.Planner`, `Orchestrator.Handle`/`Resume`, the `audit.Sink`, or
  the ReplayCache singletons.

## Dependencies

- **Task 111 should land BEFORE this task** (ADR 054 §Existing-task updates): 111 edits
  `orchestrate_seams.go`/`orchestrate.go` (the SEC-001 keygen fail-fast), the same files this
  task restructures. Landing 111 first avoids a churn collision. No functional dependency —
  just ordering.
- Tasks 081, 086, 099, 085, 083 — merged.
- ADR 054 — the authoritative design.
- **Unblocks:** 113 (message protocol builds on this control loop), and transitively 110 (the
  re-specced LLM-planner wiring must not start before this merges — collision on
  `assembleOrchestrate`).
