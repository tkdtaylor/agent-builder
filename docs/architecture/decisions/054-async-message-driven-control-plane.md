# ADR 054 — Async, message-driven orchestrator control plane

**Status:** Proposed
**Date:** 2026-06-28
**Author:** architect
**Architect review required:** yes — this replaces the serial goal-intake loop with a
concurrent, message-driven control plane and adds cancellation across the worker seam

---

## Context

The live orchestrate path is a **synchronous batch loop**. `runGoalIntakeLoop`
(`internal/cli/orchestrate.go` ~L236) is literally `for { Next(); Handle() }`, and
`Orchestrator.Handle` (`internal/orchestrator/orchestrator.go` ~L252) **blocks until a
goal is fully processed** — planned, gated, and (on allow) every sub-goal dispatched and
joined — before the loop reads the next goal. While a goal runs there is no way to ask
the orchestrator anything: the single thread of control is inside `Handle`.

We want the orchestrator to stay **responsive after goals start**: the operator can ping
for status, submit more goals, send new information on an in-flight goal, and cancel a
goal, all while goals run concurrently.

### Grounded current state (verified against the code)

- **Top-level intake is strictly serial.** `runGoalIntakeLoop` reads one goal, calls
  `Handle`, and only loops when `Handle` returns. Multiple top-level goals never overlap.
- **Per-goal sub-goal dispatch is *already* concurrent** (task 086, `dispatchPlan`
  ~L389): one goroutine per sub-goal, joined by a `sync.WaitGroup`, outcomes written into
  a pre-sized slice at the sub-goal index, best-effort completion, audit/PlanStore writes
  serialized. So the fan-out primitive exists; what is missing is fan-out across
  *top-level* goals.
- **Plan state is goalID-keyed and concurrency-safe.** `PlanStore.Get/Put/Delete(goalID)`
  (`orchestrator.go` ~L116) over the mutex-guarded `MemoryPlanStore`
  (`internal/orchestrator/store.go`). `HasPendingPlan(goalID)`, `Resume(Approval{GoalID})`
  already address goals by ID. This is the seam an apply-info / cancel command keys off.
- **The inbound seam carries goals only, no message type.**
  `supervisor.GoalSource.Next() (Task, bool, error)` (`internal/supervisor/supervisor.go`
  ~L75). There is no notion of `status` / `info` / `cancel` today. Outbound is
  `supervisor.Reporter.Report(ctx, text)` (`internal/supervisor/reporter.go`).
- **Telegram already implements both seams but is not wired in.**
  `internal/channel/telegram/adapter.go` `Adapter` (GoalSource over `getUpdates`) and
  `reply.go` `ReplyAdapter` (Reporter over `sendMessage`). The live orchestrate path
  hardcodes `newEnvGoalSource` + `newLogReporter` (`orchestrate.go` ~L216/227); neither
  Telegram adapter is reachable from `orchestrate` yet.
- **The dispatch seam round-trips one sub-goal.** `newTransportDispatch`
  (`internal/cli/orchestrate_seams.go`) seals/verifies the work-item, calls
  `runtimewiring.Run(cfg, io.Discard)`, then seals/verifies the result.

### The cancellation cost — `runtime.Run` has no `context.Context` (the gating finding)

The live worker entry point is **`func Run(config Config, stdout io.Writer) error`**
(`internal/runtime/run.go` L535) — **it takes no `context.Context`.** Nor does the
underlying **`func (s *Supervisor) Run() (err error)`** (`internal/supervisor/supervisor.go`
L234). Today a worker is stopped only by the supervisor's own **wall-clock timeout**
(`WithRunTimeout` → `box.Kill(handle)` on timer fire, supervisor.go ~L301–324); there is
no caller-driven cancel path into a running box.

**Consequence for this ADR:** cancellation that tears down an *in-flight* sub-goal worker
mid-run is **not** a free `ctx.Done()` plumb-through — it requires threading a
`context.Context` through `runtime.Run` → `supervisor.Run` → the run-loop select, so a
cancel signal triggers the existing `box.Kill`/`Teardown` path the same way the
wall-clock timer already does. That is a real, multi-file change with its own test
surface, and it **gates the size of the cancellation task** (it cannot be a thin
wrapper). This ADR scopes cancellation to honor the operator's "cancel is in scope"
decision while confining the seam change to the smallest correct cut: add a
`ctx context.Context` parameter to `runtime.Run` and `Supervisor.Run`, select on
`ctx.Done()` alongside the existing wall-clock timer, and reuse `box.Kill`/`Teardown` for
teardown. No new teardown mechanism is invented; cancel becomes a second trigger for the
kill path that already exists.

