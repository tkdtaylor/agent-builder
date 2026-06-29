# Task 100: LLMPlanner

**Project:** agent-builder
**Created:** 2026-06-28
**Status:** completed

## Goal

Implement an executor-backed `LLMPlanner` that satisfies the existing `orchestrator.Planner`
interface, decomposes free-form human goals into structured sub-goals via a model routed
through the ADR 043 registry/router, and is selectable alongside `StructuredPlanner` via
`AGENT_BUILDER_PLANNER=llm` — with no change to `internal/orchestrator`.

## Context

ADR 046 §1 shipped `StructuredPlanner` as the v1 `Planner` concrete and explicitly deferred
the LLMPlanner to "a separate task gated behind task 095 (the router)." The router cluster
(tasks 087–095) is now merged. This is that follow-on task.

The `Planner` seam (`orchestrator.Planner`) was designed with the LLMPlanner in mind: the
interface is `Plan(goal supervisor.Task) (Plan, error)` — the same contract the LLMPlanner
satisfies. The orchestrator adopts the LLMPlanner by swapping the concrete at construction
via `WithPlanner`; no other change to `internal/orchestrator` is needed.

### Import-boundary decision (resolved here)

The orchestrator's F-010 fitness check (`make fitness-orchestrator-no-executor`) asserts
`internal/orchestrator` does not directly import `internal/executor`. The LLMPlanner needs
to invoke a model — but it MUST NOT do so through `internal/executor` directly, for the same
reason: the orchestrator authors no code and must not acquire an unrestricted model path.

**Resolution:** `LLMPlanner` lives in a NEW sub-package `internal/orchestrator/planner`.
This package defines a narrow `ExecutorResolver` interface:

```go
// ExecutorResolver resolves a model for decomposition. Satisfied by *router.Router
// via a thin adapter.
type ExecutorResolver interface {
    Resolve(ctx context.Context, spec router.RoutingSpec) (registry.RegistryEntry, error)
}
```

`internal/orchestrator/planner` imports `internal/router` and `internal/registry` — the
routing path — but NOT `internal/executor`. A new fitness check **F-014**
(`make fitness-llm-planner-no-executor`) asserts this DIRECT-import invariant. The
`*router.Router` (which itself imports `internal/executor`) satisfies `ExecutorResolver`
at the wiring layer (`internal/cli`), where `internal/executor` imports are already
present; the LLMPlanner package itself never sees `internal/executor`.

This is the identical pattern to how `internal/orchestrator` dispatches through
`internal/runtime` (which imports `internal/executor`) without importing `internal/executor`
directly: the boundary is preserved by the DIRECT-import fitness check, not by a blanket
transitive exclusion.

### The decomposition prompt and parse contract

The LLMPlanner sends a prompt to the resolved model via a narrow invocation interface (not
the full `Executor.Run` agentic loop — the decomposition step returns data, not a branch).
The prompt includes: the goal text, the catalog of available recipe names (from
`recipe.ListRecipes()`), and instructions to return a structured line-list in the same
format as `StructuredPlanner` expects:

```
<recipe-name>: <spec text> [| target_repo=<repo> | sink=<sink>]
```

The LLMPlanner parses the response with the same tokenizer `StructuredPlanner.splitLine`
uses, supplemented with `target_repo=` / `sink=` metadata extraction. Fail-closed on
empty or unparseable responses (REQ-100-03).

### Sub-goals carry TargetRepo/Sink for the self-repo bright line

The orchestrator's `spawn-worker` gate (task 085) reads `SubGoal.TargetRepo` and
`SubGoal.Sink`. The LLMPlanner must prompt the model to include these fields and parse them
into each returned `SubGoal`. A response that omits them is legal (the bright-line guard
applies `TargetRepo == OwnRepo` → deny; empty string is not the own-repo). See REQ-100-02
and TC-100-02.

## Requirements

| Req ID      | Description                                                                                                               | Priority   |
|-------------|---------------------------------------------------------------------------------------------------------------------------|------------|
| REQ-100-01  | `LLMPlanner` implements `orchestrator.Planner`; produces a valid `Plan` with ≥1 sub-goals from a goal via a stub model   | must have  |
| REQ-100-02  | Each sub-goal carries `TargetRepo`/`Sink` parsed from the model response; self-repo bright line preserved                 | must have  |
| REQ-100-03  | Fail-closed on malformed/empty model response (empty string, no parseable sub-goals, resolver error) → error, never empty-repo plan | must have |
| REQ-100-04  | LLMPlanner uses a narrow `ExecutorResolver` interface (never `internal/executor` directly); compile-time seam enforcement | must have  |
| REQ-100-05  | Fitness F-014: `internal/orchestrator/planner` does not directly import `internal/executor`; check in Makefile + SPEC.md  | must have  |
| REQ-100-06  | Selectable via `AGENT_BUILDER_PLANNER=llm` alongside `StructuredPlanner`; unknown value → error + usage exit              | must have  |

