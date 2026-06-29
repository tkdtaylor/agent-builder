# Task 114: Status-query handler + immediate reporter answer

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Implement the `status` handler body that task 113 routes the `MsgStatus` kind to: read the
goalID-keyed status registry (task 112) and render a reply over `supervisor.Reporter` —
fleet status (no goalID) or per-goal status (with goalID) including sub-goal progress. The
answer is **immediate**: it reads the registry and answers **without** ever calling
`Handle`/`Resume` or blocking on a goal actor. This is what makes the control plane
responsive while goals run.

## Context

ADR 054 (the authoritative design) §3 specifies the live status registry and the
immediate-answer property. Task 112 builds the registry and writes its transitions; task 113
routes the `MsgStatus` kind. This task is the read-and-render handler in between.

### Status never blocks on `Handle` (ADR 054 §3 — load-bearing)

The registry is a **projection** for observability — **not** the source of truth for control
flow (the PlanStore remains that). A `status` read touches only the mutex-guarded registry
and the Reporter; it must never call `Handle`/`Resume` or wait on any goal actor. That is
exactly what lets status be answered while a goal is mid-dispatch. The reply is a
**snapshot** at read time (status is eventually-consistent — ADR 054 Consequences); a goal
may transition the instant after.

### Render shapes

- **Fleet status (empty GoalID):** one entry per registered goal — goalID + `GoalState` —
  for an operator scan.
- **Per-goal status (with GoalID):** the goal's `GoalState` plus per-sub-goal progress
  (`SubGoals[i]`: name/recipe + running/done/failed).
- **Unknown goalID:** the graceful "no such goal" report is task 113's router job; this
  task's per-goal renderer is reached only for a **known** goalID, but must tolerate an empty
  `SubGoals` slice without panicking.

### Security / invariant note

Status is read-only over the registry; it adds no path around any gate and writes no control
state. The `Cancelled` state (added in task 116) should render as a state string if present —
this renderer is forward-tolerant of it but does not implement cancellation.

## Requirements

| Req ID      | Description                                                                                                       | Priority   |
|-------------|------------------------------------------------------------------------------------------------------------------|------------|
| REQ-114-01  | A `status` message reads the registry and answers over the Reporter **without** calling `Handle`/`Resume` or blocking on a goal actor | must have |
| REQ-114-02  | Fleet status (empty GoalID) renders one entry per live goal with its `GoalState`                                | must have  |
| REQ-114-03  | Per-goal status (with GoalID) renders `GoalState` plus per-sub-goal progress (name/recipe + running/done/failed) | must have  |
| REQ-114-04  | The status answer is immediate while a goal runs: a `status` issued mid-dispatch returns before the goal terminates | must have  |

## Readiness gate

- [ ] Task 112 merged (the status registry + its lifecycle transitions to read)
- [ ] Task 113 merged (the `MsgStatus` routing that invokes this handler)
- [x] Task 081 merged (`supervisor.Reporter` the handler answers over)
- [x] ADR 054 §3 read

## Acceptance criteria

- [ ] [REQ-114-01] TC-114-01: status handler for `goal-1` (`Dispatching`) → exactly one Reporter reply mentioning `goal-1`; spy orchestrator records **zero** `Handle`/`Resume`
- [ ] [REQ-114-02] TC-114-02: empty-GoalID over 3 goals → reply contains all three goalIDs paired with their state strings; exactly three entries
- [ ] [REQ-114-03] TC-114-03: per-goal `goal-7` with two sub-goals → reply renders state + `auth/coding-agent=done` + `docs/docs-fix=running`; empty `SubGoals` does not panic
- [ ] [REQ-114-04] TC-114-04: `status goal-1` while dispatch held at a latch → Reporter reply (state `Dispatching`) arrives **before** the latch is released; L6 live `status` mid-flight returns immediately

## Verification plan

- **Highest level achievable: L6** — submit a long goal, then a `status` mid-flight, observe
  an immediate registry-projected reply on the live binary. L2 (render + no-block unit tests,
  `-race`) is the CI ceiling.
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
  sub-goal progress. Record the reply (with a timestamp showing it preceded goal completion)
  in the verify commit.

## Modules touched

- `internal/cli` (the status handler the router invokes + a small render helper for fleet /
  per-goal output).
- *(Possibly)* `internal/orchestrator` — only if reading sub-goal progress needs a registry
  accessor method added next to the registry type; prefer exposing a read-only snapshot
  accessor rather than the CLI reaching into registry internals.

(One code module — `internal/cli` — plus at most a small read accessor on the
`internal/orchestrator` registry type. Within the at-most-two-modules rule; no spec/data-model
change beyond what 112/113 already record, since status is a read-only projection of existing
state.)

## Out of scope

- The status **routing** + unknown-goalID graceful report — task 113.
- Writing the registry state transitions — task 112 (this task only reads).
- Pending-info-queue rendering in status — task 115 (lands with the info feature).
- Cancellation — task 116 (this renderer tolerates the `Cancelled` state but does not
  implement it).
- Telegram-formatted status replies — task 117 (this task answers over the generic
  `Reporter`).

## Dependencies

- **Task 112 — HARD dependency** (the registry to read).
- **Task 113 — HARD dependency** (the `MsgStatus` routing that invokes this handler).
- Task 081 (`Reporter`) — merged.
- ADR 054 §3 — the authoritative design.
- **Mutually independent of tasks 115 and 116** — different control-loop handler + different
  lower seam; may run in parallel on a separate branch once 113 merges.