### The two product decisions are already made (design to them, do not re-litigate)

1. **Apply-new-info at the next checkpoint.** New info on an in-flight goal is *queued*
   and folded into that goal's **next planning/approval checkpoint** (or spawns an
   amendment sub-goal). Already-running sandboxed sub-goal workers finish as-is — never
   killed mid-task by an info message.
2. **Cancellation is in scope.** A cancel command stops a goal and tears down its
   in-flight sub-goal workers/sandboxes.

---

## Decision

Replace the synchronous batch loop with an **async, message-driven control plane**: a
single non-blocking **control loop** that reads typed messages from a generalized inbound
seam and dispatches them to **per-goal actor goroutines**, with a **live status
registry**, a **fleet-wide concurrency cap**, **checkpoint-augment** semantics for new
info, and **context-driven cancellation** threaded through the worker seam.

### 1. Async execution model — control loop + actor-per-goal

- **Non-blocking control loop.** A single goroutine owns the inbound seam: it reads one
  typed message and routes it — it never blocks on goal *processing*. Reading a message
  and handling a goal are decoupled. The control loop is the only reader of the inbound
  seam (no concurrent `Next()` races).
- **Actor-per-goal.** A `new-goal` message starts a **goal actor** goroutine that owns
  that goal's lifecycle (plan → gate → dispatch → aggregate → report) by calling the
  existing `Orchestrator.Handle`/`Resume`. Each actor owns one goalID; the control loop
  retains no per-goal processing state beyond the registry handle. Control messages
  (`status`, `info`, `cancel`) are delivered to the addressed actor via a per-goal
  **command mailbox** (a small buffered channel keyed by goalID in the registry), or
  answered directly by the control loop for `status` (which only reads the registry).
- **Fleet-wide concurrency cap.** Concurrency is now **two-dimensional**: M concurrent
  top-level goals × N sub-goal workers each. Define **one global worker bound** —
  `AGENT_BUILDER_MAX_WORKERS` (default conservative, e.g. 4) — enforced by a **shared
  weighted semaphore acquired at the point of worker dispatch**, *inside* `dispatchPlan`'s
  per-sub-goal goroutine, **not** at the goal-actor level. This composes cleanly with the
  task-086 fan-out: the existing WaitGroup still joins a goal's own sub-goals, but each
  sub-goal goroutine must `Acquire(1)` before `o.dispatch(...)` and `Release(1)` after, so
  the *total* live workers across all goals never exceeds the cap. A separate, looser
  **goal-admission cap** (`AGENT_BUILDER_MAX_GOALS`, default e.g. 8) bounds how many goal
  actors may exist at once; excess `new-goal` messages park in a queue with `queued`
  status until a slot frees. The worker semaphore is the load-bearing bound (it caps
  sandbox/box pressure); the goal cap is back-pressure on planning state.
- **Audit-chain append serialization under concurrency.** The shared `audit.Sink`
  (`BlockSink.Append` is **mutex-guarded** — verified `internal/audit/blocksink.go` L63)
  already serializes appends; the hash chain stays single-writer-correct with M goals ×
  N workers because every appender goes through that one mutex. **No change needed** to
  the sink, but the invariant is now load-bearing across goals, not just sub-goals — note
  it explicitly (see Consequences). The SEC-003 deny-audit-must-succeed rule
  (`emitFleetEventForDeny`) is preserved unchanged; it already runs inside the per-sub-goal
  goroutine.

### 2. Inbound message protocol — evolve `GoalSource` into `MessageSource`

Generalize the inbound seam from "goal-only" to **typed messages**. Introduce a new seam
**`supervisor.MessageSource`** rather than mutating `GoalSource` (keeping `GoalSource`
intact preserves `runtime.Run`'s recipe-driven `GoalSourceFactory` path, which is a
*different* inbound seam — the per-worker task source inside the box — and must not be
disturbed):

```go
type MessageKind int
const (
    MsgNewGoal MessageKind = iota // a fresh goal to plan
    MsgStatus                     // query lifecycle state (optionally a goalID; empty = fleet)
    MsgInfo                       // new info for an in-flight goal (carries GoalID + text)
    MsgCancel                     // cancel a goal + tear down its workers (carries GoalID)
)

type Message struct {
    Kind   MessageKind
    GoalID string          // addresses status/info/cancel; the new goal's ID for new-goal
    Goal   supervisor.Task // populated for MsgNewGoal
    Text   string          // info payload / free-form
}

type MessageSource interface {
    Next() (Message, bool, error)
}
```

