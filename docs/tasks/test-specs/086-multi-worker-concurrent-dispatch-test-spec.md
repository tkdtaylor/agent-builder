# Test spec — Task 086: Multi-worker concurrent dispatch

**Linked task:** `docs/tasks/backlog/086-multi-worker-concurrent-dispatch.md`
**Written:** 2026-06-27 (expanded 2026-06-28)
**Status:** active — prerequisites 081/083/085 merged

## Context

The orchestrator's `dispatchPlan` is currently **sequential** (task 081 / ADR 046 §5:
"one worker per sub-goal, sequential"). ADR 042 fixes the decision that the agent runs
**multiple concurrent workers from the start**. This task converts `dispatchPlan` from a
sequential `for` loop into a **concurrent fan-out**: every approved sub-goal is dispatched
in its own goroutine, all started before any completes, each in its own exec-sandbox via
the `DispatchFunc` seam (the live default reuses `runtime.Run`).

The hard constraint is **race-safety** (`go test -race`). The concurrent dispatch shares
three pieces of mutable state across goroutines:

1. **The aggregated `PlanResult.Outcomes`** — each goroutine records one outcome.
2. **The shared `audit.Sink`** — every worker's spawn-decided event (plus the worker tier's
   own containment/finish events) appends to the **one** fleet-audit hash chain.
3. **The `PlanStore`** (already `sync.Mutex`-guarded — `MemoryPlanStore`).

Everything that task 085 made true must **still** hold under concurrency:
- The per-sub-goal `spawn-worker` policy gate fires for **each** worker (a deny still skips
  exactly that worker, not the plan).
- The self-repo bright line still denies a worker targeting the own-repo.
- The SEC-003 hard-error on a deny-event audit-append failure still halts the plan.
- Task 081's pause/resume approval still gates dispatch.

### Carry-forward from task 083 (the worker transport, now on a live concurrent path)

- **083 SEC-001 (was MEDIUM, now LIVE):** there is exactly **one** long-lived shared
  `*envelope.ReplayCache` per direction across all concurrent workers — never a fresh
  Receiver/cache per worker, or replay protection is defeated (a virgin cache accepts a
  replayed envelope). The `envelope.ReplayCache` is already `sync.Mutex`-guarded (see
  `internal/envelope/replay.go`), so it is safe to share across goroutines; this task
  confirms that under `-race`.

### Design decisions recorded by this task

- **Concurrency primitive:** `sync.WaitGroup` fan-out — one goroutine per sub-goal, a
  `WaitGroup.Wait()` join, outcomes written into a **pre-sized** `[]SubGoalOutcome` at the
  sub-goal index (no `append` from goroutines → no slice-growth race, and outcome order
  stays deterministic = sub-goal order, which TC-085-02/04 assert).
- **Partial-failure isolation:** a worker's dispatch error is captured into *its own*
  outcome slot; goroutines never return early and never cancel siblings → best-effort
  completion. (The SEC-003 deny-audit hard error is the one exception: it is collected and,
  if any occurred, returned after the join so the plan halts — preserving 085 SEC-003.)
- **Shared-sink race-safety:** the `audit.Sink` is a hash chain that must be appended
  serially. Both the orchestrator's own appends and the worker tier's appends (modelled by
  the dispatch spy) target the one sink concurrently. The sinks used on this path
  (`audit.FakeSink`, `audit.BlockSink`) are made `sync.Mutex`-guarded so concurrent
  `Append` is serialized and race-free.

## Requirements coverage

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-086-01 | Orchestrator dispatches N workers concurrently; each in its own exec-sandbox   | TC-086-01  |
| REQ-086-02 | A worker failure does not halt other in-flight workers (best-effort)            | TC-086-02  |
| REQ-086-03 | Aggregated result reflects per-worker outcomes (success + failure mix)          | TC-086-03  |
| REQ-086-04 | No data races under `-race` (concurrent aggregate + shared sink + store)         | TC-086-04  |
| REQ-086-05 | Fleet audit chain covers all N concurrent workers                               | TC-086-05  |

## Pre-implementation checklist

- [x] Task 081 merged (orchestrator core — sequential dispatch)
- [x] Task 083 merged (worker transport — signed envelopes + shared ReplayCache)
- [x] Task 085 merged (per-worker spawn-worker gate + fleet audit + self-repo guard)
- [x] Concurrency model decided: WaitGroup fan-out, pre-sized outcome slice, serialized sink
- [x] Partial-failure policy decided: best-effort (no early abort, no sibling cancel)

---

## Test cases

### TC-086-01 — All N workers start before any completes (true concurrency)

- **Requirement:** REQ-086-01
- **Level:** L2 (unit test with a barrier dispatch spy)
- **File:** `internal/orchestrator/concurrent_test.go`

**Input:** A plan with 3 approved sub-goals. The injected `DispatchFunc` is a **barrier
spy**: on entry it increments a started-counter and then blocks on a shared
`sync.WaitGroup`/channel barrier that releases only once **all 3** have entered. If
dispatch were sequential, the first call would block forever (the 2nd/3rd never enter to
release the barrier) → the test would deadlock/time out.

**Expected output (assertions):**
- All 3 dispatch goroutines reach the barrier (`started == 3`) **before any returns** —
  proven because the barrier only releases when the started-count hits 3. A sequential
  dispatch cannot satisfy this (asserted: the test completes within a timeout; a sequential
  implementation hangs).
- `result.Outcomes` has length 3, all `Success == true`.
- The dispatch spy recorded 3 distinct sub-goals (one `DispatchFunc` call per sub-goal =
  one exec-sandbox per worker).

### TC-086-02 — One worker failure does not halt the others (best-effort)

- **Requirement:** REQ-086-02
- **Level:** L2
- **File:** `internal/orchestrator/concurrent_test.go`

