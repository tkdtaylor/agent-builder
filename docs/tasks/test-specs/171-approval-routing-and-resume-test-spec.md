# Test Spec 171: route pending approvals over the channel; approve/deny resumes or aborts

**Linked task:** [`docs/tasks/backlog/171-approval-routing-and-resume.md`](../backlog/171-approval-routing-and-resume.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Task 170 persists a `runstore.PendingApproval` and pauses further sub-goal
dispatch, but nothing surfaces it to an operator or resumes it. This task adds
the operator-facing half: new `approve <goalID> <taskID>` / `deny <goalID>
<taskID>` verbs, symmetric with and additive to the EXISTING `confirm
<goalID>`/`cancel <goalID>` plan-level grammar (tasks 125/126), routed through
the SAME two derivation sites those tasks touched (`internal/cli/router.go`'s
`parseMessageLine` for the stdin/worker path, `internal/channel/telegram/adapter.go`'s
`deriveMessage` for Telegram), because task 126's own task file documents these
two sites as independent and BOTH required for a channel-abstract verb (task
126's Context section: "This task is independent of task 125 ... Both are
required to close the channel-abstract confirm contract"). This task
deliberately merges what tasks 125/126 did as two tasks into one, because
`approve`/`deny` are small, symmetric, mechanical repetitions of the exact same
pattern those two tasks already established, not a new design.

**No new `examples/agent-cli` subcommand is needed.** `runSend` (`examples/agent-cli/main.go:131-`)
already accepts arbitrary command text as a positional argument and builds a
signed/sealed envelope carrying it; an operator using the CLI channel sends
`agent-cli send ... "approve goal-7 task-3"` exactly as they would `"confirm
goal-7"` today. This task adds the VERB, not a new transport command.

**Timeout-based auto-escalation** mirrors the existing clarifier-seam
timeout/state-machine precedent (task 128,
`internal/orchestrator/clarifier.go`) rather than inventing a new one.

**Module boundary:** this task necessarily touches three modules
(`internal/cli`, `internal/channel/telegram`, `internal/orchestrator`) because it
is explicitly the twin-task merge described above; each individual edit within
those modules is a small, mechanical, symmetric repetition of an established
pattern (grammar verb addition ×2, one new orchestrator method, one timeout
check), not three independent designs.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-171-01 | `parseMessageLine("approve <goalID> <taskID>", seq)` returns a new `Message{Kind: MsgApprove, GoalID, TaskID}` (a new `TaskID` field on `supervisor.Message`, or an encoding into the existing `Text` field, executor's choice, documented); `"deny <goalID> <taskID>"` returns `Message{Kind: MsgDeny, ...}` symmetrically. Malformed input (missing `taskID`) returns `ErrMalformedInput`, mirroring `confirm`'s exact convention (task 125). | TC-171-01, TC-171-02 |
| REQ-171-02 | The Telegram adapter's `deriveMessage` recognizes `approve <taskID>`/`deny <taskID>` as reply-to command keywords (goalID threaded from `goalIDCache`, mirroring `confirm`'s exact derivation, task 126). A standalone `approve`/`deny` (not a reply) is `MsgNewGoal`, matching `confirm`'s existing fallback rule. | TC-171-03, TC-171-04 |
| REQ-171-03 | `Orchestrator.ResumeApproval(ctx, goalID, taskID string, approved bool) error` reads the goal's `runstore.Record`, finds the matching `PendingApproval`, and on `approved==true`, re-dispatches JUST that sub-goal (reusing `dispatchOne`); on `approved==false`, marks that sub-goal's attempt terminally failed/needs-human and, if it was the LAST pending item for the plan, finalizes the plan's outcome. | TC-171-05, TC-171-06 |
| REQ-171-04 | An unresolved pending approval older than a configurable timeout (`AGENT_BUILDER_APPROVAL_TIMEOUT`, default e.g. `1h`) is auto-escalated over the `Reporter` seam exactly once (mirroring the clarifier's existing timeout/linger pattern, task 128), naming the goal, task, and elapsed wait. | TC-171-07 |
| REQ-171-05 | `goalActor`/the CLI command-drain loop routes `MsgApprove`/`MsgDeny` to `Orchestrator.ResumeApproval`, mirroring `applyConfirm`'s existing wiring shape (`internal/cli/goal_actor.go:237-273`). | TC-171-08 |
| REQ-171-06 | Pre-existing `confirm`/`cancel`/`status`/`info` grammar and Telegram derivation are byte-for-byte unaffected. | TC-171-09 |

---

## Pre-implementation checklist

- [x] Task 170 merged (`PendingApproval`, `Record.Status ==
  StatusAwaitingApproval` exist and are populated)
- [x] Task 125/126 merged (`confirm`/`cancel` grammar, the pattern this task mirrors)
- [x] Task 128 merged (clarifier timeout/state-machine, the pattern this task's
  timeout mirrors)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-171-01, `parseMessageLine("approve <goalID> <taskID>", ...)`

- **Requirement:** REQ-171-01
- **Level:** L2 (unit test, extends `internal/cli/router_test.go`'s existing
  `confirm`/`cancel` table exactly)
- **Test file:** `internal/cli/router_test.go` (extend)

**Step:** `parseMessageLine("approve goal-7 task-3", &seq)`.

**Expected output:** `Message{Kind: MsgApprove, GoalID: "goal-7", TaskID:
"task-3"}`, `ok == true`, `err == nil`. Symmetric `"deny goal-7 task-3"` returns
`Kind: MsgDeny` with the same fields.

---

### TC-171-02, malformed `approve`/`deny` input

- **Requirement:** REQ-171-01
- **Level:** L2

**Step:** `parseMessageLine("approve goal-7", &seq)` (missing `taskID`);
`parseMessageLine("approve", &seq)` (missing both).

**Expected output:** both return `ok == false`, `errors.Is(err,
cli.ErrMalformedInput) == true`, mirroring `confirm`'s exact TC-125-02
convention; `approve`/`deny` are NEVER silently downgraded to `MsgNewGoal`.

---

### TC-171-03, Telegram `deriveMessage` recognizes `approve`/`deny` as reply-to commands

- **Requirement:** REQ-171-02
- **Level:** L2 (extends `internal/channel/telegram/adapter_test.go`'s existing
  `confirm` derivation table)
- **Test file:** `internal/channel/telegram/adapter_test.go` (extend)

**Step:** Simulate a reply-to message with text `"approve task-3"` replying to a
cached new-goal message (populating `goalIDCache` exactly as the existing
`confirm` test fixture does).

**Expected output:** derives `Message{Kind: MsgApprove, GoalID: <cached goal
ID>, TaskID: "task-3"}`.

---

### TC-171-04, a standalone `approve` (no reply-to) is a new goal

- **Requirement:** REQ-171-02
- **Level:** L2

**Step:** A message with text `"approve task-3"` and NO reply-to (or an unknown
reply-to cache entry).

**Expected output:** derives `MsgNewGoal` with the full text as the goal spec,
mirroring `confirm`'s exact fallback rule (task 126's documented behavior for a
standalone `go`/`confirm`).

---

### TC-171-05, `ResumeApproval(approved=true)` re-dispatches the paused sub-goal

- **Requirement:** REQ-171-03
- **Level:** L2 (real `Orchestrator` + real `runstore.FileStore`, a `Record`
  with one `PendingApproval` pre-seeded via `store.Save` directly)

**Step:** `orch.ResumeApproval(ctx, "goal-7", "task-3", true)`.

**Expected output:** the fake `DispatchFunc` is invoked exactly once for
`task-3` (and NOT for any other sub-goal already completed); on success,
`store.Load("goal-7").Pending` no longer contains the `task-3` entry, and
`Record.Status` returns to `StatusRunning` (or `StatusCompleted` if it was the
last pending sub-goal and all others are already complete).

---

### TC-171-06, `ResumeApproval(approved=false)` marks the sub-goal failed and finalizes if last

- **Requirement:** REQ-171-03
- **Level:** L2

**Step:** Same seeded `Record`, `orch.ResumeApproval(ctx, "goal-7", "task-3",
false)`, `task-3` being the ONLY pending entry and all other sub-goals already
`StatusCompleted`.

**Expected output:** the fake `DispatchFunc` is NEVER invoked for `task-3`
(denied, not re-dispatched); `task-3`'s `AttemptState.Status ==
runstore.StatusNeedsHuman` (or `StatusFailed`, executor's choice, documented);
`Record.Status` becomes a terminal status (the plan finalizes, since this was
the last pending item) and `Handle`'s originally-blocked report is now sent
(reusing whatever finalization path `dispatchPlan` already uses for a
completed/failed plan).

---

### TC-171-07, timeout auto-escalates exactly once

- **Requirement:** REQ-171-04
- **Level:** L2 (fake clock, mirroring task 128's existing clarifier-timeout test
  technique, `internal/orchestrator/clarifier_test.go`)

**Step:** Seed a `PendingApproval` with `RequestedAt` set further in the past
than `AGENT_BUILDER_APPROVAL_TIMEOUT` (via an injectable clock, matching
`router.NewWithClock`'s and the clarifier's existing fake-clock seam
conventions). Run whatever periodic/on-check timeout-sweep mechanism this task
introduces (executor's choice: a sweep called from the goal actor's existing
`sweep` method, `internal/cli/goal_actor.go:211-226`, is the most natural
integration point, reuse it rather than inventing a new poll loop).

**Expected output:** `Reporter.Report` is called exactly once for the timed-out
approval, naming the goal ID, task ID, and elapsed wait; a second sweep tick
does NOT re-escalate the same pending approval (idempotent, matching the
clarifier's own once-only escalation guarantee).

---

### TC-171-08, `goalActor` routes `MsgApprove`/`MsgDeny` to `ResumeApproval`

- **Requirement:** REQ-171-05
- **Level:** L2 (extends `internal/cli/goal_actor_test.go`'s existing
  `applyConfirm` test pattern)
- **Test file:** `internal/cli/goal_actor_test.go` (extend)

**Step:** Drive a `Message{Kind: MsgApprove, GoalID: "goal-7", TaskID: "task-3"}`
through `goalActor.handleCommand`/`applyConfirm`'s sibling (a new `applyApproval`
method, mirroring `applyConfirm`'s exact shape at `goal_actor.go:237-273`).

**Expected output:** the fake `Orchestrator.ResumeApproval` seam (mirroring the
existing `WithDispatchFunc`-style test double conventions for `goalActor`) is
called with `("goal-7", "task-3", true)`.

---

### TC-171-09, pre-existing grammar/derivation unaffected

- **Requirement:** REQ-171-06
- **Level:** L2 (regression)

**Step:** Re-run the FULL pre-existing `internal/cli/router_test.go` and
`internal/channel/telegram/adapter_test.go` suites unmodified.

**Expected output:** byte-identical pass, `status`/`info`/`cancel`/`confirm`/bare-goal
grammar and derivation completely unaffected.

---

### TC-171-10, full regression

- **Requirement:** all
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/orchestrator/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2/L3 for the grammar/derivation/routing pieces
  (mirrors tasks 125/126/128's own verification level, no runtime binary needed
  beyond what the existing test harnesses already exercise); L5 is achievable for
  `ResumeApproval`'s dispatch-resumption proof via the same in-process
  two-independently-constructed-store technique used in tasks 168/169/170, if the
  executor judges it adds confidence over the L2 unit tests above.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/... ./internal/orchestrator/... -run TestTC171
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Any change to the existing `confirm`/`cancel`/`status`/`info` grammar or
  derivation logic.
- Any change to the plan-level `pauseForApproval`/`Resume`/`Approval` flow.
- `examples/agent-cli` code changes (none needed, `send` already generic).