- **Addressing follow-up messages.** Goals are addressed by **`GoalID`** (already the
  PlanStore / registry key). The inbound channel's reply-to / conversation handle maps to
  a goalID at the *adapter* edge (e.g. Telegram threads a goalID into the message text or
  derives it from a reply-to); the control plane only ever sees `Message.GoalID`. An
  `info`/`cancel`/`status` for an unknown goalID is answered with a "no such goal" report,
  never a panic (fail-loud-but-graceful).
- **Local-test path survives (load-bearing).** The env/stdin source must keep working so
  the operator can drive the control plane locally without Telegram. Generalize
  `newEnvGoalSource` into a `MessageSource` that parses a **line-oriented command grammar**
  from stdin/env: a bare line (or `AGENT_BUILDER_GOAL_SPEC`) is `new-goal`; lines prefixed
  `status`, `info <goalID> <text>`, `cancel <goalID>` map to the corresponding kinds.
  EOF / no-more-input returns `ok=false` and the control plane drains and exits. This is
  the local-first testing seam every L5/L6 verification of this work hangs off.

### 3. Live status registry

A **goalID-keyed, concurrency-safe registry** of in-flight lifecycle state:

```go
type GoalState int // Queued, Planning, AwaitingApproval, Dispatching, Done, Failed, Cancelled

type GoalStatus struct {
    GoalID    string
    State     GoalState
    SubGoals  []SubGoalProgress // per-sub-goal: name, recipe, running/done/failed
    UpdatedAt time.Time
}
```

- The registry is a mutex-guarded map (mirrors `MemoryPlanStore`). The goal actor
  **transitions its own goalID's state** at each lifecycle edge (intake → `Planning`,
  `require_approval` → `AwaitingApproval`, allow → `Dispatching`, sub-goal start/finish →
  `SubGoals[i]`, terminal → `Done`/`Failed`/`Cancelled`). Sub-goal progress is written
  from inside the task-086 dispatch goroutines (same place outcomes are written today).
- A `status` message **reads** the registry and the Reporter answers **immediately** —
  this is what makes the plane "responsive": status never waits on `Handle`. The registry
  is a *projection* for observability; it is **not** the source of truth for control flow
  (the PlanStore remains that), so a registry write failure never halts a goal.
- The registry carries each goal's **command mailbox** channel and a `context.CancelFunc`
  (see §5), so `cancel`/`info` routing is a registry lookup.

### 4. Checkpoint-augment semantics (apply-new-info)

Define **checkpoint** precisely in this codebase: a checkpoint is a point in a goal's
lifecycle where the orchestrator is **about to (re)plan or is paused awaiting approval** —
concretely the **`require_approval` pause** in `Handle` (the plan sits in the PlanStore,
state `AwaitingApproval`, dispatch not yet begun) and the **pre-plan boundary** of any
re-plan. It is **not** a point inside an already-dispatched worker's run.

- **Queue, don't interrupt.** An `info` message for goalID G appends its text to a
  per-goal **pending-info queue** in the registry. It does **not** touch any running
  worker. Running sub-goal workers finish as-is (honoring product decision 1) —
  guaranteed because workers are dispatched from `dispatchPlan` and the info queue is read
  only at checkpoint boundaries, never inside the dispatch goroutine.
- **Fold at the next checkpoint.** When the goal actor next reaches a checkpoint:
  - If the goal is **`AwaitingApproval`**: the queued info is surfaced *with* the approval
    solicitation (the operator sees the amended context before approving), and on
    `Resume`-approve the plan is **re-planned** with the info folded into the goal text
    (or the original plan dispatched if the info was purely informational — the actor
    re-runs `planner.Plan` on the augmented goal and replaces the stored plan before
    dispatch).
  - If the goal has **already dispatched** (no upcoming natural checkpoint), the info
    **spawns an amendment sub-goal**: a new goal actor for `G/amend-1` carrying the info as
    its goal text, gated through the normal spawn-plan/spawn-worker policy path. This keeps
    the "info is folded at *a* checkpoint" invariant true even for goals past their
    approval gate, without mutating a running worker.
- **Confirmation:** running workers are **never mutated mid-task** under either branch.
  The only state an info message writes synchronously is the registry's pending-info
  queue; everything else happens at a checkpoint the actor controls.

### 5. Cancellation + teardown

