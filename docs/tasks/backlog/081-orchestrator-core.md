# Task 081: Orchestrator core

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Build the orchestrator core (`internal/orchestrator`): a new layer ABOVE the
existing `supervisor`/`runtime` stack that accepts a goal from the channel adapter,
decomposes it into a plan, presents the plan for human approval via policy-engine's
`require_approval` obligation, and — only on approval — selects and dispatches
purpose-built worker agents by calling `recipe.SelectRecipe`. It aggregates results
and reports back through the channel. The orchestrator itself authors no code.

## Context

ADR 042 defines the orchestrator as Tier 1 of the two-tier architecture. Key
constraints: it is a consumer of the recipe seam (not a recipe itself), it authors
nothing (code-authoring is delegated to a worker recipe), and the `supervisor` package
is not modified by this task. The orchestrator is a purely additive new package.

**Blocked by tasks 076–080.** The recipe seam must be stable (076–079) and the
channel adapter must exist (080) before the orchestrator can receive goals.

## Requirements

| Req ID     | Description                                                                                                                                          | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-081-01 | Goal intake from `GoalSource`; plan decomposed into an ordered list of sub-goals. | must have |
| REQ-081-02 | Plan presented to human approval gate (policy-engine `require_approval`) before any worker is dispatched; no worker starts without approval. | must have |
| REQ-081-03 | On approval, orchestrator calls `recipe.SelectRecipe(name)` and dispatches one worker supervisor per sub-goal (single-worker sequential for this task; concurrency in task 086). | must have |
| REQ-081-04 | Worker results aggregated; summary reported back through the channel adapter. | must have |
| REQ-081-05 | `internal/orchestrator` has zero import of `internal/executor` (no code-authoring); `internal/supervisor` is unchanged (empty diff on that package). | must have |

## Readiness gate

- [x] Test spec `081-orchestrator-core-test-spec.md` exists (written first)
- [ ] Tasks 076–079 merged (recipe seam stable)
- [ ] Task 080 merged (channel adapter + `GoalSource` interface)
- [ ] Open questions in test spec resolved (goal decomposition strategy; report format)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-081-01] TC-081-01: Goal string → plan with at least one sub-goal produced before any dispatch
- [ ] [REQ-081-02] TC-081-02: Policy-engine stub returns `require_approval` → no worker dispatched; approval solicited via channel
- [ ] [REQ-081-03] TC-081-03: Policy-engine stub returns `allow` → `recipe.SelectRecipe` called per sub-goal; worker supervisor constructed and started
- [ ] [REQ-081-04] TC-081-04: Two workers complete (one success, one failure) → aggregated result reported with per-worker outcomes
- [ ] [REQ-081-05] TC-081-05: `go list -deps ./internal/orchestrator/...` contains no `internal/executor`; `git diff HEAD~1 -- internal/supervisor/` is empty

## Verification plan

- **Highest level achievable:** L2 (unit tests with stubbed channel adapter and
  policy-engine). An L5 end-to-end orchestrator run requires tasks 083 (agent-mesh)
  and 084 (memory-guard).
- **Harness command:**
  ```
  go test -count=1 ./internal/orchestrator/...
  go list -deps ./internal/orchestrator/...
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/orchestrator`
  - `go list` → no `internal/executor` in output
  - `make check` → `All checks passed.`

## Out of scope

- agent-mesh transport for worker dispatch (task 083).
- memory-guard on orchestrator state (task 084).
- Orchestrator self-containment + policy gating (task 085).
- Multi-worker concurrent dispatch (task 086).

## Dependencies

- Task 076 (recipe type + selector)
- Task 077 (runtime assembles from recipe)
- Task 078 (gate-existence assertion)
- Task 079 (seam proven with two recipes)
- Task 080 (channel adapter)
- Informs: tasks 082, 083, 084, 085, 086
