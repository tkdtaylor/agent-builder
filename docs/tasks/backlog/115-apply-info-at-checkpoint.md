# Task 115: Apply-info-at-checkpoint (queue + fold + amendment sub-goal)

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Implement checkpoint-augment semantics for new info on an in-flight goal: add a per-goal
**pending-info queue** to the registry; surface queued info with the approval solicitation;
on approve, re-plan the goal with the info **folded** into the goal text and replace the
stored plan before dispatch; for already-dispatched goals, spawn an amendment sub-goal
`G/amend-N` through the **normal** spawn-plan/spawn-worker + self-repo gates. **Running
sub-goal workers are never mutated mid-task.**

## Context

ADR 054 (the authoritative design) §4 implements **product decision 1** (already made — do
not re-litigate): new info on an in-flight goal is *queued* and folded into that goal's next
planning/approval checkpoint, or spawns an amendment sub-goal. Already-running sandboxed
workers finish as-is. Task 113 delivers the `MsgInfo` to the goal's command mailbox; this
task is what the actor does with it.

### Checkpoint, defined precisely (ADR 054 §4 — load-bearing)

A **checkpoint** is a point where the orchestrator is **about to (re)plan or is paused
awaiting approval**: concretely the **`require_approval` pause** in `Handle` (plan in the
PlanStore, state `AwaitingApproval`, dispatch not begun) and the **pre-plan boundary** of any
re-plan. It is **NOT** a point inside an already-dispatched worker's run.

### Queue, don't interrupt (ADR 054 §4)

`info` for goalID G appends its text to a per-goal **pending-info queue** in the registry. It
touches no running worker. The queue is read **only** at checkpoint boundaries, never inside
the dispatch goroutine — the structural guarantee that running workers finish as-is.

### Fold at the next checkpoint (ADR 054 §4)

- **If `AwaitingApproval`:** queued info is surfaced *with* the approval solicitation; on
  `Resume`-approve the actor re-runs `planner.Plan` on the goal text **augmented** with the
  info and **replaces the stored plan** before dispatch.
- **If already dispatched** (no upcoming natural checkpoint): the info **spawns an amendment
  sub-goal** — a new goal actor for `G/amend-1` carrying the info as its goal text, gated
  through the **normal** spawn-plan/spawn-worker + self-repo policy path. This keeps "info is
  folded at *a* checkpoint" true past the approval gate without mutating a running worker.

### Confirmation invariant (ADR 054 §4 — assert it)

Running workers are **never mutated mid-task** under either branch. The only state an info
message writes synchronously is the registry's pending-info queue.

### Security invariants carried forward (ADR 054 §6)

The amendment sub-goal goes through the **same** `dispatchOne` gate as any sub-goal: the
self-repo bright line and the policy fail-closed gates fire on it; the SEC-003 deny-audit
rule holds; an info-spawned amendment cannot bypass any gate. The `G/amend-N` ID scheme must
stay collision-free and the registry/PlanStore must tolerate the derived IDs.

## Requirements