- **Cancel signal.** Each goal actor runs under a `context.Context` derived from a
  per-goal `context.WithCancel`; the `CancelFunc` lives in the registry. A `cancel`
  message looks up the goalID and calls `CancelFunc`, sets state `Cancelled`, and removes
  the plan from the PlanStore (so a late approval cannot resurrect it).
- **Teardown of in-flight workers — the `runtime.Run` ctx change.** Because
  `runtime.Run`/`Supervisor.Run` take **no ctx today** (the gating finding above), the
  cancellation task **must** thread a `context.Context` through:
  `dispatch(ctx, sub, base)` → `runtime.Run(ctx, cfg, stdout)` → `Supervisor.Run(ctx)` →
  the run-loop `select`, adding a `case <-ctx.Done():` arm **beside** the existing
  `case <-timer.C:` wall-clock arm (supervisor.go ~L305). On cancel, that arm calls the
  **same `box.Kill(handle)` + `Teardown(handle)` path** the timeout already uses — no new
  teardown mechanism. The `DispatchFunc` type
  (`func(ctx, SubGoal, runtime.Config) error`) **already carries a ctx**; today
  `defaultDispatch` and `newTransportDispatch` *ignore* it past the transport step — the
  fix is to pass it into `runtime.Run`.
- **Fail-safe on partial teardown.** If `box.Kill`/`Teardown` partially fails, follow the
  existing supervisor convention: `errors.Join` the kill/teardown errors, log loudly,
  mark the sub-goal `Failed` (not silently `Cancelled`), and surface the teardown error in
  the goal's final report. A box that cannot be confirmed torn down is reported as a
  **leak requiring operator attention**, never swallowed — the wall-clock kill remains the
  backstop (a cancel that fails to tear down still hits the timeout).
- **Best-effort across siblings.** Cancelling goal G cancels only G's workers (G's
  derived ctx); sibling goals' contexts are independent, so cancel has no blast radius
  beyond the addressed goal.

### 6. Import / boundary & security notes

- **Self-repo bright line intact.** `decideSpawnWorker`'s self-repo guard runs per
  sub-goal regardless of concurrency; amendment sub-goals (§4) go through the *same*
  `dispatchOne` gate, so an info-spawned amendment cannot bypass the bright line.
- **Policy fail-closed intact.** Every dispatch — including amendment sub-goals — passes
  the spawn-plan + spawn-worker gates. Concurrency does not add a path around `decideSpawn`.
- **ReplayCache invariant intact.** The ONE-cache-per-direction rule (083 SEC-001) is
  *strengthened* by concurrency, not threatened: the shared caches are mutex-guarded and
  must remain assembly-time singletons; **do not** make them per-goal or per-actor (that
  would reopen the replay window). The control plane holds the same two caches for the
  process lifetime.
- **Fleet-audit chain intact.** Single mutex-guarded `Append` keeps the hash chain correct
  across M×N appenders (§1). `goal-intake` / `plan-decided` / `spawn-decided` /
  `completion` events now interleave across goals on one chain — that is acceptable and
  expected (events carry `TaskID`/`RunID`); the chain proves *order of append*, not
  per-goal isolation.
- **New race surface (call it out).** (a) The status registry is new shared mutable state
  — must be mutex-guarded and only ever a projection (never gate control flow on it).
  (b) The per-goal command mailbox channels must be created before the actor is
  registered, to avoid a `status`/`cancel` racing actor startup (register-then-start
  ordering). (c) The worker semaphore is shared global state — acquire/release must be
  balanced on every path including the cancel/teardown path (release on ctx-cancel return,
  or the cap leaks permits). (d) Cancelling a goal mid-`Resume` must consume the plan from
  the store under the same delete path so a concurrent approval cannot double-dispatch.

---

## Consequences

### Positive

- The orchestrator is **responsive while goals run**: status, new goals, info, and cancel
  are all serviceable concurrently — the north-star "front door" UX.
- **Reuses the task-086 fan-out** rather than reinventing concurrency; the actor model is
  a thin layer over the existing `Handle`/`Resume`/`dispatchPlan`.
- **One global worker cap** gives a single, tunable bound on sandbox/box pressure that
  composes M goals × N workers correctly — no per-goal cap multiplication blowing past
  host limits.
- Cancellation finally threads a real `context.Context` through the worker seam, which is
  a **latent good** beyond cancel (deadlines, shutdown drain) the codebase has lacked.

### Negative / what gets harder

