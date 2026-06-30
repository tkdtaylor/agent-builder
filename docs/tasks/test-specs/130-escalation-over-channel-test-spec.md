# Test Spec 130: Escalation over the channel — reporterStatusWriter

**Linked task:** [`docs/tasks/backlog/130-escalation-over-channel.md`](../backlog/130-escalation-over-channel.md)
**Written:** 2026-06-29
**ADR:** 058 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-130-01 | TC-130-01, TC-130-02 | ✅ |
| REQ-130-02 | TC-130-03         | ✅ |
| REQ-130-03 | TC-130-04         | ✅ |
| REQ-130-04 | TC-130-05         | ✅ |

## Test locations

- `internal/cli/orchestrate_seams_test.go` (new file, package `cli`) — TC-130-01, TC-130-02
- `internal/cli/orchestrate_test.go` — TC-130-03, TC-130-04, TC-130-05

Test function names:
- **TC-130-01:** `TestReporterStatusWriterFormatsNeedsHumanLine`
- **TC-130-02:** `TestReporterStatusWriterSyntheticGoalID`
- **TC-130-03:** `TestReporterStatusWriterSatisfiesLoopStatusWriterInterface`
- **TC-130-04:** `TestAssembleOrchestrateUsesReporterStatusWriter`
- **TC-130-05:** `TestReporterStatusWriterReplacesFileBackedWriter`

## Unit under test

`internal/cli/orchestrate_seams.go` (new file) — `reporterStatusWriter` struct that
implements `loop.StatusWriter` via a `supervisor.Reporter` field:

```go
type reporterStatusWriter struct {
    reporter supervisor.Reporter
}

func (w *reporterStatusWriter) WriteStatus(taskID string, status loop.WritableStatus) (loop.StatusWriteResult, error)
```

`WriteStatus` calls `w.reporter.Report(ctx, "needs-human: goal <taskID> escalated (<status>)")` —
no filesystem access, so it works for synthetic goal IDs (`goal-N`) that have no
backing task file.

`internal/cli/orchestrate.go` — `assembleOrchestrate` is modified to pass a
`reporterStatusWriter` via `WithStatusWriter(...)` instead of the
`tasksource.NewStatusWriter(...)` call introduced in task 123. The `tasksource`
import on the orchestrate path is removed when it is no longer needed.

Prerequisite note: task 123 must be merged before this task is implemented (see task
file). The `WithStatusWriter` option and `loop.StatusWriter` interface are provided
by task 123.

## Test cases

### TC-130-01: reporterStatusWriter formats the needs-human line correctly

- **Requirement:** REQ-130-01
- **Setup:** construct a `reporterStatusWriter` with a `FakeReporter`. Call
  `WriteStatus("goal-7", loop.WritableStatus("needs-human"))`.
- **Expected:**
  - `reporter.Reported()` has exactly one entry.
  - The reported string contains `"needs-human"` — assert
    `strings.Contains(reported[0], "needs-human")`.
  - The reported string contains `"goal-7"` — assert
    `strings.Contains(reported[0], "goal-7")`.
  - `WriteStatus` returns `(result, nil)` — no error on a successful Report.
  - The exact format is: `"needs-human: goal goal-7 escalated (needs-human)"` or
    equivalent. Assert the exact string matches the format string used in implementation
    (whichever format is chosen, TC-130-01 must pin it to prevent silent drift).

### TC-130-02: reporterStatusWriter works for a synthetic goal ID (no backing file)

- **Requirement:** REQ-130-01
- **Setup:** call `WriteStatus("goal-42", loop.WritableStatus("needs-human"))` on a
  `reporterStatusWriter` backed by a `FakeReporter`. The `FakeReporter` never
  accesses the filesystem — it captures the text in memory.
- **Expected:**
  - `WriteStatus` does NOT return a "task not found" or file-related error.
  - `reporter.Reported()[0]` contains `"goal-42"`.
  - No filesystem paths are accessed during the call (assert via
    the `FakeReporter`'s pure in-memory implementation — if it returns a non-nil
    error, the test fails).
  This is the load-bearing assertion that closes the synthetic-goal-ID gap from
  task 123's open design point.

### TC-130-03: reporterStatusWriter satisfies the loop.StatusWriter interface

- **Requirement:** REQ-130-02
- **Setup:** compile-time check: `var _ loop.StatusWriter = (*reporterStatusWriter)(nil)`.
  This is a static type assertion — the test passes if the code compiles.
