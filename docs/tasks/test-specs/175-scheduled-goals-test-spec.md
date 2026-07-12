# Test Spec 175: recurring/deferred goals from config, dispatched inside the daemon

**Linked task:** [`docs/tasks/backlog/175-scheduled-goals.md`](../backlog/175-scheduled-goals.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Task 174 gives the daemon a long-lived process; this task gives it a reason to
act without an inbound message: config-declared recurring or deferred goals,
dispatched through the SAME `orchestrate` path (`Orchestrator.Handle` or task
169's `RunToCompletion`, if merged) every other goal source uses. A schedule is
just another `supervisor.GoalSource`-shaped producer feeding the same intake
path `runControlLoop` already drains, matching this codebase's Unix-philosophy
"compose, don't grow the assembler" convention.

**Stdlib-lean, no new dependency.** A full cron expression parser
(`robfig/cron` or similar) is a real, common, well-maintained third-party
option, but adding it is a NEW dependency requiring explicit approval per this
project's conventions ("Ask first: Adding dependencies not already in the tech
stack"). This task ships a v1 minimal, stdlib-only interval/daily-time
scheduler covering the common cases (`every: "1h"`, `at: "03:00"` daily); a
follow-on task can adopt a full cron parser behind an ADR if an operator
demonstrates a real need for cron's richer syntax (multiple days-of-week,
month-boundaries, etc).

**Module boundary:** `internal/cli` (config parsing + a new `scheduler.go`
ticking against `time.Now`/an injectable clock) feeding into the EXISTING
goal-intake path (`goalActor`/`Orchestrator.Handle`, unmodified). No new
package; this is a small, self-contained addition to the daemon's own module.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-175-01 | A new config surface (a JSON/plain-text file at `AGENT_BUILDER_SCHEDULE_PATH`, list of `{goal: string, every: string}` OR `{goal: string, at: string}` entries, `every` a Go `time.ParseDuration`-parseable string, `at` an `HH:MM` 24h daily time) is parsed at daemon assembly time; a malformed schedule file is a fail-fast `errUsageConfig` before the control loop starts. | TC-175-01, TC-175-02 |
| REQ-175-02 | An `every`-scheduled entry fires repeatedly at its interval (an injectable `Clock`/`Ticker` seam, mirroring `router.NewWithClock`'s existing fake-clock test convention, so tests never sleep real wall-clock time). | TC-175-03 |
| REQ-175-03 | An `at`-scheduled entry fires once per calendar day at the given time (fake clock, assert it fires when the clock crosses the boundary and does NOT fire again until the next day's boundary). | TC-175-04 |
| REQ-175-04 | A fired schedule entry dispatches through the SAME goal-intake path as a channel-originated goal (constructs a `supervisor.Task` with a deterministic, schedule-derived ID, e.g. `"sched-<index>-<timestamp>"`, and routes it through the identical `Orchestrator.Handle`/`goalActor` machinery, no parallel dispatch path). | TC-175-05 |
| REQ-175-05 | The scheduler is opt-in: `AGENT_BUILDER_SCHEDULE_PATH` unset means zero schedule entries, zero new goroutines, the daemon behaves exactly as task 174 shipped it. | TC-175-06 |
| REQ-175-06 | The scheduler stops cleanly when the daemon's context is cancelled (no goroutine leak, verified via a leak-detection pattern this codebase already uses elsewhere, e.g. task 156's cancellation-teardown tests). | TC-175-07 |
| REQ-175-07 | Pre-existing `internal/cli` (including task 174's daemon) suites pass unchanged. | TC-175-08 |

---

## Pre-implementation checklist

- [x] Task 174 merged (`runDaemon`, the daemon process this task's scheduler
  runs inside)
- [x] Task 169 merged or gracefully degraded to (if task 169's
  `RunToCompletion` is not yet merged when this task executes, dispatch through
  `Orchestrator.Handle` directly, document which was used)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-175-01, schedule file parsing

- **Requirement:** REQ-175-01
- **Level:** L2 (unit test)
- **Test file:** `internal/cli/schedule_test.go` (new)

**Step:** Parse a well-formed schedule file with one `every` entry and one `at`
entry.

**Expected output:** two `ScheduleEntry` values, field-for-field correct
(`Goal`, `Every time.Duration` parsed from `"1h"` -> `time.Hour`, `At` parsed
from `"03:00"` into a distinguishable typed representation, e.g. `time.Duration`
since midnight or a small `struct{Hour, Minute int}`, executor's choice).

---

### TC-175-02, malformed schedule file fails fast

- **Requirement:** REQ-175-01
- **Level:** L2

**Step:** Parse a schedule file with an unparseable `every` value (`"soon"`) and
separately one with BOTH `every` and `at` set on the same entry (ambiguous,
must be rejected) and one with NEITHER set (also invalid).

**Expected output:** all three return a non-nil error identifying the
offending entry and reason; `assembleOrchestrate`'s (or `runDaemon`'s) startup
sequence surfaces this as `errUsageConfig` before the control loop starts,
matching this codebase's established fail-fast-before-goal-intake convention.

---

### TC-175-03, an `every` entry fires repeatedly on a fake clock

- **Requirement:** REQ-175-02
- **Level:** L2 (fake clock/ticker, no real sleeping)

**Step:** A scheduler constructed with an injectable clock seam, one `every:
"10m"` entry, a recording dispatch func. Advance the fake clock by 35 minutes
in discrete steps (e.g. 10m, 10m, 10m, 5m).

**Expected output:** the dispatch func is called exactly 3 times (at the 10m,
20m, and 30m marks), not called a 4th time for the remaining 5m.

---

### TC-175-04, an `at` entry fires once per day at the boundary

- **Requirement:** REQ-175-03
- **Level:** L2 (fake clock)

**Step:** A scheduler with one `at: "03:00"` entry. Advance the fake clock from
`02:00` to `04:00` on day 1 (crossing the boundary once), then from `02:00` to
`04:00` on day 2 (crossing again).

**Expected output:** exactly 2 dispatches total, one per day-boundary
crossing, none extra from repeated checks within the same day.

---

### TC-175-05, a fired entry routes through the standard goal-intake path

- **Requirement:** REQ-175-04
- **Level:** L2 (real `Orchestrator`/`goalActor` wiring, fake `Planner`/`DispatchFunc`
  recording what they receive, mirroring `internal/cli/goal_actor_test.go`'s
  existing fixture pattern)

**Step:** A scheduler entry fires. Assert what reaches the intake path.

**Expected output:** a `supervisor.Task` with `Spec` equal to the schedule
entry's `Goal` text and a deterministic, non-colliding `ID`
(`"sched-<index>-<RFC3339 timestamp>"` or equivalent) is handed to the SAME
`Orchestrator.Handle`/`goalActor.run` machinery a channel-originated goal uses,
not a separate/parallel dispatch function.

---

### TC-175-06, unset schedule path is a true no-op

- **Requirement:** REQ-175-05
- **Level:** L2 (regression)

**Step:** Assemble the daemon config with `AGENT_BUILDER_SCHEDULE_PATH` unset.
Inspect (via `runtime.NumGoroutine()` before/after assembly, or a more direct
"scheduler is nil" structural assertion, executor's choice) whether any
scheduler goroutine was started.

**Expected output:** zero scheduler goroutines; `assembleOrchestrate`/`runDaemon`
behavior otherwise identical to task 174's shipped state.

---

### TC-175-07, scheduler stops cleanly on context cancellation

- **Requirement:** REQ-175-06
- **Level:** L2 (goroutine-leak check, mirrors task 156's cancellation-teardown
  test technique)

**Step:** Start a scheduler under a cancellable context with at least one
`every` entry. Cancel the context. Wait (bounded) for the scheduler's internal
goroutine to exit (e.g. via a `done` channel the scheduler closes).

**Expected output:** the scheduler's goroutine exits within a bounded time
after cancellation; no further dispatch calls occur after cancellation even if
the fake clock is advanced further.

---

### TC-175-08, full regression

- **Requirement:** REQ-175-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/cli/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2 for the scheduler mechanics (fake-clock
  driven, matching `router.NewWithClock`'s established precedent for avoiding
  real-time sleeps in tests); L6 optional operator observation (a real
  short-interval schedule entry observed firing a real goal while
  `agent-builder daemon` runs unattended) is the strongest available proof but
  not required for this task's gate.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC175
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Full cron expression syntax (day-of-week/month-boundary rules); a follow-on
  ADR-gated task if a real operator need is demonstrated, this task explicitly
  flags `robfig/cron` (or similar) as the alternative requiring dependency
  approval.
- Any change to `Orchestrator.Handle`/`RunToCompletion`/`goalActor`'s own
  logic (this task is a new goal PRODUCER, not a new dispatch mechanism).
- Persisting schedule state itself (the schedule file IS the config, not
  runtime state; whether a given firing already happened today across a daemon
  restart is a known v1 gap, acceptable duplicate-firing risk on restart near a
  boundary, documented as a limitation, not silently ignored).
