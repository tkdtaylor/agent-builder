# ADR 055 — Plan-derived authorization and a closed orchestrate dispatch loop

**Status:** Proposed
**Date:** 2026-06-29
**Supersedes / relates to:** ADR 038 (host-side policy decide gate), ADR 048 (orchestrator↔worker transport), ADR 053/054 (orchestrate control plane, planner), tasks 112–117 (control-plane plumbing, all 🟡 / never live-verified).

## Context

The `orchestrate` subcommand is meant to be agent-builder's base orchestration
component: **accept a goal, plan it, and assemble the foundational blocks into an
agent (or agents) that complete it autonomously.** Tasks 112–117 built the
plumbing — message intake, goal actor, structured planner, policy gate, in-process
signed orchestrator↔worker transport — but a local end-to-end run shows the
accept→complete loop is **not closed**. Three seams, verified in source:

1. **Policy always denies on orchestrate.** `policyClientFromEnv`
   (`internal/cli/orchestrate.go:804`) starts the policy daemon **without an allow
   set** (`&policy.PolicyDaemon{BinPath, SocketPath}` — `.Allow` unset → `serve
   --allow ""`). The allowlist evaluator denies any resource not in the (empty) set,
   so the `spawn-plan` gate denies before anything dispatches, and **no env var can
   populate the allow set** on this path. (The L6-proven `run` path *does* set it —
   `internal/runtime/run.go:815`.) This is why a local goal returns only `Plan
   denied`, and a structural reason 112–117 sit 🟡.
2. **The goal never reaches the worker.** `dispatch` (`internal/cli/orchestrate_seams.go:97`)
   runs the worker via `runtimewiring.Run(ctx, cfg, …)` passing only `cfg`
   (RecipeName set); the dispatched `sub.Task` is sealed into the transport envelope
   for verification (`:82`) but **never handed to the worker**, which reads task
   *files* from `cfg.TaskRoot` via `tasksource`. So orchestrate cannot act on a
   free-text goal at all.
3. **The result is hardcoded.** `dispatch` reports `supervisor.Result{OK: true}`
   (`orchestrate_seams.go:102`) regardless of what the worker did — a false success.

## Decision

Close the loop with three changes, the first of which adopts **plan-derived
authorization** as the orchestrate authorization model.

### 1. Plan-derived authorization (the load-bearing decision)

