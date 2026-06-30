# Task 130: Escalation over the channel — reporterStatusWriter

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** completed
**ADR:** 056 — Conversational human-gated orchestrate front door
**Test spec:** [130-escalation-over-channel-test-spec.md](../test-specs/130-escalation-over-channel-test-spec.md)

## Goal

Introduce `reporterStatusWriter` in `internal/cli/orchestrate_seams.go` — a new type
that implements `loop.StatusWriter` by routing escalation text through
`supervisor.Reporter.Report` instead of writing to a task file. Replace the
`tasksource.NewStatusWriter(...)` call on the orchestrate path (`internal/cli/orchestrate.go`)
with `&reporterStatusWriter{reporter: reporter}`. This closes the synthetic-goal-ID
escalation gap identified in task 123's open design point.

## Context — prerequisite: task 123 must be merged first

Task 123 introduced `WithStatusWriter(loop.StatusWriter)` and wired
`tasksource.NewStatusWriter(...)` on the orchestrate path. Its open design point noted:
"`tasksource.StatusWriter.WriteStatus` rewrites the Status line of a matching task
file. On the orchestrate path the goal is free text (`goal-N`) and may have no
backing task file, in which case the live needs-human write would error." This task
resolves that gap by replacing the file-backed writer with a `Reporter`-backed one.

The `reporterStatusWriter` satisfies `loop.StatusWriter` and is the ONLY status writer
on the orchestrate path after this task. The `tasksource.NewStatusWriter` call is
removed from `orchestrate.go`.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-130-01 | `reporterStatusWriter{reporter}` implements `loop.StatusWriter`. `WriteStatus(taskID, status)` calls `reporter.Report(ctx, "needs-human: goal <taskID> escalated (<status>)")`. No filesystem access. Works for synthetic goal IDs (`goal-N`, `tg-*`). | must have |
| REQ-130-02 | `var _ loop.StatusWriter = (*reporterStatusWriter)(nil)` compiles — interface satisfaction is a compile-time guarantee. | must have |
| REQ-130-03 | `assembleOrchestrate` uses `&reporterStatusWriter{reporter}` (passed via `WithStatusWriter`) instead of `tasksource.NewStatusWriter(...)`. The `tasksource.NewStatusWriter` call is removed from `orchestrate.go`. | must have |
| REQ-130-04 | After this task, `tasksource` is not a direct import of `internal/cli/orchestrate.go`. (It may remain a transitive dependency; only the direct import is removed.) | should have |

## Acceptance criteria

1. `go test -race -count=1 ./internal/cli/...` passes; all five TCs non-vacuous
   (hard assertions on format, synthetic-ID no-error, interface satisfaction,
   reporter receives needs-human line, tasksource import absent).
2. TC-130-01: `WriteStatus("goal-7", "needs-human")` produces a reporter line
   containing both `"needs-human"` and `"goal-7"` — exact format pinned.
3. TC-130-02: `WriteStatus` with a synthetic goal ID (no backing file) returns
   `(result, nil)` — no "task not found" error.
4. TC-130-03: compile-time interface assertion passes.
5. TC-130-04: a spy reporter receives the needs-human line in an assembled
   orchestrate scenario with a deny policy.
6. TC-130-05: `grep -n "tasksource" internal/cli/orchestrate.go` is empty.
7. `docs/spec/behaviors.md` updated: escalation on the orchestrate path flows via
   `Reporter`, not task file writes; synthetic goal IDs are explicitly supported.
8. `make check` passes.
9. `git status` clean on commit.

## Files changed

- `internal/cli/orchestrate_seams.go` (new) — `reporterStatusWriter` type and `WriteStatus`.
- `internal/cli/orchestrate.go` — replace `tasksource.NewStatusWriter(...)` with `&reporterStatusWriter{reporter}`.
- `internal/cli/orchestrate_seams_test.go` (new) — TC-130-01, TC-130-02, TC-130-03.
- `internal/cli/orchestrate_test.go` — TC-130-04, TC-130-05.
- `docs/spec/behaviors.md`.

## Verification plan

**L2/L3:**
`go test -race -count=1 ./internal/cli/...` — all five TCs pass.

`make check` — lint + build + fitness green. If `tasksource` removal surfaces an
unused-import lint error in `orchestrate.go`, that is expected and should be fixed
(remove the import). If `tasksource` is still needed for other reasons, TC-130-05
is updated to assert only that `NewStatusWriter` is not called.

**L5 (scripted):**
`scripts/validate-orchestrate-intake.sh` extended to include the needs-human path:
a plan whose single sub-goal is denied by policy → reevaluation exhausts →
`WriteStatus("goal-N", "needs-human")` → reporter line `"needs-human: goal goal-N…"`.
The scripted conversation pipes the goal and greps for the needs-human line.

**L6 (operator-observed):**
A Telegram round-trip where the policy denies the plan's recipe → reevaluation
exhausts → the `needs-human` line arrives in the Telegram reply (for a `tg-*` goal
ID). This proves the channel-abstract, no-filesystem-dependency claim.
After this L6 run, rows 118–123 and 124–130 can be promoted to ✅ in separate
`verify:` commits.

## Dependencies

- **Task 123 must be merged first** (provides `WithStatusWriter` option and
  `loop.StatusWriter` interface on the orchestrate path — this task replaces its
  implementation, not its seam).
- Tasks 124–129 must be merged before this task begins (the orchestrate path must
  include the full ADR 056 stack for the L5 test to exercise the right code path).

## Out of scope

- LLM clarifier (task 131).
- Any change to the task 123 `WithStatusWriter` option or the `loop.StatusWriter`
  interface — this task is a drop-in replacement at the call site only.
- Re-dispatch after a resolved replan.
