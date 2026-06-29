# Test spec — Task 113: Inbound message protocol + command router

**Linked task:** `docs/tasks/backlog/113-inbound-message-protocol.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 054 §2 (evolve `GoalSource` into a typed `MessageSource`; the
line-oriented local-test grammar; the control-loop command router).

## Context

ADR 054 §2 generalizes the inbound operator seam from "goal-only"
(`supervisor.GoalSource.Next() (Task, bool, error)`) to **typed messages** via a new seam
`supervisor.MessageSource`. It introduces `Message`/`MessageKind`
(`new-goal`/`status`/`info`/`cancel`), generalizes the env/stdin source into a line-oriented
`MessageSource`, and adds the control-loop **router** that dispatches each kind. The router
is the only reader of the inbound seam (no concurrent `Next()` races — ADR 054 §1).

### Why a new seam, not a mutated `GoalSource` (ADR 054 §2 — load-bearing)

`GoalSource` MUST stay intact: it is **also** the per-worker, in-box recipe task source
(`runtime.Run`'s recipe-driven `GoalSourceFactory` path), a *different* inbound seam that
must not be disturbed. Introduce `supervisor.MessageSource` as a **new** interface
alongside `GoalSource`; do not change `GoalSource`'s signature.

```go
type MessageKind int
const (
    MsgNewGoal MessageKind = iota
    MsgStatus
    MsgInfo
    MsgCancel
)

type Message struct {
    Kind   MessageKind
    GoalID string
    Goal   supervisor.Task // populated for MsgNewGoal
    Text   string          // info payload / free-form
}