- **`runtime.Run`/`Supervisor.Run` signatures change** (add `ctx`). This touches the run
  path and every caller (`RunFromEnv`, `defaultDispatch`, `newTransportDispatch`, tests).
  It is the single largest mechanical cost and the reason the cancel task is non-trivial.
- **The audit chain now interleaves goals.** Reading the chain for a single goal's story
  requires filtering by `TaskID`/`RunID`; the linear "one goal then the next" readability
  of the batch model is gone. The serialization correctness holds, but human forensics
  cost rises slightly.
- **More live shared state** (registry, mailboxes, semaphore) = more race surface to test.
  The verification bar rises: `-race` runs and concurrency stress become load-bearing.
- **Status is eventually-consistent.** A `status` reply reflects the registry at read
  time; a goal may transition the instant after. Acceptable for an operator UX, but the
  reply must be understood as a snapshot, not a transaction.
- **Amendment sub-goals add goalID namespace** (`G/amend-N`). The ID scheme must stay
  collision-free and the registry/PlanStore must tolerate the derived IDs.

### Neutral / explicitly unchanged

- `GoalSource` (the per-worker, in-box recipe task source) is untouched — only the
  *inbound operator* seam generalizes to `MessageSource`.
- The ReplayCache singletons, policy gates, self-repo bright line, and SEC-003 deny-audit
  rule are preserved verbatim.

---

## Recommended task decomposition

Dependency-ordered, starting at **112** (verified next free ID: 109/110/111 exist in
`docs/tasks/backlog/`, ≤108 completed). Right-sized; splits avoided where artificial.

- **Task 112 — Async control-loop core + actor-per-goal + status registry.**
  Replace `runGoalIntakeLoop`'s serial `for { Next(); Handle() }` with a non-blocking
  control loop that spawns a goal actor goroutine per `new-goal` and tracks each in a new
  mutex-guarded **status registry** (`Queued/Planning/AwaitingApproval/Dispatching/Done/
  Failed`), with the fleet-wide worker semaphore (`AGENT_BUILDER_MAX_WORKERS`) acquired
  inside `dispatchPlan`'s per-sub-goal goroutine and the goal-admission cap
  (`AGENT_BUILDER_MAX_GOALS`). This task carries the concurrency skeleton but still reads
  *goals only* (the message protocol lands in 113) — to keep it shippable, it consumes the
  existing `GoalSource` and proves M goals run concurrently with the cap honored.
  **Highest verification: L6** — run `orchestrate` with several env/stdin goals and observe
  concurrent dispatch under the cap on the live binary (plus `-race`). The registry is L2.
  *Touches `internal/cli` + `internal/orchestrator` (registry type + dispatch semaphore).*

- **Task 113 — Inbound message protocol + command router (`MessageSource`).**
  Introduce `supervisor.MessageSource` + the typed `Message`/`MessageKind`, generalize the
  env/stdin source into a line-oriented `MessageSource` (`new-goal` default; `status`,
  `info <goalID> <text>`, `cancel <goalID>`), and add the control-loop router that
  dispatches each kind (status/info/cancel routed to the addressed goal's mailbox; unknown
  goalID → graceful "no such goal" report). Wires the env/stdin path so the local-first
  test seam works end to end. **Highest verification: L6** — drive all four message kinds
  over stdin against the live binary and observe each routed correctly. *Touches
  `internal/supervisor` (seam) + `internal/cli` (env source + router).*

- **Task 114 — Status-query handler + immediate reporter answer.**
  The `status` message reads the registry and answers over the Reporter **without** waiting
  on `Handle`; render fleet status (no goalID) and per-goal status (with goalID) including
  sub-goal progress. **Highest verification: L6** — submit a long goal, then a `status`
  mid-flight, observe an immediate registry-projected reply on the live binary. *Touches
  `internal/cli` (+ a small render helper; possibly `internal/orchestrator` if progress
  read needs a registry accessor).*

- **Task 115 — Apply-info-at-checkpoint (queue + fold + amendment sub-goal).**
  Add the per-goal pending-info queue to the registry; surface queued info with the
  approval solicitation; on approve, re-plan the goal with info folded; for
  already-dispatched goals, spawn the `G/amend-N` amendment sub-goal through the normal
  gates. Assert **running workers are never mutated mid-task**. **Highest verification:
  L6** — send `info` during an `AwaitingApproval` goal and observe the amended plan;
  send `info` to a dispatched goal and observe an amendment sub-goal. *Touches
  `internal/cli` + `internal/orchestrator` (checkpoint hook + re-plan).*

