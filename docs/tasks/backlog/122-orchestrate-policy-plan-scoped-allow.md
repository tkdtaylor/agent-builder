# Task 122: Feed the policy daemon the plan-scoped allow set

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 1, daemon side)

## Goal
Make the independent policy engine actually ALLOW a plan's in-plan resources on the
orchestrate path, by configuring the policy daemon with the plan-derived allow set
(`Plan.AllowedResources()`) intersected with an optional deployment base allow
(`AGENT_BUILDER_POLICY_ALLOW`). Split from task 118 (which added the orchestrator-side
plan-derived gate); this closes the daemon side so the end-to-end run dispatches.

## Context
`policyClientFromEnv` (`internal/cli/orchestrate.go:804`) starts the daemon with
`.Allow` unset → `serve --allow ""` → every resource denied, including in-plan ones.
The decide resources are dynamic per goal (`plan.GoalID`, recipe names, `task.ID`), so
the daemon must be fed the plan-derived set per plan — a per-plan daemon-lifecycle
concern. Deployment base (`AGENT_BUILDER_POLICY_ALLOW`) can only narrow (intersect).

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-122-01 | The policy daemon serving an orchestrate plan's decisions is configured with `Plan.AllowedResources()` (so the independent engine allows in-plan resources, fixing the empty-allowlist denial). | must have |
| REQ-122-02 | An optional deployment base allow (`AGENT_BUILDER_POLICY_ALLOW`) intersects the plan-derived set: effective daemon allow = plan-derived ∩ base (base unset → full plan-derived). Deployment can only narrow, never widen. | must have |
| REQ-122-03 | Fail-closed preserved: no plan resources / empty effective set → deny; the self-repo bright line and the orchestrator plan-gate (task 118) remain in force. | must have |

## Verification plan
- L2/L3: unit test the effective-allow computation + the daemon-config seam (fake launcher recording `--allow`); `make check`.
- L5/L6: the end-to-end orchestrate run (with 118/119/120) shows `Plan denied` → dispatched for an in-plan goal; quote the transition.
- Cross-module: producer = `Plan.AllowedResources()` ∩ base; consumer = policy daemon `--allow`. Assert the daemon receives the derived set.
- Spec: `docs/spec/configuration.md` documents `AGENT_BUILDER_POLICY_ALLOW` + plan-derived allow on orchestrate.

## Out of scope
The orchestrator-side gate (task 118, done). A request-scoped authorization protocol in the `policy-engine` block (follow-up ADR). Depends on task 118.