## Readiness gate

- [x] Task 087 merged (registry types)
- [x] Task 088 merged (vault per-provider auth)
- [x] Task 089 merged (Claude CLI harness adapter)
- [x] Task 090 merged (Gemini CLI harness adapter)
- [x] Task 091 merged (local entry translation proxy)
- [x] Task 092 merged (router capability/cost/escalation)
- [x] Task 093 merged (usage/quota tracking)
- [x] Task 095 merged (recipe routing with real router)
- [x] Task 081 merged (orchestrator core — Planner interface stable)
- [x] ADR 046 §6 design intent read and resolved by this task

## Acceptance criteria

- [ ] [REQ-100-01] TC-100-01: `LLMPlanner.Plan` with stub resolver returns a 2-sub-goal `Plan`; compile-time `var _ orchestrator.Planner = (*planner.LLMPlanner)(nil)`
- [ ] [REQ-100-02] TC-100-02: `TargetRepo`/`Sink` parsed from model response; own-repo sub-goal not dispatched (assembled orchestrator + spy dispatch confirms)
- [ ] [REQ-100-03] TC-100-03 (sub-cases A–D): empty/garbage/own-repo-only/resolver-error all return non-nil errors; no zero-sub-goal plan emitted
- [ ] [REQ-100-04] TC-100-04: L2 compile-test + L3 direct-import check confirm `internal/executor` is not a direct import of `internal/orchestrator/planner`
- [ ] [REQ-100-05] TC-100-05: `make fitness-llm-planner-no-executor` exits 0 (`PASS F-014`); check is in `.PHONY`, `fitness:` prereqs, `docs/spec/SPEC.md`, `docs/spec/fitness-functions.md`
- [ ] [REQ-100-06] TC-100-06: `AGENT_BUILDER_PLANNER=structured` → StructuredPlanner; `=llm` → LLMPlanner; unknown → error + ExitUsage

## Verification plan

- **Highest level achievable in CI:** L2 (unit tests with stub model) + L3 (F-014 fitness).
  L5 (real model on dev host, router dispatches decomposition request, model returns a
  real plan) and L6 (observed live via `agent-builder orchestrate --planner=llm` with a
  free-form goal) are achievable on the dev host but are NOT CI-automatable (require a
  real model endpoint and API key).
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/orchestrator/planner/... ./internal/orchestrator/...
  ```
  Expected: `ok`.
- **L3 fitness commands:**
  ```
  make fitness-llm-planner-no-executor
  make fitness-orchestrator-no-executor
  make check
  ```
  Expected: `PASS F-014`; `PASS F-010`; `All checks passed.`
- **L5 (operator-run, dev host):** configure `AGENT_BUILDER_PLANNER=llm` + a real model
  endpoint; run `agent-builder orchestrate` with a free-form goal; record model,
  decomposition output, and sub-goal count in the verify commit.

## Modules touched

- `internal/orchestrator/planner` (new sub-package — LLMPlanner, ExecutorResolver interface,
  NewPlannerFromEnv)
- `docs/spec/SPEC.md`, `docs/spec/fitness-functions.md` (F-014 entry)
- `docs/spec/configuration.md` (`AGENT_BUILDER_PLANNER` env var)

(One new package + spec docs — within the one-task, at-most-two-modules rule. The new
sub-package does not count as a second "module" in the Unix-philosophy sense — it is the
single deliverable of this task. `internal/orchestrator` itself is not modified beyond the
compile-time interface assertion in tests.)

## Out of scope

- Changing the `orchestrator.Planner` interface.
- The `orchestrate` subcommand wiring (task 099 — already handles `AGENT_BUILDER_PLANNER`
  dispatch; this task provides the `"llm"` concrete it calls).
- Prompt engineering beyond the minimum to make TC-100-01 pass with a real model.
- Model fine-tuning or evaluation (task 094 covers model selection).
- A2A multi-model planning.

## Dependencies

- Tasks 087–095 (registry + router + harness adapters — all merged).
- Task 081 (stable `orchestrator.Planner` interface — merged).
- Task 099 (wiring layer that selects the planner via env var) — this task is a PARALLEL
  dependency: 099 reserves the `AGENT_BUILDER_PLANNER` hook; 100 provides the `"llm"`
  concrete. They can be worked concurrently on different branches; 100's output is
  mergeable into main after 099.
