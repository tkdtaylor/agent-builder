# Task 032: Phase 0 end-to-end acceptance

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed

## Goal
Prove the Phase 0 system can execute one representative task end to end: select it, run the executor inside the configured containment/run adapter, pass the verification gate, produce a branch artifact, collect logs, and report a final outcome.

## Context
- Tech stack: Go
- Roadmap: `docs/plans/roadmap.md` Phase 0
- Related ADRs: ADR 002, ADR 012, ADR 013, ADR 014, ADR 015, ADR 016, ADR 020, ADR 024
- Dependencies: 028, 029, 030, 031, 033, 034
- This is the final bootstrap acceptance task before treating Phase 0 as ready for the first Phase 1 exec-sandbox task.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | The end-to-end harness starts from a fixture backlog task and target worktree, then runs through the production CLI `run` path. | must have |
| REQ-002 | The executor produces a non-empty branch artifact and the Gate passes against the target worktree before success is reported. | must have |
| REQ-003 | The run record contains task ID, box/run ID, command stream, stdout/stderr stream, gate result summary, branch, PR artifact when configured, and terminal outcome. | must have |
| REQ-004 | Failure paths for executor failure, Gate failure, timeout, and blocked ingestion produce non-success outcomes without marking the task done. | must have |
| REQ-005 | The final evidence is recorded in the coverage tracker and roadmap without overstating unsupported real-provider behavior. | must have |

## Readiness gate
- [x] Test spec `032-phase0-end-to-end-acceptance-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks complete: 028, 029, 030, 031, 033, and 034

## Acceptance criteria
- [x] [REQ-001] A runtime-visible harness invokes the built `agent-builder run` binary, not just package-level fakes.
- [x] [REQ-002] Success requires executor branch output and a passing production Gate.
- [x] [REQ-003] Run-record NDJSON survives teardown and contains the required lifecycle, branch, PR artifact, and terminal fields.
- [x] [REQ-004] Negative harness cases prove failures do not mutate task status to done or claim verification success.
- [x] [REQ-005] Roadmap/spec/tracker language says Phase 0 is accepted only to the level actually observed: fake-provider harness, real containment, real sandbox-runtime, or real Claude as applicable.

## Verification plan
- **Highest level achievable:** L5/L6 - L5 with fake provider CLIs is required; L6 with real containment/provider is optional and must be labeled honestly.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/e2e -run TestPhase0EndToEndAcceptance
  ```
  Expected final assertion: `TC-001 Phase 0 accepted: task selected, branch produced, gate passed, run record persisted`
- **Level 6 - Operator observation:**
  - Binary path: `agent-builder run` with real configured containment and provider credentials in an approved environment
  - Targeted behaviour to observe: one task completes with branch artifact, Gate pass, logs persisted, and no bypass of armor/egress controls.
- **Cross-module state risk:** full pipeline; producer-consumer trace required from task source through executor, Gate, status writer, run record, and teardown.
- **Runtime-visible surface:** CLI, run-record file, branch artifact, and task/tracker status.

## Out of scope
- Building exec-sandbox v0 itself.
- Multi-provider routing.
- Product UI or public release packaging.

## Notes
- This task should be the point where "component-complete" turns into "Phase 0 accepted." Keep the evidence labels precise; fake-provider L5 is useful, but it is not the same as real Claude or real Podman L6.