type MessageSource interface {
    Next() (Message, bool, error)
}
```

### The line-oriented local-test grammar (ADR 054 §2 — load-bearing)

The env/stdin source must keep working so the operator can drive the control plane locally
without Telegram — this is the seam every L5/L6 verification of this work hangs off.
Generalize `newEnvGoalSource` into a `MessageSource` that parses a line grammar from
stdin/env:

- A **bare line** (or `AGENT_BUILDER_GOAL_SPEC`) → `MsgNewGoal` (the line is the goal spec).
- `status` (optionally `status <goalID>`) → `MsgStatus` (empty GoalID = fleet).
- `info <goalID> <text>` → `MsgInfo` (GoalID + remaining text as payload).
- `cancel <goalID>` → `MsgCancel`.
- EOF / no-more-input → `ok=false`; the control plane drains and exits.

### Routing + addressing (ADR 054 §2/§3)

Follow-up messages are addressed by **`GoalID`** (the PlanStore/registry key). The router
dispatches by kind: `MsgNewGoal` spawns a goal actor (task 112's path); `MsgStatus` is
answered directly by the control loop reading the registry (the handler body itself is task
114 — this task routes the kind and proves the dispatch reaches the right place);
`MsgInfo`/`MsgCancel` are routed to the addressed goal's per-goal **command mailbox** (the
buffered channel keyed by goalID in the registry; the mailbox must be created
**before** the actor is registered — register-then-start — to avoid racing actor startup).
A `status`/`info`/`cancel` for an **unknown goalID** is answered with a graceful "no such
goal" report — **never a panic** (fail-loud-but-graceful).

This task wires the router to deliver to the mailbox / answer unknown-goal gracefully; the
**handler bodies** for status (114), info-fold (115), and cancel-teardown (116) land in
their own tasks. Here the assertions are about correct **parsing**, **kind dispatch**, and
**addressing** (delivery to the right mailbox / graceful unknown-goal report), with stub
handlers recording what they received.

## Requirements coverage

| Req ID      | Description                                                                                                                | Test cases             |
|-------------|---------------------------------------------------------------------------------------------------------------------------|------------------------|
| REQ-113-01  | `supervisor.MessageSource` is a **new** seam (not a mutation of `GoalSource`); `GoalSource` signature unchanged           | TC-113-01              |
| REQ-113-02  | The line grammar parses bare-line→`MsgNewGoal`, `status[/<id>]`→`MsgStatus`, `info <id> <text>`→`MsgInfo`, `cancel <id>`→`MsgCancel`; EOF→`ok=false` | TC-113-02              |
| REQ-113-03  | The env/stdin local-test path still works end-to-end (a bare line / `AGENT_BUILDER_GOAL_SPEC` produces a new goal)        | TC-113-03              |
| REQ-113-04  | The control-loop router dispatches by kind: new-goal→actor; status→registry read; info/cancel→addressed goal's mailbox    | TC-113-04              |
| REQ-113-05  | `info`/`cancel`/`status` for an **unknown goalID** → graceful "no such goal" report, never a panic                       | TC-113-05              |
| REQ-113-06  | Mailboxes are created **before** actor registration (register-then-start); a `cancel`/`info` arriving at actor-startup is not lost/raced | TC-113-06              |

---

## Test cases

### TC-113-01 — `MessageSource` is a new seam; `GoalSource` is untouched (L2)

- **Requirement:** REQ-113-01
- **Level:** L2 (compile-time + signature assertion)

**Input:** Build the package and inspect the seams.

**Expected output (assertions):**
- `supervisor.MessageSource` exists with `Next() (Message, bool, error)`; a compile-time
  `var _ supervisor.MessageSource = (*<envMessageSource>)(nil)` assertion holds for the
  env/stdin source.
- `supervisor.GoalSource.Next()` still has signature `() (Task, bool, error)` (unchanged) —
  a compile-time assertion against an existing `GoalSource` implementer confirms it was not
  mutated. (Protects the in-box recipe `GoalSourceFactory` path.)

---

### TC-113-02 — Line grammar parses each kind correctly (L2)

- **Requirement:** REQ-113-02
- **Level:** L2 (table-driven unit test over the parser)

**Input → Expected (table):**

| Input line                          | `Kind`        | `GoalID`   | `Goal.Spec` / `Text`                |
|-------------------------------------|---------------|------------|-------------------------------------|
| `add rate limiting to the API`      | `MsgNewGoal`  | (assigned) | `Goal.Spec == "add rate limiting to the API"` |
| `status`                            | `MsgStatus`   | `""`       | —                                   |
| `status goal-7`                     | `MsgStatus`   | `goal-7`   | —                                   |
| `info goal-7 also handle retries`   | `MsgInfo`     | `goal-7`   | `Text == "also handle retries"`     |
| `cancel goal-7`                     | `MsgCancel`   | `goal-7`   | —                                   |

- EOF (closed stdin / no more lines) → `Next()` returns `ok=false`, `nil` error.
- Malformed control line (e.g. `cancel` with no goalID) → either a parse error surfaced on
  `Next()` **or** routed to the unknown/invalid path that yields a graceful report — assert
  it does **not** panic and does **not** silently become a `new-goal`.

---

### TC-113-03 — env/stdin local-test path still produces a new goal (L2/L6)

- **Requirement:** REQ-113-03
- **Level:** L2 (unit) + L6 (live binary, operator)

**Input (L2):** Set `AGENT_BUILDER_GOAL_SPEC="ship the widget"`; construct the env
`MessageSource`; call `Next()`.

**Expected (L2):**
- First `Next()` → `Message{Kind: MsgNewGoal, Goal.Spec: "ship the widget"}`, `ok=true`.
- Second `Next()` (no more input) → `ok=false`.

**Input (L6):** Run `agent-builder orchestrate` with `AGENT_BUILDER_GOAL_SPEC` set (no
Telegram); observe the goal planned + dispatched on the live binary — confirming the
local-first seam survived the generalization.

---

### TC-113-04 — Router dispatches by kind to the right place (L2)

- **Requirement:** REQ-113-04
- **Level:** L2 (unit test with stub handlers + a stub registry recording mailbox delivery)

**Input:** A scripted `MessageSource` yields, in order: a `new-goal` (`goal-1`), a `status`
(empty GoalID), an `info goal-1 …`, a `cancel goal-1`, then `ok=false`. Register `goal-1`'s
actor with a recording mailbox.

**Expected output (assertions):**
- `new-goal` → a goal actor for `goal-1` is spawned (task-112 path; spy confirms one actor
  start for `goal-1`).
- `status` (empty GoalID) → the router invokes the status path (a stub status handler
  records being called with empty GoalID = fleet) **without** entering `goal-1`'s mailbox.
- `info goal-1 …` → delivered to `goal-1`'s **command mailbox** (the recording mailbox
  receives an `MsgInfo` with the text).
- `cancel goal-1` → delivered to `goal-1`'s **command mailbox** (the recording mailbox
  receives an `MsgCancel`).
- After `ok=false` the router loop returns and the control plane drains.

---

### TC-113-05 — Unknown goalID → graceful "no such goal", never a panic (L2)

- **Requirement:** REQ-113-05
- **Level:** L2 (unit test with a spy Reporter)

**Input:** With no goal registered, feed `status goal-X`, `info goal-X foo`, and
`cancel goal-X` in turn.

**Expected output (assertions):**
- Each produces a Reporter call whose text indicates "no such goal" / unknown goalID
  (substring assertion) — a graceful report, not a crash.
- No panic; the router continues to the next message after each.
- No mailbox is created or written for `goal-X` (the registry has no entry for it).

---

### TC-113-06 — Mailbox created before actor registration (register-then-start) (L2)

- **Requirement:** REQ-113-06
- **Level:** L2 (unit test, `-race`)

**Input:** Drive a `new-goal` (`goal-2`) immediately followed by a `cancel goal-2` from the
same source, with the goal actor's lifecycle held at a latch right after registration but
before it begins planning.

**Expected output (assertions):**
- The `cancel goal-2` is delivered to `goal-2`'s mailbox **without loss** — the mailbox
  exists at the moment the cancel routes (because registration created the mailbox before
  starting the actor). The recording mailbox receives the `MsgCancel`.
- No data race reported under `-race` on the registry's mailbox map (register-then-start
  ordering holds).

---

## Verification plan

- **Highest level achievable: L6** — drive all four message kinds over stdin against the
  live binary and observe each routed correctly. L2 (parser + router unit tests, `-race`) is
  the CI ceiling.
- **L2 harness commands:**
  ```
  go test -race -count=1 ./internal/supervisor/... ./internal/cli/...
  ```
  Expected: `ok` each, no race report.
- **L3 fitness commands:**
  ```
  make fitness-supervisor-isolation
  make check
  ```
  Expected: `PASS …`; `All checks passed.` (The new `MessageSource` seam lives in
  `internal/supervisor` and must not pull executor/web-fetch imports into the supervisor
  graph — F-003.)
- **L6 (operator-run, dev host):** run `agent-builder orchestrate`; over stdin send a bare
  goal line, then `status`, then `info <goalID> <text>`, then `cancel <goalID>`; observe
  each routed (a new goal starts; status answers; info/cancel reach the goal). Record the
  four interactions in the verify commit.

## Out of scope

- The **status handler body** (registry render + immediate Reporter answer) — task 114 (this
  task routes the `MsgStatus` kind to the handler; the handler is 114's).
- The **info-fold / pending-info queue / amendment sub-goal** behavior — task 115 (this task
  delivers `MsgInfo` to the mailbox; the fold is 115's).
- The **cancellation teardown** (`context.Context` thread, `box.Kill`/`Teardown`) — task 116
  (this task delivers `MsgCancel` to the mailbox; the teardown is 116's).
- Telegram emitting typed messages — task 117 (this task generalizes the **env/stdin**
  source).
- Any change to `GoalSource`'s signature or the in-box `GoalSourceFactory` path.
