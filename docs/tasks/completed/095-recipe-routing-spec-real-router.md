# Task 095: Recipe RoutingSpec wired to the real router

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Replace the `stubResolveExecutor` in `internal/runtime` (introduced in task 077) with
the real registry+router from tasks 087–093. After this task, `runtime.Run` resolves
each recipe's `RoutingSpec` via the real router, which selects the cheapest eligible
registered executor at dispatch time.

**Zero-drift check:** the coding-agent recipe still routes to the Claude executor
(because Claude is the only configured entry in the CI env), and all existing e2e
tests pass without modification.

## Context

Task 077 introduced a deliberately-temporary stub: `stubResolveExecutor` maps any
`RoutingSpec` unconditionally to `executor.NewClaudeCLI(...)`. The stub was marked
with a comment `// stubResolver — replaced by registry+router in task 095`.

This task removes that stub and wires the real router:
1. `runtime.Run` calls `registry.LoadFromEnv()` to build the catalog.
2. Constructs a `router.Router` from the catalog.
3. Resolves each recipe's `RoutingSpec` via `router.Select(routingSpec)`.
4. Passes the selected executor to the supervisor.

F-003 must remain intact: `internal/supervisor` still does not import `internal/router`.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                        | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-095-01 | The `stubResolveExecutor` function is removed from `internal/runtime`. `runtime.Run` resolves `RoutingSpec` via `router.Select(routingSpec)` using the catalog from `registry.LoadFromEnv()`. Existing e2e tests pass without modification (zero-drift). | must have |
| REQ-095-02 | When multiple entries are configured, the router selects the cheapest eligible entry per the ADR 043 capability/cost-first policy. A unit test with a fake registry (two entries at different tiers/costs) proves the router is invoked and returns the cheaper one. | must have |
| REQ-095-03 | `router.Select` returning `ErrNoEligibleExecutor` (empty registry or all entries exhausted) causes `runtime.Run` to return a descriptive error before any sandbox creation. No audit events emitted. | must have |
| REQ-095-04 | F-003 preserved: `make fitness-supervisor-isolation` exits 0; `go list -deps ./internal/supervisor/...` contains no `internal/router` or `internal/registry`. | must have |

## Readiness gate

- [x] Test spec `095-recipe-routing-spec-real-router-test-spec.md` exists (written first)
- [ ] Task 077 merged (stub resolver in place, clearly marked)
- [ ] Task 087 merged (registry type)
- [ ] Task 092 merged (router with selection + escalation)
- [ ] Task 093 merged (quota tracking + persistence)
- [ ] Tasks 089, 090, 091 merged (harness adapters the router constructs)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-095-01] TC-095-01: `stubResolveExecutor` absent from `internal/runtime` (source inspection); runtime uses `registry.LoadFromEnv()` + `router.Select`
- [ ] [REQ-095-01] TC-095-02: e2e tests pass without modification (zero-drift)
- [ ] [REQ-095-02] TC-095-03: fake two-entry registry → router selects cheaper entry; cheaper executor invoked
- [ ] [REQ-095-03] TC-095-04: empty registry → `runtime.Run` errors before dispatch; no audit events
- [ ] [REQ-095-03] TC-095-05: unknown recipe name → error before dispatch (regression from task 077)
- [ ] [REQ-095-04] TC-095-06: `make fitness-supervisor-isolation` → PASS; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — existing e2e harness proves zero-drift; unit test
  with fake registry proves router selection.
- **Harness command:**
  ```
  go test -count=1 ./internal/runtime/... ./tests/cli/... ./tests/e2e/... \
    -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  make fitness-supervisor-isolation
  make check
  ```
  Expected:
  - All e2e tests pass; no test files modified
  - Fitness → `PASS fitness-supervisor-isolation: …`
  - `make check` → `All checks passed.`

## Out of scope

- Multi-entry live routing with real Codex or Gemini (L6, operator-run).
- Live test with real Claude subscription + real local model simultaneously.
- Spec updates beyond `docs/spec/configuration.md` (already updated in task 087 when
  the registry env vars were documented).

## Dependencies

- Task 077 (stub resolver — the replacement target).
- Task 087 (registry type + `LoadFromEnv`).
- Task 092 (router selection + escalation).
- Task 093 (quota tracking + persistence + clock seam).
- Tasks 089, 090, 091 (harness adapters).
- This is the final task in the ADR 043 cluster. After it lands, the roadmap's
  "Multi-provider router" bullet is fully implemented.
