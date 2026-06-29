# Task 119: Route the dispatched sub-goal task to the worker

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 2)

## Goal
Make the orchestrate worker execute the dispatched `sub.Task` (the planned goal),
not just whatever task files happen to be in `AGENT_BUILDER_TASK_ROOT`.

## Context
`internal/cli/orchestrate_seams.go:97` runs `runtimewiring.Run(ctx, cfg, …)` passing
only `cfg`; `sub.Task` is sealed into the transport envelope (`:82`) but never reaches
the worker, which reads task files via `tasksource` (`run.go:286,582`). So orchestrate
cannot act on a free-text goal.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-119-01 | The dispatched `sub.Task` is delivered to the per-worker runtime as its goal (a single-task goal source seeded from `sub.Task`), so the goal drives execution. | must have |
| REQ-119-02 | The single-task `run` subcommand's task-file discovery is unchanged (no regression). | must have |

## Verification plan
- L2/L3 unit + `make check`; L5/L6 in the end-to-end orchestrate run (with 118/120/121).
- Cross-module: producer = orchestrator dispatch (`sub.Task`); consumer = worker goal source. Trace both; assert the worker acts on the dispatched task spec/ID.

## Out of scope
Plan-derived authz (118), result propagation (120), feedback (121). Depends on 118.
