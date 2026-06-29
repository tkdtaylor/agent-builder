# Test spec — Task 115: Apply-info-at-checkpoint (queue + fold + amendment sub-goal)

**Linked task:** `docs/tasks/backlog/115-apply-info-at-checkpoint.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §4 (checkpoint-augment semantics; queue-don't-interrupt;
fold-at-next-checkpoint; amendment sub-goal `G/amend-N`; running workers never mutated
mid-task).

## Context

ADR 054 §4 implements **product decision 1** (already made — do not re-litigate): new info on
an in-flight goal is *queued* and folded into that goal's **next planning/approval
checkpoint**, or spawns an amendment sub-goal. Already-running sandboxed sub-goal workers
finish as-is — never killed mid-task by an info message. Task 113 delivers the `MsgInfo` to
the goal's command mailbox; this task implements what the actor does with it.

### Checkpoint, defined precisely in this codebase (ADR 054 §4 — load-bearing)

A **checkpoint** is a point where the orchestrator is **about to (re)plan or is paused
awaiting approval**: concretely the **`require_approval` pause** in `Handle` (the plan sits
in the PlanStore, state `AwaitingApproval`, dispatch not yet begun) and the **pre-plan
boundary** of any re-plan. It is **NOT** a point inside an already-dispatched worker's run.

### Queue, don't interrupt (ADR 054 §4)

An `info` message for goalID G appends its text to a per-goal **pending-info queue** in the
registry. It does **not** touch any running worker. The queue is read **only** at checkpoint
boundaries, never inside the dispatch goroutine — this is the structural guarantee that
running workers finish as-is.

### Fold at the next checkpoint (ADR 054 §4)

- **If `AwaitingApproval`:** queued info is surfaced *with* the approval solicitation (the
  operator sees the amended context before approving); on `Resume`-approve the actor re-runs
  `planner.Plan` on the goal text **augmented** with the info and **replaces the stored
  plan** before dispatch.
- **If already dispatched** (no upcoming natural checkpoint): the info **spawns an amendment
  sub-goal** — a new goal actor for `G/amend-1` carrying the info as its goal text, gated
  through the **normal** spawn-plan/spawn-worker policy + self-repo bright-line path. This
  keeps "info is folded at *a* checkpoint" true even past the approval gate, without mutating
  a running worker.

### Confirmation invariant (ADR 054 §4 — assert it)

Running workers are **never mutated mid-task** under either branch. The only state an info
message writes synchronously is the registry's pending-info queue; everything else happens
at a checkpoint the actor controls.

### Security invariants carried forward (ADR 054 §6)

The amendment sub-goal goes through the **same** `dispatchOne` gate as any other sub-goal:
the self-repo bright line and the policy fail-closed gates fire on it; an info-spawned
amendment cannot bypass them. The `G/amend-N` ID scheme must stay collision-free and the
registry/PlanStore must tolerate the derived IDs (ADR 054 Consequences).

## Requirements coverage

| Req ID      | Description                                                                                                                  | Test cases             |
|-------------|-----------------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-115-01  | An `info` message appends to a per-goal pending-info queue in the registry; it touches no running worker (queue-don't-interrupt) | TC-115-01              |
| REQ-115-02  | At an `AwaitingApproval` checkpoint, queued info is surfaced with the approval solicitation                                  | TC-115-02              |
| REQ-115-03  | On `Resume`-approve with queued info, the actor re-plans the **augmented** goal and replaces the stored plan before dispatch | TC-115-03              |
| REQ-115-04  | For an already-dispatched goal, info spawns an amendment sub-goal `G/amend-N` gated through the normal spawn-plan/spawn-worker + self-repo path | TC-115-04              |
| REQ-115-05  | Running sub-goal workers are **never mutated mid-task** under either branch (the only synchronous write is the info queue)    | TC-115-05              |
| REQ-115-06  | The amendment sub-goal passes the self-repo bright line + policy gate like any sub-goal (no bypass); `G/amend-N` IDs are collision-free | TC-115-06              |

---

## Test cases

### TC-115-01 — `info` appends to the pending-info queue, touches no worker (L2)

- **Requirement:** REQ-115-01
- **Level:** L2 (unit test)

**Input:** A goal `G` with one sub-goal **held in dispatch** at a latch (worker running).
Deliver `MsgInfo{GoalID: "G", Text: "also add a metrics endpoint"}` to `G`'s mailbox.

**Expected output (assertions):**
- The registry's pending-info queue for `G` contains exactly the one entry
  `"also add a metrics endpoint"`.
- The held worker's dispatch is **not** signalled/cancelled/restarted by the info message —
  the dispatch spy records no extra call and the latch is still held (the worker is
  untouched). Releasing the latch afterward lets the original sub-goal complete normally.

---

### TC-115-02 — Queued info surfaced with the approval solicitation (L2)

- **Requirement:** REQ-115-02
- **Level:** L2 (unit test with a spy Reporter)

**Input:** A goal `G` paused at `AwaitingApproval` (plan in PlanStore, dispatch not begun).
Deliver `MsgInfo{GoalID: "G", Text: "must support IPv6"}`. Then have the actor reach/redo its
approval solicitation.

**Expected output (assertions):**
- The approval solicitation reply sent over the Reporter **includes** the queued info text
  `"must support IPv6"` (substring) alongside the plan summary — the operator sees the
  amended context before approving.

---

### TC-115-03 — On approve, re-plan the augmented goal + replace the stored plan (L2)

- **Requirement:** REQ-115-03
- **Level:** L2 (unit test with a spy planner + PlanStore)

**Input:** Goal `G` (`Spec: "build the import pipeline"`) at `AwaitingApproval` with a stored
plan `P0`. Queue `info` `"also validate CSV headers"`. `Resume(Approval{GoalID:"G",
allow})`.

**Expected output (assertions):**
- The spy `planner.Plan` is called a **second** time with a goal whose text is the original
  **augmented** with the info — assert the planner input contains both
  `"build the import pipeline"` and `"also validate CSV headers"`.
- The PlanStore for `G` is **replaced** with the re-planned `P1` (not `P0`) **before**
  dispatch — the dispatch spy is called with `P1`'s sub-goals, not `P0`'s.
- The pending-info queue for `G` is **drained** after folding (no double-application on a
  subsequent checkpoint).

---

### TC-115-04 — Already-dispatched goal: info spawns `G/amend-1` through the normal gates (L2)

- **Requirement:** REQ-115-04
- **Level:** L2 (unit test)

**Input:** Goal `G` already past its approval gate and **dispatching** (no upcoming natural
checkpoint). Deliver `MsgInfo{GoalID: "G", Text: "add a rollback path"}`.

**Expected output (assertions):**
- A **new goal actor** is spawned for goalID `G/amend-1` whose goal text is
  `"add a rollback path"`.
- `G/amend-1` is planned and its sub-goals are dispatched **through the normal
  spawn-plan/spawn-worker path** (the policy gate + self-repo bright line are invoked for
  it — spy confirms `decideSpawn`/bright-line ran for the amendment's sub-goals).
- `G`'s original in-flight workers are unaffected (the dispatch spy for `G`'s original
  sub-goals records no change; they complete as-is).
- A second info on `G` while `G/amend-1` is live yields `G/amend-2` (monotonic, collision-
  free).

---

### TC-115-05 — Running workers never mutated mid-task (L2)

- **Requirement:** REQ-115-05
- **Level:** L2 (unit test, both branches)

**Input:** (a) the `AwaitingApproval` branch of TC-115-03 and (b) the already-dispatched
branch of TC-115-04, each with a worker held at a latch during the info delivery.

**Expected output (assertions):**
- In **both** branches the held worker receives **no** cancel/restart/mutate signal as a
  result of the info — the only synchronous state change observable from the info delivery is
  the pending-info queue append. (Assert the worker latch is still held and the dispatch spy
  records no extra interaction with the running worker between info-delivery and
  checkpoint-fold.)
- Releasing the latch lets the original worker finish normally in both branches.

---

### TC-115-06 — Amendment passes the self-repo + policy gate; IDs collision-free (L2)

- **Requirement:** REQ-115-06
- **Level:** L2 (unit test)

**Input:** Deliver info to a dispatched goal `G` such that the amendment's planner produces a
sub-goal targeting the **own-repo** (`github.com/tkdtaylor/agent-builder`); a separate
amendment delivers a sub-goal that the stub policy **denies**.

**Expected output (assertions):**
- The own-repo amendment sub-goal is **not dispatched** — the self-repo bright line fires on
  `G/amend-N`'s sub-goal exactly as it does on any sub-goal (dispatch spy never called with
  the own-repo target).
- The policy-denied amendment sub-goal is **not dispatched** — `decideSpawn` deny fires and
  the SEC-003 deny-audit event is emitted (fail-closed; no path around the gate via the
  amendment route).
- Two amendments on the same `G` produce distinct IDs `G/amend-1`, `G/amend-2` (no collision
  in the registry or PlanStore).

---

## Verification plan

- **Highest level achievable: L6** — send `info` during an `AwaitingApproval` goal and
  observe the amended plan; send `info` to a dispatched goal and observe an amendment
  sub-goal, on the live binary. L2 (queue/fold/amendment unit tests, `-race`) is the CI
  ceiling.
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
- **L6 (operator-run, dev host):** run `agent-builder orchestrate` with `require_approval`
  on; submit a goal; while it is `AwaitingApproval`, send `info <goalID> <text>`; observe the
  info surfaced with the approval solicitation and, on approve, a re-planned/amended plan.
  Then submit a goal, let it dispatch, send `info`; observe a `G/amend-1` sub-goal spawned and
  gated. Record both in the verify commit.

## Out of scope

- The `MsgInfo` **routing** to the mailbox + unknown-goalID graceful report — task 113.
- The pending-info **queue type/field** placement in the registry is shared with task 112's
  registry (this task adds the queue semantics; the registry reserving room for it is 112's).
- Cancellation / teardown — task 116.
- The status renderer surfacing the pending-info queue (optional) is bounded to this task's
  approval-solicitation surfacing, not task 114's status render.
- Telegram-formatted info acks — task 117.
- Changing the `planner.Plan` interface or the `Resume`/`Approval` contract (the actor calls
  them as-is, just on an augmented goal text).