- **Expected:**
  - The compilation succeeds (the assertion is satisfied).
  - Additionally, construct a `reporterStatusWriter` and pass it to a function
    that takes `loop.StatusWriter` — assert at runtime that the value is non-nil.
  This is the interface-satisfaction contract test. Any missing method causes a
  compilation failure, not a subtle runtime bug.

### TC-130-04: assembleOrchestrate wires reporterStatusWriter instead of the file-backed writer

- **Requirement:** REQ-130-03
- **Setup:** call `assembleOrchestrate` (or the equivalent assembly function used in
  tests) with a spy `FakeReporter` and a stub policy/planner. Trigger a blocked
  spawn scenario: use a denyingPolicy (returns `DecisionDeny` for `spawn-worker`)
  and a plan with one sub-goal. Run the control loop until the goal's actor joins.
- **Expected:**
  - The `FakeReporter` receives a `"needs-human"` line (the `reporterStatusWriter`
    routed the escalation through the Reporter, not to a file).
  - `strings.Contains(reporter.Reported()[n], "needs-human")` for some n.
  - No file `WriteStatus` call is made (the test environment has no `TaskRoot` wired,
    and the `tasksource.NewStatusWriter` call is absent from the orchestrate path).
  Assert by checking that `reporter.Reported()` contains the needs-human line
  (the file-backed writer would have errored on a synthetic ID instead).

### TC-130-05: the file-backed tasksource.StatusWriter is no longer constructed on the orchestrate path

- **Requirement:** REQ-130-04
- **Setup:** `go list -deps ./internal/cli/...` or a direct import-graph check.
- **Expected:**
  - The `internal/tasksource` package is NOT imported by `internal/cli/orchestrate.go`
    after this task (the import was added by task 123 solely for `NewStatusWriter` on
    the orchestrate path; `reporterStatusWriter` removes that need).
  - Assert via either:
    (a) `go list -deps ./internal/cli` does NOT contain `agent-builder/internal/tasksource`
        as a direct import of `orchestrate.go` specifically, OR
    (b) a static grep: `grep -n "tasksource" internal/cli/orchestrate.go` returns
        empty output.
  This is the cleanup assertion: the `tasksource` dependency on the orchestrate path
  is incidental to task 123's interim solution and should be removed by this task.
  Note: `tasksource` may remain a transitive dependency (via `internal/runtime`);
  the assertion is about DIRECT imports of `orchestrate.go` only.

## Post-implementation verification

- [ ] `go test -race -count=1 ./internal/cli/...` passes with all five TCs
  non-vacuous (hard assertions — not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] `docs/spec/behaviors.md` updated: the escalation-over-the-channel behavior is
  documented — needs-human escalations on the orchestrate path flow via `Reporter`,
  not via `tasksource.StatusWriter` (the file-backed path); synthetic goal IDs no
  longer error at the escalation sink in the same commit
- [ ] `docs/spec/configuration.md` updated: a note that `AGENT_BUILDER_TASK_ROOT`
  (if it existed on the orchestrate path) is no longer used for escalation routing
  on the orchestrate path; escalation is now Reporter-backed
- [ ] The `reporterStatusWriter` is in `internal/cli/orchestrate_seams.go` (a new
  file isolating the seam adapter from the assembly function)
- [ ] L5: `scripts/validate-orchestrate-intake.sh` covers the needs-human path
  (a plan whose single sub-goal is denied by policy → reevaluation exhausts →
  `WriteStatus("goal-N", "needs-human")` → `reporter.Reported()` contains the line)

## Test framework notes

- Go `testing`. Reuse `FakeReporter` (task 098). The `loop.StatusWriter` interface
  and `loop.WritableStatus` type are from `internal/loop` (established in task 123).
- TC-130-05's import-graph check: use `go list -deps ./internal/cli/...` in a test
  helper or a `make fitness-*` rule. If `tasksource` remains as a direct import for
  other reasons (not the `NewStatusWriter` call), update TC-130-05 to assert only
  that `tasksource.NewStatusWriter` is not called on the orchestrate path.
- Prerequisite: task 123 must be merged. TC-130-03 (interface satisfaction) fails to
  compile if `loop.StatusWriter` or `WithStatusWriter` from task 123 are absent.
- L5: the needs-human path requires a full orchestrate run with a deny policy and
  a `reporterStatusWriter` spy. The scripted conversation in
  `scripts/validate-orchestrate-intake.sh` should include this scenario.
- L6: the operator observes `"needs-human: goal tg-CHATID-MSGID escalated (needs-human)"`
  in the Telegram reply (for a Telegram-channel run where the goal ID is a synthetic
  tg-* ID). This proves the channel-abstract claim.
