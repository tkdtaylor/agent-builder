# Test spec — Task 142: LLM-backed GoalAnalyzer

**Task:** `docs/tasks/backlog/142-llm-goal-analyzer.md`
**Relates to:** ADR 060 §4, tasks 138/140, ADR 053, task 131 (LLMClarifier pattern).

## Context

`LLMGoalAnalyzer` classifies Kind + Complexity with a model via the `Invoker` seam, env-selected by
`AGENT_BUILDER_GOAL_ANALYSIS=llm`. Fail-safe: malformed model output falls back to the heuristic —
analysis never breaks intake.

## Requirements

- **REQ-142-01** — `LLMGoalAnalyzer` implements `GoalAnalyzer`; `Analyze` calls the `Invoker` with a
  classification prompt and parses a small structured response into `GoalAnalysis{Kind,Complexity,Rationale}`.
- **REQ-142-02** — Malformed/unparseable model output → fall back to the `HeuristicGoalAnalyzer` result
  (no error propagated to intake).
- **REQ-142-03** — `goalAnalyzerFromEnv`: `llm` → LLM analyzer (wired with the Invoker); `heuristic`/
  truthy → heuristic; unset/false → nil.
- **REQ-142-04** — Boundary: `internal/orchestrator` imports no `internal/executor`; the Invoker is
  constructed in `internal/cli` (F-010/F-014 green).

## Test cases (to implement)

- **TC-142-01** — stubbed Invoker returns a well-formed classification → correct Kind/Complexity parsed.
- **TC-142-02** — stubbed Invoker returns garbage → heuristic fallback result, no error.
- **TC-142-03** — env selection table (`llm`/`heuristic`/off) → right analyzer type or nil.
- **TC-142-04 (L6)** — an ambiguous goal the heuristic mis-routes is classified correctly by the LLM analyzer.
