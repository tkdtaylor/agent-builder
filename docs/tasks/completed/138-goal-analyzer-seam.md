# Task 138 — GoalAnalyzer seam + HeuristicGoalAnalyzer

**Status:** backlog
**Spec:** `docs/tasks/test-specs/138-goal-analyzer-seam-test-spec.md`
**Relates to:** ADR 060 (goal analysis & routing); mirrors ADR 058 Clarifier.

## Goal

Add the `GoalAnalyzer` seam and its deterministic default so the orchestrator can classify a goal's
**kind** (answer vs coding) and **complexity** before routing (ADR 060). No flow change in this task —
just the seam + types + heuristic, unit-tested. Task 139 consumes it.

## Scope

- **`internal/orchestrator/analyzer.go` (new):** `GoalKind` (`KindAnswer`/`KindCoding`),
  `GoalComplexity` (`ComplexitySimple`/`ComplexityComplex`), `GoalAnalysis{Kind,Complexity,Rationale}`,
  `GoalAnalyzer interface { Analyze(supervisor.Task) (GoalAnalysis, error) }`, `HeuristicGoalAnalyzer`
  + `NewHeuristicGoalAnalyzer()`. Rules per the test spec (repo/path → coding; question → answer;
  build-verb → coding; else answer). Deterministic, no IO, no executor import.
- No changes to `orchestrator.go` flow yet.

## Out of scope

- Wiring the analyzer into `BeginGoal` / routing (task 139). LLM analyzer (task 140). CLI (task 141).

## Verification plan

- **Highest now: L2/L3.**
- **L2:** `go test -race ./internal/orchestrator/...` — TC-138-01..04 hard-assert kind + complexity +
  rationale for the table of goals.
- **L3:** `make check` green (F-010/F-014 stay green — analyzer is pure).

## Boundaries

- Pure/deterministic; no model seam (that is the LLM analyzer, task 140). Keeps the orchestrator's
  no-executor invariant intact.