When a goal is received and the planner produces a `Plan`, the orchestrator
**constructs the allowed-action set from that plan** — the goal ID (for
`spawn-plan`), each sub-goal's recipe name (for `spawn-worker`), and each sub-goal's
task ID (for the worker's `run-task` gate). Authorization is scoped to exactly what
the plan needs to achieve the goal: no standing grants, nothing static.

**Two-gate model — why this is enforcement, not self-granting.** The threat model
the host-side policy gate exists for is the **in-box agent** (the executor running
inside exec-sandbox): it cannot self-grant because the policy decision happens
host-side, *before* the box is created (ADR 038). The orchestrator is the **trusted
control plane**; it deriving "what the plan needs" is legitimate. A spawn proceeds
iff:

```
plan-needs (orchestrator: resource ∈ this plan's derived set)
    ∧ deployment-permits (policy-engine: independent deployment policy)
```

- **plan-needs** is the orchestrator's contribution: it authorizes only the
  resources its own plan declared.
- **deployment-permits** keeps the `policy-engine` block load-bearing rather than a
  rubber stamp: the deployment's policy (allowlist `--allow`, or an OPA/Cedar policy)
  can still deny a recipe, egress host, or sensitivity the plan asked for. The plan
  can never *widen* authorization beyond what the deployment permits.

**Mechanism (v1):** the orchestrator owns the policy daemon lifecycle in
`policyClientFromEnv`. Per admitted plan, it derives the plan's resource set and
makes the policy decisions against that set (the `policy-engine` daemon's `--allow`
is populated from the plan-derived resources, intersected with an optional
deployment base allow supplied via env). Each goal's authorization is scoped to its
plan; the in-box agent never influences it. (A request-scoped authorization protocol
in the `policy-engine` block — long-lived daemon, authorized set per decide request —
is the cleaner long-term form and is left to a follow-up ADR so this change stays
within agent-builder and uses the block as shipped.)

### 2. Route the dispatched task to the worker

The worker must execute the sub-goal the orchestrator dispatched, not whatever task
files happen to be in `TaskRoot`. The dispatched `sub.Task` is delivered to the
per-worker runtime as its goal (a single-task goal source seeded from `sub.Task`),
so the goal text drives execution. Task-file discovery remains the path for the
single-task `run` subcommand; the orchestrate dispatch supplies the task directly.

### 3. Propagate the worker's real result

`dispatch` returns the worker's actual outcome (success/failure + verdict) instead
of a hardcoded `Result{OK: true}`, so a denied spawn, a failed gate, or an idle
worker is reported honestly to the operator via the reporter, and the audit/result
envelope carries the true result. This is the substrate the feedback loop (4) reads.

### 4. Blocked-action feedback and reevaluation

When an attempt fails because a **necessary action was denied** — the policy gate
returned deny for an action the agent needed to make progress — that denial is not a
silent dead-end. It is captured as **structured failure feedback** (the denied
resource/action and the deny reason, a typed failure distinct from a gate failure or
executor error) and **bubbled up** to the orchestrator. The orchestrator
**reevaluates**:

- **replan** — the next attempt takes an adjusted approach that achieves the goal
  within what is permitted (the reevaluation re-derives the plan, and therefore the
  plan-derived allow set, for the next attempt); or
- **escalate, independently** — when the action is genuinely required and only an
  independent grant can permit it, the orchestrator routes a needs-human escalation
  carrying the denied action and reason, so a human (not the agent) decides whether
  to widen authorization.

This is what **keeps the permission grant outside the agent and independent**: the
in-box executor never widens its own authorization. A denial alters behavior only
through reevaluation at the planning layer or an explicit, independent human grant —
never by the agent self-granting. The denial → feedback → reevaluate → adjusted-retry
cycle is what makes plan-derived authorization safe to iterate: least-privilege by
default, with a principled "the plan needed more, here is the action and why" signal
instead of either over-granting up front or failing opaquely. The retry policy bounds
reevaluation attempts before escalation, mirroring the existing gate-failure retry
bound.

## Consequences

- The orchestrator can, for the first time, take a goal and drive it to a verified
  completion by composing the blocks (policy-engine authorizes, exec-sandbox runs,
  the verify gate gates, branch-publish reports) — closing the 112–117 loop and
  giving those rows a path to ✅.
- Authorization is least-privilege and intent-scoped: the plan is the authorization
  request, the deployment policy is the ceiling.
- No new per-goal feature surface in agent-builder; this finishes the base
  orchestration component rather than expanding what it does per goal.
- Follow-up: a request-scoped authorization protocol in `policy-engine` (replacing
  per-plan `--allow` population) for long-lived-daemon efficiency and richer
  obligations.

## Task breakdown

- **Task 118** — plan-derived policy authorization on orchestrate (this branch):
  derive the allow set from the plan; intersect with optional deployment base allow;
  feed the policy decisions. Fail-closed unchanged for anything outside the plan.
- **Task 119** — route the dispatched `sub.Task` to the worker (goal reaches
  execution, not just task files).
- **Task 120** — propagate the worker's real result (replace hardcoded OK); this is
  the substrate the feedback loop reads.
- **Task 121** — blocked-action feedback + reevaluation: surface a policy denial of a
  necessary action as a typed failure carrying the action + reason; route it to
  bounded replan/reevaluation (re-deriving the plan and its allow set for the next
  attempt), then to independent human escalation. The agent never self-grants.

Each lands test-spec-first, on its own task branch, per the standard workflow.
