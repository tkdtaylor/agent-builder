# Test Spec 119: Route the dispatched sub-goal task to the worker

**Linked task:** [`docs/tasks/backlog/119-orchestrate-route-task-to-worker.md`](../backlog/119-orchestrate-route-task-to-worker.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 2)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-119-01 | TC-001, TC-003    | ⏳ |
| REQ-119-02 | TC-002            | ⏳ |

## Unit under test

The orchestrate dispatch closure in `internal/cli/orchestrate_seams.go` and the
per-worker runtime entry (`runtimewiring.Run`). Today the dispatched `sub.Task` is
sealed into the transport envelope (`:82`) but the worker is run with only `cfg`
(`:97`), so it reads task files from `cfg.TaskRoot` and ignores the goal. This task
makes the dispatched `sub.Task` the worker's goal (a single-task goal source seeded
from `sub.Task`), while leaving the `run` subcommand's task-file discovery intact.

## Test cases

### TC-001: dispatched sub.Task drives the worker

- **Requirement:** REQ-119-01
- **Setup:** dispatch a `SubGoal{Task: {ID: "goal-1-0", Spec: "add a Reverse function", Repo: <tmp>}}` through the orchestrate dispatch seam with a **spy worker runner** (a fake `runtimewiring.Run` / goal-source seam recording the goal it received).
- **Expected:** the worker's goal source yields a task whose `ID == "goal-1-0"` and `Spec == "add a Reverse function"` — i.e. the worker acts on the dispatched sub-goal, **not** on a `TaskRoot` file. Assert the spy saw exactly that task.

### TC-002: the `run` subcommand still discovers task files (no regression)

- **Requirement:** REQ-119-02
- **Setup:** the single-task `run` path against a target with a seeded `docs/plans/roadmap.md` + one ready `docs/tasks/backlog/NNN-*.md`.
- **Expected:** `run` still selects the ready task **file** via `tasksource` (unchanged behavior); the 119 change is scoped to the orchestrate dispatch path and does not alter `run`'s goal source. Assert the run path picks the seeded task ID.

### TC-003: a blank/invalid dispatched task is a hard error, not a silent no-op

- **Requirement:** REQ-119-01
- **Setup:** dispatch a `SubGoal` whose `Task.ID`/`Task.Spec` are empty.
- **Expected:** the dispatch returns a descriptive error (fail fast); the worker is not run against an empty goal, and the result is **not** reported OK (interacts with task 120's honest-result requirement).

## Post-implementation verification

- [ ] `go test ./internal/cli/... ./internal/runtime/...` passes
- [ ] `make check` passes; no regression in `run`-path / tasksource tests
- [ ] Cross-module trace recorded: producer = orchestrate dispatch (`sub.Task`); consumer = worker goal source. The test asserts the worker consumed the dispatched task, not a file.

## Test framework notes

- Go `testing`. Use the existing dispatch/seam fakes in `internal/cli` tests; seed a
  temp repo for the `run`-path regression. No live executor/box required for the unit
  level — the spy stands in for `runtimewiring.Run`. L5/L6 covered in the end-to-end run.
