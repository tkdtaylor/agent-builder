# Test spec — Task 086: Multi-worker concurrent dispatch

**Linked task:** `docs/tasks/backlog/086-multi-worker-concurrent-dispatch.md`
**Written:** 2026-06-27
**Status:** stub — blocked by tasks 081 and 083

## Context

The orchestrator must coordinate N workers concurrently. The single-worker path
(one recipe → runtime → supervisor) already exists. This task adds the coordination
layer: the orchestrator dispatches multiple workers in parallel, tracks their state,
handles partial failures, and aggregates results.

This task is **blocked by task 081** (orchestrator core must exist and dispatch
at least one worker) and **task 083** (agent-mesh transport must be the dispatch
mechanism before concurrency can be added on top of it).

**Detailed task shape is deferred** pending those prerequisites.

## Requirements coverage (preliminary)

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-086-01 | Orchestrator dispatches N workers concurrently; each in its own exec-sandbox   | TC-086-01  |
| REQ-086-02 | A worker failure does not halt other in-flight workers                          | TC-086-02  |
| REQ-086-03 | Aggregated result reflects per-worker outcomes (success + failure mix)          | TC-086-03  |
| REQ-086-04 | Concurrent workers share no mutable state (no data races under -race)           | TC-086-04  |
| REQ-086-05 | Fleet audit chain covers all N concurrent workers                               | TC-086-05  |

## Pre-implementation checklist

- [ ] Task 081 merged (orchestrator core)
- [ ] Task 083 merged (agent-mesh transport)
- [ ] Concurrency model decided (goroutines + channels vs worker-pool vs task queue)
- [ ] Partial-failure handling policy defined (fail-fast vs best-effort)
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-086-01 — Orchestrator dispatches 3 workers concurrently

- **Requirement:** REQ-086-01
- **Level:** L2 (unit test with stub worker executors)
- **Status:** stub

**Input:** Orchestrator receives a plan with 3 approved sub-goals.

**Expected output:**
- All 3 workers are started before any completes (concurrent dispatch confirmed via
  timing or stub call ordering).
- Each worker runs in its own exec-sandbox (separate `sandboxBox.Create` calls).

---

### TC-086-02 — One worker failure does not halt others

- **Requirement:** REQ-086-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Worker 1 of 3 fails immediately; workers 2 and 3 are still running.

**Expected output:**
- Workers 2 and 3 complete normally.
- The aggregated result marks worker 1 as failed and workers 2/3 as successful.

---

### TC-086-03 — Aggregated result reflects per-worker outcomes

- **Requirement:** REQ-086-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** 2 workers succeed, 1 fails.

**Expected output:**
- Orchestrator reports 2 successes and 1 failure.
- Report is delivered through the channel to the human.

---

### TC-086-04 — No data races under -race (concurrent workers)

- **Requirement:** REQ-086-04
- **Level:** L2 (race detector)
- **Status:** stub

**Input:** `go test -race -count=1 ./internal/orchestrator/...` with a test that
spawns 5 concurrent mock workers.

**Expected output:**
- `go test -race` exits 0; no race conditions reported.

---

### TC-086-05 — Fleet audit chain covers all N concurrent workers

- **Requirement:** REQ-086-05
- **Level:** L2 (unit test with FakeSink)
- **Status:** stub

**Input:** 3 concurrent workers all complete.

**Expected output:**
- The audit chain includes events for all 3 workers (spawn + complete per worker).
- Chain is valid (`audit-trail verify` returns `valid=true`).

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 (unit tests with race detector). L5 requires a
  live multi-worker orchestrator run with real exec-sandbox and real workers.
- **L2 harness command (to be confirmed):**
  ```
  go test -race -count=1 ./internal/orchestrator/...
  ```
  Expected: `ok` (no races).

## Out of scope

- Worker quota / concurrency limits (max-N-concurrent) — a follow-on tuning task.
- Cross-host worker dispatch (all workers run on the same host in this task).
- Worker priority / scheduling order.
