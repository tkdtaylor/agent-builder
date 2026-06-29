# Task 118: Plan-derived authorization on orchestrate

**Project:** agent-builder
**Created:** 2026-06-29
**Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md)

## Goal

Make the orchestrator construct the policy allow set **from the plan it just made**,
so a goal's spawns are authorized to exactly what the plan needs — replacing the
empty-allowlist denial that currently stops every orchestrate goal at `Plan denied`.

## Context

- Tech stack: Go.
- ADR 055 seam 1 (plan-derived authorization). Relates to ADR 038 (host-side policy
  decide), tasks 112–117 (orchestrate control plane, 🟡).
- Root cause (verified): `policyClientFromEnv` (`internal/cli/orchestrate.go:804`)
  starts the policy daemon with `.Allow` unset → `serve --allow ""` → the allowlist
  evaluator denies every resource. The `run` path sets it (`run.go:815`); orchestrate
  has no env to populate it.
- Decide resources key on `Resource.ID`: spawn-plan = `plan.GoalID`
  (`orchestrator.go` decideSpawn), spawn-worker = `sub.RecipeName` (decideSpawnWorker),
  worker run-task = `task.ID` (run.go).
- Dependencies: none (first of the ADR-055 chain). Tasks 119/120/121 follow.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-118-01 | `Plan.AllowedResources()` returns the deduped set `{GoalID} ∪ {recipe names} ∪ {task IDs}`. | must have |
| REQ-118-02 | The orchestrator gates each spawn decision on the plan-derived set first: in-plan resources proceed to the policy engine; out-of-plan resources are denied without consulting policy. | must have |
| REQ-118-03 | Effective decision = `plan-allows ∧ policy-allows` — the policy engine remains the independent ceiling (an in-plan resource it denies is still denied). | must have |
| REQ-118-04 | The policy daemon is configured with the plan-derived allow set, intersected with an optional deployment base allow (`AGENT_BUILDER_POLICY_ALLOW`); deployment can only narrow, never widen. | must have |

## Readiness gate

- [x] Test spec `118-orchestrate-plan-derived-authz-test-spec.md` exists
- [x] All acceptance criteria have a linked REQ ID
- [x] No blocking tasks

## Acceptance criteria

- [ ] [REQ-118-01] `Plan.AllowedResources()` derives + dedups the resource set (TC-001, TC-002).
- [ ] [REQ-118-02] In-plan resources reach policy; out-of-plan denied without a policy call (TC-003, TC-004).
- [ ] [REQ-118-03] In-plan ∧ policy-deny → deny; in-plan ∧ policy-allow → allow (TC-005).
- [ ] [REQ-118-04] Daemon allow = plan-derived ∩ optional deployment base (TC-006, TC-007).
- [ ] `make check` passes; existing orchestrate tests updated (the empty-allow deny path is replaced).
- [ ] `docs/spec/configuration.md` documents `AGENT_BUILDER_POLICY_ALLOW` and the plan-derived allow behavior on orchestrate.

## Verification plan

- **Highest level achievable:** L2/L3 here — the orchestrator gate and allow-set
  derivation are unit-tested with a fake PolicyClient and a fake daemon launcher
  recording `--allow`. The live allow→dispatch path (L5/L6) is reached only once the
  routing + result + feedback seams (119–121) land; this task earns 🟡 at L2/L3.
- **Cross-module state risk:** the plan-derived allow set flows orchestrator → policy
  daemon config. Trace: producer = `Plan.AllowedResources()` / the CLI policy wiring;
  consumer = the policy daemon `--allow`. A test must assert the daemon actually
  receives the derived set (TC-006), not just that the function returns it.
- **Runtime-visible surface:** the `Plan denied` → allowed transition is observable in
  the orchestrate run; quote it at L5/L6 in the end-to-end verification once 121 lands.

## Out of scope

- Routing the dispatched task to the worker (task 119), real result propagation
  (task 120), blocked-action feedback (task 121).
- A request-scoped authorization protocol inside the `policy-engine` block (follow-up
  ADR) — v1 configures the daemon allow per plan within agent-builder.

## Notes

- Keep the self-repo bright line in `decideSpawnWorker` ahead of everything (it is
  fail-closed regardless of policy or plan).
- Preserve fail-closed semantics end to end: empty plan → deny; policy error → deny
  (`policy.Decide` already returns `DecisionDeny` on error).