| Req ID      | Description                                                                                                                  | Priority   |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-115-01  | An `info` message appends to a per-goal pending-info queue; it touches no running worker (queue-don't-interrupt)             | must have  |
| REQ-115-02  | At an `AwaitingApproval` checkpoint, queued info is surfaced with the approval solicitation                                  | must have  |
| REQ-115-03  | On `Resume`-approve with queued info, the actor re-plans the **augmented** goal and replaces the stored plan before dispatch; queue drained | must have |
| REQ-115-04  | For an already-dispatched goal, info spawns amendment sub-goal `G/amend-N` through the normal spawn-plan/spawn-worker + self-repo path | must have |
| REQ-115-05  | Running sub-goal workers are **never mutated mid-task** under either branch                                                  | must have  |
| REQ-115-06  | The amendment passes the self-repo bright line + policy gate like any sub-goal (no bypass); `G/amend-N` IDs collision-free   | must have  |

## Readiness gate

- [ ] Task 112 merged (the registry + the goal-actor lifecycle the checkpoint hook lives in)
- [ ] Task 113 merged (the `MsgInfo` mailbox delivery this consumes)
- [x] Task 081 merged (`Handle`/`Resume`/`Approval`, PlanStore, `planner.Plan`)
- [x] Task 085 merged (`dispatchOne` self-repo bright line + policy gate + SEC-003 deny-audit)
- [x] ADR 054 §4 read; product decision 1 confirmed (apply-info-at-checkpoint)

## Acceptance criteria

- [ ] [REQ-115-01] TC-115-01: `info` to `G` (worker held) → pending-info queue has exactly the one entry; held worker untouched (no extra dispatch-spy call); original sub-goal completes on release
- [ ] [REQ-115-02] TC-115-02: `G` at `AwaitingApproval` + queued info → approval solicitation reply includes the info text
- [ ] [REQ-115-03] TC-115-03: on approve, `planner.Plan` called again with goal text containing original + info; PlanStore replaced with `P1`; dispatch uses `P1`; queue drained
- [ ] [REQ-115-04] TC-115-04: info to a dispatched `G` → `G/amend-1` actor (info as text) planned + gated through spawn path; `G`'s originals unaffected; second info → `G/amend-2`
- [ ] [REQ-115-05] TC-115-05: in both branches a held worker receives no cancel/restart/mutate from the info; only the queue append is observable; original finishes on release
- [ ] [REQ-115-06] TC-115-06: own-repo amendment sub-goal not dispatched (bright line fires); policy-denied amendment not dispatched (deny + SEC-003 audit); `G/amend-1`/`G/amend-2` distinct

## Verification plan

- **Highest level achievable: L6** — send `info` during an `AwaitingApproval` goal and observe
  the amended plan; send `info` to a dispatched goal and observe an amendment sub-goal, on the
  live binary. L2 (queue/fold/amendment unit tests, `-race`) is the CI ceiling.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/cli/... ./internal/orchestrator/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make fitness-orchestrator-no-executor
  make check
  ```
  Expected: `PASS …`; `All checks passed.`
- **L6 (operator-run, dev host):** run `agent-builder orchestrate` with `require_approval` on;
  submit a goal; while `AwaitingApproval`, send `info <goalID> <text>`; observe it surfaced
  with the solicitation and, on approve, a re-planned plan. Then submit a goal, let it
  dispatch, send `info`; observe a `G/amend-1` sub-goal spawned + gated. Record both in the
  verify commit.

## Modules touched

- `internal/cli` (the goal-actor checkpoint hook: drain the pending-info queue at the
  `AwaitingApproval`/pre-plan boundary; spawn the `G/amend-N` actor for the dispatched-goal
  branch; surface queued info with the approval solicitation).
- `internal/orchestrator` (the pending-info queue semantics on the registry; the re-plan
  helper that augments the goal text and replaces the stored plan; the `G/amend-N` ID
  derivation — all routed through the existing `dispatchOne`/`decideSpawn` gates, not around
  them).
- `docs/spec/behaviors.md` (the apply-info-at-checkpoint behavior + amendment sub-goal).

(Two code modules — `internal/cli` + `internal/orchestrator`. The queue + re-plan + amendment
ID logic lives next to the registry/PlanStore; the checkpoint hook + actor spawn live in the
control loop. Within the at-most-two-modules rule.)

## Out of scope

- The `MsgInfo` **routing** to the mailbox + unknown-goalID graceful report — task 113.
- The registry **reserving** the pending-info field — task 112 (this task adds the queue
  semantics on top).
- Cancellation / teardown — task 116.
- Surfacing the pending-info queue in `status` replies — task 114 renders state + sub-goal
  progress only; this task surfaces info **with the approval solicitation**, a distinct path.
- Telegram-formatted info acks — task 117.
- Changing the `planner.Plan` interface or the `Resume`/`Approval` contract.

## Dependencies

- **Task 112 — HARD dependency** (the registry + goal-actor lifecycle).
- **Task 113 — HARD dependency** (the `MsgInfo` mailbox delivery).
- Task 081 (`Handle`/`Resume`/`planner.Plan`/PlanStore), task 085 (`dispatchOne` gates) —
  merged.
- ADR 054 §4 — the authoritative design.
- **Mutually independent of tasks 114 and 116** — different control-loop handler + different
  lower seam; may run in parallel on a separate branch once 113 merges.