- **Task 116 — Cancellation + teardown (the `runtime.Run` ctx thread).**
  Thread `context.Context` through `runtime.Run` and `Supervisor.Run`, add the
  `case <-ctx.Done():` arm beside the wall-clock arm reusing `box.Kill`/`Teardown`, derive
  a per-goal cancel context in the control plane, and route `cancel` to it (state →
  `Cancelled`, plan consumed from store, semaphore permits released, partial-teardown
  failures `errors.Join`'d + reported as leaks). **This is the largest task** because of
  the signature change and its caller fan-out — do not under-size it. **Highest
  verification: L6** — cancel an in-flight goal and observe the box torn down on the live
  binary (plus `-race`); L2 covers the select arm and permit-release accounting. *Touches
  `internal/runtime` + `internal/supervisor` + `internal/cli` — this is the one task that
  legitimately spans three modules; if the planner wants it tighter, split the seam change
  (`runtime`/`supervisor` ctx plumb) from the control-plane cancel routing (`cli`) into
  116a/116b, but the ctx plumb must land first.*

- **Task 117 — Telegram wiring (message-aware), LAST.**
  Make the Telegram `Adapter` emit typed `Message`s (derive `MessageKind`/`GoalID` from
  message text or reply-to at the adapter edge) and wire `Adapter` (MessageSource) +
  `ReplyAdapter` (Reporter) into `assembleOrchestrate` in place of the hardcoded
  `newEnvGoalSource`/`newLogReporter`, behind config (so the env/stdin path stays the
  default for local tests). **Highest verification: L6** — drive new-goal/status/info/
  cancel over a real Telegram bot and observe each end to end. *Touches
  `internal/channel/telegram` + `internal/cli` (wiring).* Last because it depends on the
  message protocol (113), status (114), info (115), and cancel (116) all existing.

**Dependency order:** 112 → 113 → {114, 115, 116} → 117. 114, 115, 116 each depend on
112+113 but are mutually independent (different control-loop handlers + different lower
seams) and may run in parallel on separate branches once 113 merges; 117 depends on all.

---

## Existing-task updates required

- **Task 110 (wire `AGENT_BUILDER_PLANNER=llm` into `orchestrate`) — re-spec to compose
  with the async core, sequence AFTER task 112.** Task 110 plugs the LLM planner into the
  **same `orchestrate.go` loop** that task 112 rewrites from serial to async. Re-litigating
  the planner inside the old serial loop and then having 112 rewrite around it wastes work
  and risks a merge collision on `assembleOrchestrate`/`plannerFromEnv`. **Recommendation:
  sequence 110 *after* 112** and update its body to assemble the planner into the
  async-loop wiring (the planner is constructed in `assembleOrchestrate` exactly as before
  — the *planner seam is orthogonal to the control loop* — so the change is small: re-point
  110's "the loop" references at the new control-plane assembly and confirm
  `plannerFromEnv` still feeds `Orchestrator.New`). The planner work itself does not change;
  only the surrounding loop it plugs into does. Do **not** start 110 before 112 merges.
  *(If the operator wants the LLM planner sooner, 110 may land before 112 against the
  current serial loop, but then 112 must explicitly carry "preserve the `=llm` planner
  wiring across the loop rewrite" in its scope — the post-112 sequencing is cleaner.)*

- **Task 109 (single-shot `Completer` seam + ollama completer in `internal/executor`) — no
  change.** It is confined to `internal/executor`, off the orchestrate loop entirely; the
  async rewrite does not touch it. It remains 110's hard prerequisite. **109 is unaffected
  and may proceed independently / first.**

- **Task 111 (SEC-001: propagate the discarded `GenerateKeyPair()` error in
  `newTransportDispatch`) — no functional change; one sequencing note.** It hardens
  `newTransportDispatch`/`assembleOrchestrate` error propagation — both of which the async
  core (112) and Telegram wiring (117) also edit. It is *independent* of the design and may
  land any time, but to avoid a churn collision in `orchestrate_seams.go`/`orchestrate.go`,
  **prefer landing 111 before 112** (it is small and self-contained, and gets the
  fail-fast keygen fix in before the file is restructured). No spec/ADR change for 111.

**Recommended overall execution order across all tasks:**
`109` and `111` first (independent, small, both unblock/avoid-collision) → `112`
(async core) → `110` (LLM planner, re-specced onto the async loop) and `113` (message
protocol) → `114`/`115`/`116` (parallelizable) → `117` (Telegram, last).
