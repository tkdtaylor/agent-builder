# Task 121: Blocked-action feedback + reevaluation

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 4)

## Goal
When an attempt fails because a necessary action was denied by policy, bubble it up as
typed feedback, reevaluate (bounded replan), then escalate independently to a human —
the agent never self-grants.

## Context
ADR 055 seam 4. Builds on real result propagation (120). The retry/escalation policy
(`internal/loop`) must distinguish a policy denial of a necessary action from a gate
failure / executor error, route it to bounded replan (re-deriving the plan and its
allow set), then to needs-human escalation carrying the denied action + reason.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-121-01 | A policy denial of a necessary action surfaces as a typed failure carrying the denied resource/action + reason (distinct from gate failure / executor error). | must have |
| REQ-121-02 | The orchestrator routes it to bounded reevaluation (replan re-derives plan + allow set) up to the retry bound, then to independent human escalation (needs-human) carrying the action + reason. | must have |
| REQ-121-03 | The in-box agent never widens its own authorization — authorization changes only via replan or independent human grant. | must have |

## Verification plan
- L2/L3 unit + `make check`; L5/L6 in the end-to-end run (a goal needing a denied action → escalation with the reason).
- Cross-module: producer = worker/policy denial; consumer = retry policy + escalation. Assert the denial reason reaches escalation and no self-grant occurs.

## Out of scope
118/119/120. Depends on 120 (real result) and 118 (plan-derived allow to re-derive on replan).
