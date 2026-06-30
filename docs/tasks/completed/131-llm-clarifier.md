# Task 131: LLM clarifier (deferred — could-have)

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** 🟡
**ADR:** 056 — Conversational human-gated orchestrate front door
**Test spec:** [131-llm-clarifier-test-spec.md](../test-specs/131-llm-clarifier-test-spec.md)
**Priority:** could-have — do NOT begin implementation until tasks 124–130 are all ✅

## Goal

Implement `LLMClarifier` in `internal/orchestrator/clarifier_llm.go` behind the
`AGENT_BUILDER_CLARIFIER=llm` env var, reusing the `Invoker` seam established in the
`LLMPlanner` (task 100). Add `clarifierFromEnv` to `internal/cli/orchestrate.go`
to select the clarifier based on the env var.

## Context

Task 128 delivers `HeuristicClarifier` v1 (no LLM, deterministic) as the default.
The `Clarifier` seam is explicitly designed for this follow-on: `LLMClarifier` sends
the goal to a local LLM (ollama-native only, via the same `Invoker` seam as
`LLMPlanner`) and asks it to decide readiness and generate clarifying questions if
needed. This task is deferred because:
1. The heuristic v1 is sufficient for the first cut.
2. The LLM clarifier's correctness depends on model quality and prompt engineering —
   it needs its own iteration cycle, separate from the intake state machine.
3. The F-010 / F-014 invariant (`Clarifier` must not import `internal/executor`)
   is easier to verify after the seam is stable.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-131-01 | `LLMClarifier` implements `Clarifier`. It sends a structured prompt containing the goal spec to the model via the injected `Invoker`. The `Invoker` seam is `internal/orchestrator/planner.Invoker`. | could have |
| REQ-131-02 | `LLMClarifier` parses the model's response to extract `Clarification.Ready` and `Clarification.Questions`. The response format (JSON or structured text) is defined by the implementation; the parsing must be deterministic and unit-testable via a spy `Invoker`. | could have |
| REQ-131-03 | `clarifierFromEnv` in `internal/cli/orchestrate.go` reads `AGENT_BUILDER_CLARIFIER` (`"llm"` → `LLMClarifier`; unset / `"heuristic"` → `HeuristicClarifier`; unknown → fail-fast error). | could have |
| REQ-131-04 | F-010 preserved: `internal/orchestrator/clarifier_llm.go` does NOT directly import `internal/executor`. The `Invoker` is injected at assembly time by `orchestrate.go`. | could have |

## Acceptance criteria

1. All four TCs in the test spec pass with hard assertions.
2. F-010 (`make fitness-orchestrator-no-executor`) remains green — no direct
   executor import from `internal/orchestrator/`.
3. `AGENT_BUILDER_CLARIFIER=llm` with a live ollama-native entry: the LLM clarifier
   calls the model and returns a `Clarification` — L5 with a real qwen3:8b inference.
4. `docs/spec/interfaces.md` and `docs/spec/configuration.md` updated.
5. `make check` passes.
6. `git status` clean on commit.

## Files changed

- `internal/orchestrator/clarifier_llm.go` (new) — `LLMClarifier` + `Invoke` call + response parsing.
- `internal/cli/orchestrate.go` — `clarifierFromEnv`, `EnvClarifier` constant.
- `internal/orchestrator/clarifier_llm_test.go` (new) — TC-131-01 through TC-131-03.
- `internal/cli/orchestrate_test.go` — TC-131-04.
- `docs/spec/interfaces.md`, `docs/spec/configuration.md`.

## Verification plan

**L2/L3:** `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...`
— all four TCs pass via spy `Invoker` (no real LLM needed for unit tests).

`make check` + `make fitness-orchestrator-no-executor` green.

**L5 (live ollama):** with a real `AGENT_BUILDER_CLARIFIER=llm` and a live
qwen3:8b entry, run the scripted conversation through `validate-orchestrate-intake.sh`
and observe the LLM-generated question.

**L6:** Telegram round-trip with the LLM clarifier active.

## Dependencies

- Tasks 124–130 must all be ✅ before this task begins.
- Task 100 (`LLMPlanner` + `Invoker` seam — the model for this implementation).

## Out of scope

- Prompt engineering and model tuning (those are follow-up iterations).
- Non-ollama-native harnesses (fail closed with `ErrSingleShotUnsupported`, same as
  `LLMPlanner`).
