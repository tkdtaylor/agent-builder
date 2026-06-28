# Task 086: Multi-worker concurrent dispatch

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Extend the orchestrator to dispatch N workers concurrently, each in its own
exec-sandbox, track per-worker state, handle partial failures without halting
other in-flight workers, and aggregate results. Pass `go test -race` to confirm no
data races under concurrent dispatch.

## Context

ADR 042 (fixed decision by project owner): "Multiple concurrent workers from the
start, not a single-worker proof of concept." The single-worker path (task 081) is
the prerequisite; this task adds the concurrency layer. Multi-worker is what makes
agent-mesh (task 083) and memory-guard (task 084) prerequisites rather than niceties.

**Blocked by tasks 081 and 083.** The orchestrator core must exist and agent-mesh
must be the dispatch transport before adding concurrency on top. **Detailed task
shape is deferred** pending those prerequisites.

## Requirements

| Req ID     | Description                                                                                                                                   | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-086-01 | Orchestrator dispatches all approved sub-goals concurrently (all start before any complete); each in its own exec-sandbox. | must have |
| REQ-086-02 | A worker failure does not halt other in-flight workers; best-effort completion. | must have |
| REQ-086-03 | Aggregated result reflects per-worker outcomes correctly (success + failure mix). | must have |
| REQ-086-04 | `go test -race -count=1 ./internal/orchestrator/...` exits 0 with no race conditions. | must have |
| REQ-086-05 | Fleet audit chain covers all N concurrent workers; `audit-trail verify` → `valid=true`. | must have |

## Readiness gate

- [x] Test spec `086-multi-worker-concurrent-dispatch-test-spec.md` exists (written first)
- [ ] Task 081 merged (orchestrator core — single worker working)
- [ ] Task 083 merged (agent-mesh transport — needed for signed inter-agent dispatch)
- [ ] Concurrency model and partial-failure policy decided
- [ ] All test cases in test spec refined into full inputs/expected-outputs
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-086-01] TC-086-01: Plan with 3 sub-goals → all 3 workers started before any completes (stub timing or call-ordering assertion)
- [ ] [REQ-086-02] TC-086-02: Worker 1 fails immediately; workers 2 and 3 complete; orchestrator continues and reports all 3 outcomes
- [ ] [REQ-086-03] TC-086-03: 2 success + 1 failure → aggregated report accurate; delivered through channel
- [ ] [REQ-086-04] TC-086-04: `go test -race -count=1 ./internal/orchestrator/...` exits 0
- [ ] [REQ-086-05] TC-086-05: 3 concurrent workers → fleet audit chain has events for all 3; `audit-trail verify` → `valid=true`

## Verification plan

- **Highest level achievable:** L2 with race detector. L5 requires a live
  multi-worker orchestrator run with real exec-sandbox and real workers.
- **Harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/...
  make check
  ```
  Expected: `ok` (no races); `All checks passed.`

## Out of scope

- Worker quota / concurrency limits (max-N-concurrent) — follow-on tuning task.
- Cross-host worker dispatch.
- Worker priority / scheduling order.

## Dependencies

- Task 081 (orchestrator core)
- Task 083 (agent-mesh transport)
- Task 085 (orchestrator containment + policy — concurrent dispatch should run on the
  fully-secured orchestrator)