**Input:** A plan with 3 sub-goals. The dispatch spy fails **sub-goal 0 immediately**
(returns an error before/without blocking) while sub-goals 1 and 2 dispatch successfully.

**Expected output (assertions):**
- All 3 outcomes are present (`len(result.Outcomes) == 3`).
- Outcome 0 is `Success == false` and its `Detail` carries the failure reason.
- Outcomes 1 and 2 are `Success == true` (the surviving workers completed — they were not
  halted by worker 0's failure). The spy recorded that sub-goals 1 and 2 were actually
  dispatched (their `DispatchFunc` ran).
- `Handle` returns a nil error (a single worker failure is a recorded outcome, not a plan
  error).

### TC-086-03 — Aggregated result reflects a success+failure mix, delivered via Reporter

- **Requirement:** REQ-086-03
- **Level:** L2
- **File:** `internal/orchestrator/concurrent_test.go`

**Input:** A plan with 3 sub-goals; the dispatch spy fails sub-goal 1 (the middle one) and
succeeds 0 and 2.

**Expected output (assertions):**
- `result.Outcomes` length 3, in **sub-goal order** (deterministic): outcome 0 success,
  outcome 1 failure, outcome 2 success.
- Exactly **one** summary is delivered through the `Reporter` (`fakeReporter`), and it
  contains both an `OK` and a `FAIL` marker (the rendered mix).
- The count of successes (2) and failures (1) in the aggregated `PlanResult` is exact.

### TC-086-04 — No data races under `-race` (5 concurrent workers, shared sink + aggregate)

- **Requirement:** REQ-086-04
- **Level:** L2 (race detector — the load-bearing requirement)
- **File:** `internal/orchestrator/concurrent_test.go`
- **Gate command:** `go test -race -count=1 ./internal/orchestrator/...`

**Input:** A plan with **5** sub-goals. A `fleetDispatchSpy`-style dispatch func that, for
every worker, both records the dispatch **and appends two worker-tier events to the shared
`audit.FakeSink`** (containment + finish) — so the test exercises concurrent writes to (a)
the aggregated outcome slice, (b) the shared audit sink, and (c) the PlanStore. The
orchestrator is constructed with `WithAuditSink(sharedSink)`.

**Expected output (assertions):**
- `go test -race` exits 0 — **no race conditions reported** (the load-bearing assertion).
- `len(result.Outcomes) == 5`, all success.
- The shared sink recorded the expected event volume (≥ orchestrator events + 2 per worker)
  with no lost/corrupted events (asserted via exact action counts).

### TC-086-05 — Fleet audit chain covers all N concurrent workers (single chain)

- **Requirement:** REQ-086-05
- **Level:** L2 (FakeSink single-chain coverage); L5 when `AGENT_BUILDER_AUDIT_BIN` is set
- **File:** `internal/orchestrator/concurrent_test.go`

**Input:** 3 concurrent workers all complete; orchestrator constructed with a shared
`audit.FakeSink`; the dispatch spy appends each worker's containment + finish events to that
same sink.

**Expected output (assertions):**
- The one chain contains an orchestrator `spawn-decided` event for **each** of the 3 workers
  (count == 3), and the worker tier's `containment` (count == 3) and `finish` (count == 3)
  events — i.e. every one of the 3 concurrent workers is represented in the single chain
  (asserted per-worker by `TaskID`: each of the 3 distinct sub-goal task IDs appears among
  the `spawn-decided`/`containment`/`finish` events).
- `goal-intake` appears exactly once and is **first**; `completion` appears exactly once and
  is **last** (the orchestrator's chain bookends are preserved even though the per-worker
  events interleave nondeterministically between them).
- **L5 (when `AGENT_BUILDER_AUDIT_BIN` is set):** replay the recorded chain through a real
  `audit.BlockSink` and run `audit.VerifyChain` → `Valid == true`. When the env var is
  unset, log `L5 binary-deferred` and assert only L2.

---

## Carry-forward assertions (must still pass — regression guards)

These existing task-085 / 081 tests are part of the gate for this task; concurrency must not
regress them:

- `TestTC085_02_SpawnWorkerDenySkipsWorkerAndReports` — per-worker deny still skips exactly
  that worker; outcome order preserved.
- `TestTC085_02_SpawnWorkerFailClosedOnPolicyError` — fail-closed deny still skips dispatch.
- `TestTC085_03_FleetAuditChainCoversBothTiers` — fleet chain bookends + counts (now under
  concurrent appends; goal-intake first / completion last preserved).
- `TestTC085_05_*` — self-repo bright line still denies.
- `TestTC081_02_*` / `TestTC081_03_*` / `TestTC081_04_*` — pause/resume, per-sub-goal
  dispatch, success+failure aggregation.

## Verification plan

- **Highest level achievable:** L2 + race detector. L5 audit-chain verify is reachable in
  TC-086-05 only when `AGENT_BUILDER_AUDIT_BIN` is set (else `L5 binary-deferred`). A full
  live multi-worker run with real exec-sandbox + real workers is L6, operator-deferred.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/...
  go test -count=1 ./internal/channel/worker/...
  make fitness-supervisor-isolation
  make fitness-orchestrator-no-executor
  make fitness-no-self-repo-sink
  make check
  ```
  Expected: `ok` (no races); fitness `PASS` lines; `All checks passed.`

## Out of scope

- Worker quota / max-N-concurrent limits — a follow-on tuning task.
- Cross-host worker dispatch (all workers run on the same host).
- Worker priority / scheduling order (fan-out is unordered; outcome aggregation is ordered).
- Full out-of-process transport-on-dispatch wiring beyond making dispatch concurrent +
  race-safe; the `DispatchFunc` seam keeps the live sandbox launch behind a stub for L2.
