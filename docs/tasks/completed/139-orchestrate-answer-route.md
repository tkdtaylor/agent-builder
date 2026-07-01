# Task 139 — orchestrate answer route (single-turn)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/139-orchestrate-answer-route-test-spec.md`
**Relates to:** ADR 060, task 138, ADR 059.

## Goal

Route a `KindAnswer` goal to the single-shot `Answerer` and report the answer over the channel;
`KindCoding` falls through to the existing flow. Read-only inference — no clarifier/planner/gate/approval.

## Scope

- **`internal/orchestrator/analyzer.go`:** `Answerer` interface (`Answer(ctx, prompt, complexity)`).
- **`internal/orchestrator/orchestrator.go`:** `analyzer`/`answerer` fields + `WithGoalAnalyzer`/
  `WithAnswerer` options; the `KindAnswer` branch in `BeginGoal` + `answerGoal` (audit `ActionCompletion`,
  `StateDone`, report). Nil analyzer = pre-060 coding behavior.

## Out of scope

- Multi-turn conversation (task 141). LLM analyzer (task 142). CLI wiring (task 140).

## Verification plan

- **L2:** `go test -race ./internal/orchestrator/...` — TC-139-01..03.
- **L3:** `make check` green (F-010/F-014 intact — no executor import).
- **L6:** exercised via task 140's live orchestrate answer.

## Boundaries

- `Answerer` is an interface; the concrete (Completer-backed) impl lives in `internal/cli`.
- Read-only: the answer route takes no action, so it bypasses the spawn-policy + approval gates (ADR 060).
