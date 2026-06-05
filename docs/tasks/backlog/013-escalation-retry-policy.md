# Task 013: Escalation + retry-N + mandatory stop condition

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
The policy layer on the loop: on Gate fail, retry up to a configurable N attempts (with an escalation hook for a stronger executor — single-executor for bootstrap), then mark the task escalated/needs-human and advance — a mandatory stop condition guaranteeing the loop never thrashes infinitely.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 retry-N → escalate, stop condition mandatory; §5 escalate to stronger executor — router deferred)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.2)
- Related ADRs: ADR required: retry-N + escalation policy and the mandatory stop condition
- Dependencies: 012

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Configurable retry count N | must have |
| REQ-002 | After N failed attempts, the task is marked escalated (needs-human) and the loop advances — never loops forever | must have |
| REQ-003 | The escalation hook is a seam (single executor now, multi-provider router later) | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 012

## Acceptance criteria
- [ ] [REQ-001] Retry count N is configurable; the policy honours the configured value
- [ ] [REQ-002] With an always-failing Executor, exactly N attempts run, then the task is marked escalated/needs-human and the loop advances; attempt count is bounded and the loop terminates (no infinite loop)
- [ ] [REQ-003] The escalation step is a named seam invoked between attempts; bootstrap uses a single executor, and the seam is shaped so a router can be substituted without changing the policy

## Verification plan
- **Highest level achievable:** L5 — fake always-failing Executor → assert exactly N attempts, then one escalation, then loop termination (bounded attempt count, no infinite loop).
- Harness: `go test ./internal/loop/... -run TestEscalation`. Expected final assertion: attempt counter == N, task status == escalated/needs-human, loop returns/advances.
- **Cross-module state risk:** names the escalation outcome / policy config; on escalate it drives the status writer (011) to mark needs-human. Updates `docs/spec/<file>.md` in same commit (retry/escalation policy contract).
- **Runtime-visible surface:** none directly (status write goes through 011's surface).

## Out of scope
- The multi-provider executor router (deferred, post-v1)
- The happy-path loop state machine (task 012)

## Notes
- The mandatory stop condition is the load-bearing safety property: an autonomous loop with no bound thrashes forever. The test must prove termination, not just count attempts.
- The escalation hook exists now as a no-op/single-executor seam so §5's router can drop in later without reshaping the policy.
