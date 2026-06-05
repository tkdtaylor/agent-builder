# Task 028: default run wiring

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed

## Goal
Make `agent-builder run` construct the real Phase 0 pipeline instead of an empty supervisor, so one configured task can be picked, attempted, verified, logged, and torn down through the repo-owned seams.

## Context
- Tech stack: Go
- Roadmap: `docs/plans/roadmap.md` Phase 0 flip-the-switch goal
- Related ADRs: ADR 002, ADR 012, ADR 013, ADR 020, ADR 024
- Dependencies: 010, 012, 017, 018, 019, 020, 021, 022, 023, 027
- Audit finding: `agent-builder run` currently defaults to `supervisor.New().Run()` and exits with `supervisor: nil containment box`.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | The default `run` command builds a non-nil task source, executor, gate, containment/run adapter, in-box loop, timeout, and run-record path from explicit configuration. | must have |
| REQ-002 | One `agent-builder run` invocation dispatches at most one ready task and records pick, attempt, verify, and terminal outcome evidence. | must have |
| REQ-003 | The runtime wiring preserves the supervisor isolation invariant: `internal/supervisor` still imports no executor, ingestion, armor, web, or LLM packages. | must have |
| REQ-004 | Missing required runtime configuration fails before any executor attempt with a clear usage/configuration error. | must have |

## Readiness gate
- [x] Test spec `028-default-run-wiring-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks are complete or explicitly stubbed through fakeable seams in the test harness

## Acceptance criteria
- [x] [REQ-001] `agent-builder run` no longer reaches `supervisor.ErrNilContainmentBox`, `supervisor.ErrNilInBoxLoop`, or `supervisor.ErrMissingTask` on a fully configured fixture.
- [x] [REQ-002] A runtime-visible harness run proves one task is selected, attempted by the executor, verified by the Gate, and written to a durable run record.
- [x] [REQ-003] `make fitness-supervisor-isolation` remains green.
- [x] [REQ-004] Missing task source, worktree, executor token, sandbox runtime, or run configuration exits non-zero before task mutation and names the missing setting.

## Verification plan
- **Highest level achievable:** L5 - runtime binary harness with fake external CLIs and a fixture task/worktree exercises the real `run` command path end to end without touching production credentials.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/cli ./tests/supervisor -run 'TestRuntimeRunWiresPhase0Pipeline|TestRunConfigFailures'
  ```
  Expected final assertion: `TC-005 runtime run completed one configured task and persisted run_finished`
- **Cross-module state risk:** CLI configuration feeds task source, supervisor, in-box loop, executor, Gate, and run-record writer; producer-consumer trace required in the harness output.
- **Runtime-visible surface:** CLI exit code, stdout/stderr, and run-record NDJSON.

## Out of scope
- Real Claude Code authentication or a real model invocation.
- Real Podman or sandbox-runtime network isolation evidence; task 030 owns that runtime security proof.
- GitHub PR creation; task 032 owns final Phase 0 acceptance evidence.

## Notes
- Keep the trusted supervisor dumb. Runtime assembly can live in CLI/bootstrap wiring, but must not make `internal/supervisor` import executor, armor, ingestion, web, or LLM packages.
- Prefer fakeable process seams for tests so the harness proves wiring without spending tokens or requiring provider credentials.
