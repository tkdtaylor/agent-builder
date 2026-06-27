# Task 084: memory-guard adoption (orchestrator goal/fleet state)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Adopt the memory-guard block to protect the orchestrator's long-lived goal/fleet
state (in-flight plan, active worker set, aggregated results) with write-gate +
delete-verify. Add a `internal/memoryguard` leaf adapter package (mirrors the
`internal/policy`/`internal/vault` pattern). When unset (`AGENT_BUILDER_MEMORY_GUARD_BIN`
absent), the orchestrator degrades gracefully to in-memory-only state with a logged
warning.

## Context

ADR 042: "memory-guard becomes the guard on the orchestrator's long-lived goal/fleet
state." The block exists at `~/Code/Public/memory-guard` (v0; write-gate +
delete-verify). Before this task can be scoped fully, the block API must be surveyed
and an adoption ADR may be needed.

**Blocked by task 081** (orchestrator core must own state before there is state to
guard). **Detailed task shape deferred** pending orchestrator core delivery and block
API survey.

## Requirements

| Req ID     | Description                                                                                                                                    | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-084-01 | Orchestrator writes goal/fleet state through memory-guard write-gate when configured. | must have |
| REQ-084-02 | Delete-verify: attempts to delete state bypassing the guard result in a tamper-detected error on the next read. | must have |
| REQ-084-03 | `internal/memoryguard` is a leaf; a fitness check asserts this. | must have |
| REQ-084-04 | `AGENT_BUILDER_MEMORY_GUARD_BIN` unset → in-memory-only mode with a structured warning log; existing e2e tests pass unchanged. | must have |
| REQ-084-05 | memory-guard stub returns tamper-detected → orchestrator halts the plan; audit event emitted with `tamper_detected=true`. | must have |

## Readiness gate

- [x] Test spec `084-memory-guard-adoption-test-spec.md` exists (written first)
- [ ] Task 081 merged (orchestrator core — state ownership clear)
- [ ] memory-guard block API surveyed; adoption ADR written if warranted
- [ ] All test cases in test spec refined into full inputs/expected-outputs
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-084-01] TC-084-01: Orchestrator writes a new goal → goes through write-gate; stub returns success; state handle held
- [ ] [REQ-084-02] TC-084-02: Simulated delete bypass → subsequent read returns tamper-detected error from stub
- [ ] [REQ-084-03] TC-084-03: `go list -deps ./internal/memoryguard/...` → leaf; `make fitness-memoryguard-isolation` → PASS
- [ ] [REQ-084-04] TC-084-04: `AGENT_BUILDER_MEMORY_GUARD_BIN` unset → orchestrator starts; structured warning in log; `TestPhase0EndToEndAcceptance` still passes
- [ ] [REQ-084-05] TC-084-05: Tamper-detected error → orchestrator halts plan; `audit.FakeSink` receives event with `tamper_detected=true`

## Verification plan

- **Highest level achievable:** L2 with stub memory-guard. L5 requires live
  memory-guard binary.
- **Harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/memoryguard/...
  make fitness-memoryguard-isolation
  make check
  ```
  Expected: `ok`; PASS; `All checks passed.`

## Out of scope

- Orchestrator containment + policy gating (task 085).
- State migration / schema evolution.

## Dependencies

- Task 081 (orchestrator core)
- Informs: task 085 (containment + audit requires state to be guarded first)
