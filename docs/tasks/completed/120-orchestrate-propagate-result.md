# Task 120: Propagate the worker's real result

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 3)

## Goal
Replace the hardcoded `supervisor.Result{OK: true}` in orchestrate dispatch with the
worker's actual outcome, so success/failure is reported honestly.

## Context
`internal/cli/orchestrate_seams.go:102` seals `Result{OK: true}` regardless of what the
worker did — a false success. `runtimewiring.Run` returns an error on gate/executor
failure; the dispatch must carry the real outcome into the result envelope + reporter.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-120-01 | The dispatch result reflects the worker's real outcome (OK only when the run succeeded; failure/verdict carried otherwise). | must have |
| REQ-120-02 | A failed worker (gate fail / executor error / idle no-task) is reported as not-OK to the operator via the reporter and the result envelope. | must have |

## Verification plan
- L2/L3 unit + `make check`; L5/L6 in the end-to-end run.
- Cross-module: producer = `runtimewiring.Run` outcome; consumer = result envelope + reporter. Assert a failed run does NOT report OK.

## Out of scope
118/119/121. Depends on 119 (real execution to have a real result). Substrate for 121's feedback.
