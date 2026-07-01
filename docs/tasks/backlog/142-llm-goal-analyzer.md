# Task 142 — LLM-backed GoalAnalyzer

**Status:** backlog
**Spec:** `docs/tasks/test-specs/142-llm-goal-analyzer-test-spec.md`
**Relates to:** ADR 060 §4, tasks 138/140, ADR 053 (Completer/Invoker), task 131 (LLMClarifier — the pattern to mirror).

## Goal

Add an `LLMGoalAnalyzer` that classifies a goal's Kind + Complexity with a model (more accurate than
the heuristic), env-selectable — mirroring the LLM clarifier (task 131).

## Scope

- **`internal/orchestrator/planner` (or orchestrator):** `LLMGoalAnalyzer` implementing `GoalAnalyzer`,
  reusing the `Invoker` seam (`func(ctx, entry, prompt) (string, error)`) to prompt a model for a small
  structured classification (kind + complexity) and parse it into `GoalAnalysis`. Fail-safe: on parse
  failure fall back to the heuristic (never error the intake).
- **`internal/cli/orchestrate_answer.go`:** extend `goalAnalyzerFromEnv` so `AGENT_BUILDER_GOAL_ANALYSIS=llm`
  selects the LLM analyzer (wired with the same Invoker the LLM clarifier/planner use); `heuristic`/truthy
  → heuristic; off → nil. Keep F-010/F-014 (the Invoker is constructed in `internal/cli`).
- **Spec** updated (configuration.md `llm` value; interfaces.md analyzer implementors).

## Verification plan

- **L2:** `LLMGoalAnalyzer` parses a stubbed structured response into the right Kind/Complexity; falls
  back to heuristic on malformed output; env selection (`heuristic`/`llm`/off).
- **L3:** `make check` green.
- **L6 (operator):** a genuinely ambiguous goal (no repo, imperative-sounding) classified correctly by
  the LLM analyzer where the heuristic would mis-route.

## Boundaries

- The orchestrator never imports `internal/executor`; the model seam is the `Invoker`, wired in `internal/cli`.
- Fail-safe to the heuristic — analysis must never break goal intake.
