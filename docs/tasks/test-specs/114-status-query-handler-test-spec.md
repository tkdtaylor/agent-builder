# Test spec — Task 114: Status-query handler + immediate reporter answer

**Linked task:** `docs/tasks/backlog/114-status-query-handler.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §3 (live status registry; a `status` message reads the registry
and the Reporter answers **immediately**, never blocking on `Handle`).

## Context

ADR 054 §3 makes the control plane "responsive" by answering `status` from the registry
**without** waiting on goal processing. This task implements the `status` handler body that
task 113 routes the `MsgStatus` kind to: it reads the goalID-keyed registry (task 112) and
renders a reply over the `supervisor.Reporter` — fleet status (no goalID) or per-goal status
(with goalID) including sub-goal progress.

### Status never blocks on `Handle` (ADR 054 §3 — load-bearing)

The registry is a **projection** for observability; it is **not** the source of truth for
control flow (the PlanStore remains that). A `status` read therefore touches only the
mutex-guarded registry and the Reporter — it must **never** call `Handle`/`Resume` or wait
on any goal actor. This is exactly what makes status answerable while goals run. The reply is
a **snapshot** at read time (status is eventually-consistent — ADR 054 Consequences); the
test asserts the snapshot content, not transactional consistency.

### Render shapes

- **Fleet status (empty GoalID):** a summary over all registered goals — each goalID with its
  `GoalState`, suitable for an operator scan. At minimum: one line/entry per live goal with
  its goalID and state.
- **Per-goal status (with GoalID):** the goal's `GoalState` plus per-sub-goal progress
  (`SubGoals[i]`: name/recipe + running/done/failed).
- **Unknown goalID:** the graceful "no such goal" report is task 113's router responsibility;
  this task's per-goal renderer is only reached for a **known** goalID. (A defensive
  assertion that the renderer does not panic on an empty `SubGoals` slice is included.)

## Requirements coverage

| Req ID      | Description                                                                                                       | Test cases             |
|-------------|------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-114-01  | A `status` message reads the registry and answers over the Reporter **without** calling `Handle`/`Resume` or blocking on a goal actor | TC-114-01              |
| REQ-114-02  | Fleet status (empty GoalID) renders one entry per live goal with its `GoalState`                                | TC-114-02              |
| REQ-114-03  | Per-goal status (with GoalID) renders the `GoalState` plus per-sub-goal progress (name/recipe + running/done/failed) | TC-114-03              |
| REQ-114-04  | The status answer is immediate while a goal runs: a `status` issued mid-dispatch returns a reply before the goal terminates | TC-114-04              |

---

## Test cases

### TC-114-01 — Status reads the registry, never calls `Handle`/`Resume` (L2)

- **Requirement:** REQ-114-01
- **Level:** L2 (unit test with a spy orchestrator + spy Reporter)

**Input:** Populate the registry with one goal (`goal-1`, state `Dispatching`). Wrap the
orchestrator in a spy that records any `Handle`/`Resume` call. Invoke the status handler for
`goal-1`.

**Expected output (assertions):**
- The spy Reporter receives exactly one reply (non-empty text mentioning `goal-1`).
- The spy orchestrator records **zero** `Handle` and **zero** `Resume` calls — the status
  path never touched goal processing.

---

### TC-114-02 — Fleet status renders one entry per live goal (L2)

- **Requirement:** REQ-114-02
- **Level:** L2 (unit test)

**Input:** Registry with `goal-1` (`Planning`), `goal-2` (`Dispatching`), `goal-3` (`Done`).
Invoke the status handler with **empty** GoalID.

**Expected output (assertions):**
- The Reporter reply text contains `goal-1`, `goal-2`, and `goal-3`, each paired with its
  state string (`Planning`, `Dispatching`, `Done` respectively).
- Exactly three goal entries are rendered (no duplicates, none dropped).

---

### TC-114-03 — Per-goal status renders sub-goal progress (L2)

- **Requirement:** REQ-114-03
- **Level:** L2 (unit test)

**Input:** Registry with `goal-7` (`Dispatching`) carrying two sub-goals:
`SubGoals[0]` = {name `auth`, recipe `coding-agent`, **done**}, `SubGoals[1]` = {name
`docs`, recipe `docs-fix`, **running**}. Invoke the status handler for `goal-7`.

**Expected output (assertions):**
- The reply contains `goal-7` and its state `Dispatching`.
- The reply renders both sub-goals: `auth`/`coding-agent` as **done** and `docs`/`docs-fix`
  as **running** (substring assertions on each sub-goal's name and status).
- Defensive: invoking the per-goal renderer for a known goal with an **empty** `SubGoals`
  slice does not panic and renders the goal's state with no sub-goal lines.

---

### TC-114-04 — Status answers immediately while a goal runs (L2/L6)

- **Requirement:** REQ-114-04
- **Level:** L2 (unit, control-plane integration) + L6 (live binary)

**Input (L2):** Start `goal-1` whose sub-goal dispatch is held at a latch (state
`Dispatching`). While the dispatch is held, deliver a `status goal-1` message through the
control loop.

**Expected output (L2):**
- The Reporter receives the `goal-1` status reply (state `Dispatching`) **before** the test
  releases the held dispatch latch — proving the answer did not wait on `Handle`. (Bounded
  wait on the Reporter spy with a timeout; the reply arrives while the goal is provably still
  mid-dispatch.)

**Input (L6):** Submit a long-running goal over stdin to the live binary, then send `status`
mid-flight.

**Expected output (L6):**
- An immediate registry-projected reply appears in stdout/logs reflecting the goal's current
  state, while the goal is still running. Record the timestamped status reply in the verify
  commit.

---

## Verification plan

- **Highest level achievable: L6** — submit a long goal, then a `status` mid-flight, observe
  an immediate registry-projected reply on the live binary. L2 (render + no-block unit tests)
  is the CI ceiling.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/cli/... ./internal/orchestrator/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 (operator-run, dev host):** run `agent-builder orchestrate` with a long goal; send
  `status` over stdin mid-flight; observe the immediate reply with the goal's live state +
  sub-goal progress. Record the reply in the verify commit.

## Out of scope

- The status **routing** (the `MsgStatus` kind dispatch + unknown-goalID graceful report) —
  task 113. This task is the handler body the router invokes.
- Writing the registry state transitions — task 112 (this task only **reads** the registry).
- Apply-info / pending-info queue rendering in the status — task 115 (if the pending-info
  queue surfaces in status, that wiring lands with 115; this task renders state + sub-goal
  progress only).
- Cancellation state rendering — the `Cancelled` state's wiring lands with task 116; this
  renderer should tolerate it (render the state string) but does not implement cancellation.
- Telegram-formatted status replies — task 117 (this task answers over the generic
  `Reporter`).
