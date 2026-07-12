# Task 175: recurring/deferred goals from config, dispatched inside the daemon

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** completed

## Goal

Add a config-declared, stdlib-only interval/daily-time scheduler that fires
inside `agent-builder daemon` (task 174), dispatching through the SAME
goal-intake path every other goal source uses.

## Context

`docs/plans/roadmap.md`'s Forward arc item 5 pairs "heartbeat/daemon" with the
agent acting continuously, not only when invoked. Task 174 gives the daemon a
long-lived process; this task gives it a config-driven reason to act without an
inbound channel message. A schedule entry is just another goal producer feeding
the existing intake path (`Orchestrator.Handle`/`goalActor`), not a new
dispatch mechanism.

**No new dependency.** A full cron parser is explicitly flagged as the
richer-syntax alternative, requiring dependency approval per this project's
conventions; this task ships a v1 stdlib-only `every`/`at` scheduler covering
the common cases.

**Reference:**
- Task 174 (`runDaemon`, the process this task's scheduler runs inside)
- `internal/router/router.go:102` (`NewWithClock`, the fake-clock test
  convention this task's scheduler tests mirror)
- `internal/cli/goal_actor.go` (the existing goal-intake machinery this task's
  fired entries route through, unmodified)
- Task 156 (cancellation-teardown test technique, mirrored for
  scheduler-goroutine-exits-cleanly)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-175-01 | `AGENT_BUILDER_SCHEDULE_PATH` config, `every`/`at` entries, malformed file fails fast. | must have |
| REQ-175-02 | `every` entries fire repeatedly at their interval (fake-clock tested). | must have |
| REQ-175-03 | `at` entries fire once per calendar-day boundary. | must have |
| REQ-175-04 | Fired entries route through the standard goal-intake path, no parallel dispatch. | must have |
| REQ-175-05 | Unset schedule path is a true no-op (zero goroutines). | must have |
| REQ-175-06 | Scheduler stops cleanly on context cancellation. | must have |
| REQ-175-07 | Pre-existing `internal/cli` suites unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/175-scheduled-goals-test-spec.md` exists (written first)
- [x] Task 174 merged
- [ ] `make check` green on `main` before branching

## Implementation outline

1. New file `internal/cli/schedule.go`:
   ```go
   type ScheduleEntry struct {
       Goal  string
       Every time.Duration // zero if At is set
       At    time.Duration // time-of-day offset since midnight; zero-value if Every is set
   }

   func ParseScheduleFile(path string) ([]ScheduleEntry, error)
   ```
   JSON or plain-text format at executor's discretion (JSON recommended for
   parse-error clarity); reject an entry with both `every`/`at` set, or
   neither, with a message naming the entry index.
2. `Scheduler` type with an injectable clock seam (mirror
   `router.Clock`/`NewWithClock`'s exact shape):
   ```go
   type Clock interface { Now() time.Time }

   type Scheduler struct {
       entries []ScheduleEntry
       clock   Clock
       dispatch func(supervisor.Task) // the goal-intake seam
       done    chan struct{}
   }

   func NewScheduler(entries []ScheduleEntry, clock Clock, dispatch func(supervisor.Task)) *Scheduler
   func (s *Scheduler) Run(ctx context.Context) // blocks until ctx.Done(); ticks via a real time.Ticker in production, driven manually by tests via the clock seam plus an injectable tick-check hook
   ```
   Production ticking: a `time.Ticker` at a short fixed poll interval (e.g. 1
   minute) checking each entry against `clock.Now()`; test-driven ticking:
   tests call an unexported check method directly after advancing a fake clock,
   avoiding real sleeps (mirror how `internal/loop`/`internal/orchestrator`
   fake-clock tests already avoid real time in this codebase).
3. `runDaemon` (task 174): after `assembleOrchestrate` succeeds, if
   `AGENT_BUILDER_SCHEDULE_PATH` is set, parse it, construct a `Scheduler` whose
   `dispatch` closure builds a `supervisor.Task{ID: "sched-<n>-<RFC3339>",
   Spec: entry.Goal}` and routes it through the SAME goal-intake function
   `runControlLoop`'s message-drain path already calls for a channel-originated
   `MsgNewGoal` (reuse, do not fork), start `scheduler.Run(sigCtx)` in a
   goroutine, tracked so `runDaemon`'s own shutdown sequence can confirm it
   exits (a `done` channel closed when `Run` returns).
4. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-175-01] TC-175-01/02: schedule file parsing, malformed-file fail-fast.
- [ ] [REQ-175-02] TC-175-03: `every` fires repeatedly (fake clock).
- [ ] [REQ-175-03] TC-175-04: `at` fires once per day boundary.
- [ ] [REQ-175-04] TC-175-05: fired entries route through the standard intake path.
- [ ] [REQ-175-05] TC-175-06: unset path is a true no-op.
- [ ] [REQ-175-06] TC-175-07: scheduler stops cleanly on cancellation.
- [ ] [REQ-175-07] TC-175-08: `go test -race -count=1 ./internal/cli/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L2, fake-clock-driven scheduler mechanics
  (matches `router.NewWithClock`'s established precedent, avoids real-time
  sleeps in CI). L6 optional operator observation available but not required.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC175
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md`: new `AGENT_BUILDER_SCHEDULE_PATH` row and the
  schedule file format documented.
- `docs/operating.md`: "Running as a daemon" section (added by task 174) gains
  a schedule-file example.

## Out of scope

- Full cron expression syntax (flagged as a dependency-approval follow-on).
- Any change to `Orchestrator.Handle`/`goalActor`'s own dispatch logic.
- Persisting schedule-firing state across a daemon restart (a documented v1
  limitation: possible duplicate firing near a restart-adjacent boundary).

## Dependencies

- **Blocks on:** task 174.
- **Blocks:** none.
