# Task 113: Inbound message protocol + command router

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** backlog

## Goal

Generalize the inbound operator seam from goal-only (`supervisor.GoalSource`) to **typed
messages**: introduce a **new** `supervisor.MessageSource` seam plus typed
`Message`/`MessageKind` (`new-goal`/`status`/`info`/`cancel`), generalize the env/stdin
source into a line-oriented `MessageSource`, and add the control-loop **router** that
dispatches each kind (new-goal → goal actor; status → registry read; info/cancel → the
addressed goal's per-goal command mailbox; unknown goalID → graceful "no such goal"). The
env/stdin local-test path MUST keep working end to end.

## Context

ADR 054 (the authoritative design) §2 specifies the message protocol. The inbound seam today
carries goals only: `supervisor.GoalSource.Next() (Task, bool, error)`. There is no notion
of `status`/`info`/`cancel`. This task adds the typed protocol and the router that the
async control loop (task 112) dispatches through.

### A new seam, not a mutated `GoalSource` (ADR 054 §2 — load-bearing)

`GoalSource` MUST stay intact. It is **also** the per-worker, in-box recipe task source
(`runtime.Run`'s recipe-driven `GoalSourceFactory` path) — a *different* inbound seam that
must not be disturbed. Introduce `supervisor.MessageSource` as a **new** interface alongside
`GoalSource`; do not change `GoalSource`'s signature.

```go
type MessageKind int
const ( MsgNewGoal MessageKind = iota; MsgStatus; MsgInfo; MsgCancel )

type Message struct {
    Kind   MessageKind
    GoalID string          // addresses status/info/cancel; the new goal's ID for new-goal
    Goal   supervisor.Task // populated for MsgNewGoal
    Text   string          // info payload / free-form
}

type MessageSource interface { Next() (Message, bool, error) }
```

### The line-oriented local-test grammar (ADR 054 §2 — load-bearing)

The env/stdin source must keep working so the operator can drive the control plane locally
without Telegram — every L5/L6 verification of this work hangs off it. Generalize
`newEnvGoalSource` into a `MessageSource` that parses a line grammar from stdin/env:

- A bare line (or `AGENT_BUILDER_GOAL_SPEC`) → `MsgNewGoal` (the line is the goal spec).
- `status` (optionally `status <goalID>`) → `MsgStatus` (empty GoalID = fleet).
- `info <goalID> <text>` → `MsgInfo`.
- `cancel <goalID>` → `MsgCancel`.
- EOF / no-more-input → `ok=false`; the control plane drains and exits.

### Routing + addressing (ADR 054 §2/§3)

Goals are addressed by `GoalID` (the PlanStore/registry key). The control loop is the only
reader of the inbound seam (no concurrent `Next()` races). The router dispatches by kind:

- `MsgNewGoal` → spawn a goal actor (task 112's path).
- `MsgStatus` → answered directly by the control loop reading the registry (the **handler
  body** is task 114; this task routes the kind to the handler).
- `MsgInfo`/`MsgCancel` → routed to the addressed goal's per-goal **command mailbox** (a
  small buffered channel keyed by goalID in the registry). The mailbox MUST be created
  **before** the actor is registered (register-then-start) so a `cancel`/`info` arriving at
  actor startup is not lost or raced (ADR 054 §6 new-race-surface (b)).
- An `info`/`cancel`/`status` for an **unknown goalID** → a graceful "no such goal" report
  via the Reporter — **never a panic** (fail-loud-but-graceful, ADR 054 §2).

This task wires routing + addressing + graceful-unknown; the **handler bodies** for status
(114), info-fold (115), and cancel-teardown (116) land in their own tasks. Assertions here
are about parsing, kind dispatch, and addressing (delivery to the right mailbox / graceful
unknown-goal report), with stub handlers recording what they received.

### Security invariant carried forward

The `MessageSource` seam lives in `internal/supervisor` and must not pull
executor/LLM/web-fetch imports into the supervisor import graph (F-003 fitness). The router
is wiring in `internal/cli`; it adds no path around the policy/self-repo/audit gates — those
fire per sub-goal inside the actor regardless of how the message arrived.

## Requirements

| Req ID      | Description                                                                                                                | Priority   |
|-------------|---------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-113-01  | `supervisor.MessageSource` is a **new** seam (not a mutation of `GoalSource`); `GoalSource` signature unchanged            | must have  |
| REQ-113-02  | Line grammar parses bare-line→`MsgNewGoal`, `status[/<id>]`→`MsgStatus`, `info <id> <text>`→`MsgInfo`, `cancel <id>`→`MsgCancel`; EOF→`ok=false` | must have |
| REQ-113-03  | The env/stdin local-test path still works end-to-end (bare line / `AGENT_BUILDER_GOAL_SPEC` → a new goal)                  | must have  |
| REQ-113-04  | The control-loop router dispatches by kind: new-goal→actor; status→registry read; info/cancel→addressed goal's mailbox     | must have  |
| REQ-113-05  | `info`/`cancel`/`status` for an unknown goalID → graceful "no such goal" report, never a panic                            | must have  |
| REQ-113-06  | Mailboxes created **before** actor registration (register-then-start); a `cancel`/`info` at actor-startup is not lost/raced | must have  |

## Readiness gate

- [ ] Task 112 merged (the non-blocking control loop + status registry the router dispatches into)
- [x] Task 099 merged (`newEnvGoalSource` + the env/stdin inbound source this generalizes)
- [x] Task 081 merged (`supervisor.GoalSource`/`Task` + the orchestrator the actor drives)
- [x] ADR 054 §2 read

## Acceptance criteria

- [ ] [REQ-113-01] TC-113-01: `var _ supervisor.MessageSource = (*envMessageSource)(nil)`; `GoalSource.Next()` signature unchanged (compile-time assertion against an existing implementer)
- [ ] [REQ-113-02] TC-113-02: table-driven parse — each input line maps to the expected `Kind`/`GoalID`/`Text`; EOF→`ok=false`; malformed control line does not panic and does not become a `new-goal`
- [ ] [REQ-113-03] TC-113-03: `AGENT_BUILDER_GOAL_SPEC` → first `Next()` is `MsgNewGoal` with that spec; second `Next()` `ok=false`; L6 live binary plans+dispatches the goal
- [ ] [REQ-113-04] TC-113-04: scripted source (new-goal, status, info, cancel) → actor spawned; status handler invoked (empty=fleet); info/cancel delivered to the right mailbox
- [ ] [REQ-113-05] TC-113-05: `status/info/cancel goal-X` with no such goal → Reporter "no such goal" each; no panic; no mailbox created for `goal-X`
- [ ] [REQ-113-06] TC-113-06: `new-goal goal-2` then `cancel goal-2` with actor held at post-registration latch → cancel delivered to mailbox without loss; `-race` clean on the mailbox map

## Verification plan

- **Highest level achievable: L6** — drive all four message kinds over stdin against the live
  binary and observe each routed correctly. L2 (parser + router unit tests, `-race`) is the
  CI ceiling.
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
  Expected: `PASS fitness-supervisor-isolation`; `All checks passed.`
- **L6 (operator-run, dev host):** run `agent-builder orchestrate`; over stdin send a bare
  goal line, then `status`, then `info <goalID> <text>`, then `cancel <goalID>`; observe each
  routed (new goal starts; status answers; info/cancel reach the goal). Record the four
  interactions in the verify commit.

## Modules touched

- `internal/supervisor` (the new `MessageSource` seam + `Message`/`MessageKind` types —
  `GoalSource` untouched).
- `internal/cli` (generalize `newEnvGoalSource` into the line-oriented `MessageSource`; the
  control-loop router that dispatches by kind + addresses mailboxes; graceful unknown-goal
  reporting).
- `docs/spec/interfaces.md` (the `MessageSource` seam + `Message`/`MessageKind` contract).
- `docs/spec/configuration.md` (the stdin command grammar; `AGENT_BUILDER_GOAL_SPEC`
  semantics under the generalized source).

(Two code modules — `internal/supervisor` (the seam type) + `internal/cli` (the source
generalization + router). The seam belongs next to `GoalSource`/`Reporter`; the parser and
router belong in the CLI assembly. Within the at-most-two-modules rule.)

## Out of scope

- The **status handler body** (registry render + immediate Reporter answer) — task 114.
- The **info-fold / pending-info queue / amendment sub-goal** behavior — task 115.
- The **cancellation teardown** (`context.Context` thread, `box.Kill`/`Teardown`) — task 116.
- Telegram emitting typed messages — task 117 (this task generalizes the **env/stdin**
  source).
- Any change to `GoalSource`'s signature or the in-box `GoalSourceFactory` path.

## Dependencies

- **Task 112 — HARD dependency.** The router dispatches into the non-blocking control loop +
  status registry that 112 builds. Do not start 113 before 112 merges.
- Task 099 (env/stdin source), task 081 (`GoalSource`/`Task`) — merged.
- ADR 054 §2 — the authoritative design.
- **Unblocks:** 114, 115, 116 (each consumes the typed message + router), and transitively
  117. 110 (re-specced LLM planner) may run in parallel after 112 — it does not depend on the
  message protocol.
