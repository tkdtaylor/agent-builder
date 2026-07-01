# Task 140 — orchestrate answer wiring (analyzer + answerer)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/140-orchestrate-answer-wiring-test-spec.md`
**Relates to:** ADR 060, tasks 138/139, ADR 059.

## Goal

Wire the answer route live: inject a `GoalAnalyzer` + a Completer-backed `Answerer` into the orchestrate
assembly, opt-in via `AGENT_BUILDER_GOAL_ANALYSIS` (default off = pre-060 coding behavior).

## Scope

- **`internal/cli/orchestrate_answer.go` (new):** `EnvGoalAnalysis` const, `goalAnalyzerFromEnv`,
  `cliAnswerer` (complexity→capability floor → router.Select → `CompleterForEntry.Complete`).
- **`internal/cli/ask.go`:** extract `buildBrainCatalog` (shared by `ask` + the answerer).
- **`internal/cli/orchestrate.go`:** pass `WithGoalAnalyzer(goalAnalyzerFromEnv(getenv))` +
  `WithAnswerer(cliAnswerer{})`.
- **Spec:** `configuration.md` (`AGENT_BUILDER_GOAL_ANALYSIS`), `interfaces.md`/`behaviors.md`
  (orchestrate answer route), `diagrams.md` §5 note.

## Out of scope

- Multi-turn (task 141). LLM analyzer (task 142). Making analysis default-on (needs coding-flow test
  fixtures migrated to repo-shaped goals) — a follow-up.

## Verification plan

- **L2:** `go test ./internal/cli/...` — TC-140-01/02.
- **L3:** `make check` green with the flag off (existing orchestrate suite unaffected).
- **L6 (operator):** `AGENT_BUILDER_GOAL_ANALYSIS=true` + local brain → `orchestrate` with a bare
  question prints "Paris" over the channel.

## Boundaries

- Opt-in default-off so the coding pipeline + its tests are unaffected this task.
- `internal/executor` stays out of `internal/orchestrator` (the answerer lives in `internal/cli`).
