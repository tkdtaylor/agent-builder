# Task 083: agent-mesh adoption (orchestrator↔worker transport)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Adopt the agent-mesh block as the transport for orchestrator↔worker messaging:
wrap work items and results in Ed25519-signed envelopes with replay prevention before
delivery. Add a `internal/agentmesh` leaf adapter package (mirrors the
`internal/policy`/`internal/vault` pattern). Add a `make fitness-agentmesh-isolation`
fitness check asserting the adapter stays a leaf reached IPC-only.

## Context

ADR 042: "The orchestrator coordinates N workers concurrently; agent-mesh becomes the
orchestrator↔worker transport." The block exists at `~/Code/Public/agent-mesh` (v0;
Ed25519 signed envelopes + replay prevention). Before this task can be scoped fully,
the agent-mesh block API must be surveyed and an adoption ADR may be needed.

**Blocked by task 081** (orchestrator core must exist before the transport is needed).
**Detailed task shape deferred** pending orchestrator core delivery and block API survey.

## Requirements

| Req ID     | Description                                                                                                                       | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-083-01 | Work items from orchestrator to workers are signed Ed25519 envelopes; workers verify the signature before processing. | must have |
| REQ-083-02 | Worker results returned to orchestrator are signed; orchestrator verifies before accepting. | must have |
| REQ-083-03 | Replayed envelopes (same nonce) are rejected at the receiver. | must have |
| REQ-083-04 | `internal/agentmesh` is a leaf (no other `agent-builder/internal/` imports); a fitness check asserts this. | must have |
| REQ-083-05 | `AGENT_BUILDER_AGENT_MESH_BIN` unset or invalid → orchestrator startup fails loudly. | must have |

## Readiness gate

- [x] Test spec `083-agent-mesh-adoption-test-spec.md` exists (written first)
- [ ] Task 081 merged (orchestrator core)
- [ ] agent-mesh block API surveyed; adoption ADR written if warranted
- [ ] All test cases in test spec refined into full inputs/expected-outputs
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-083-01] TC-083-01: Orchestrator sends signed work item; stub worker receives verifiable envelope
- [ ] [REQ-083-02] TC-083-02: Worker result signed; orchestrator verifies before aggregating
- [ ] [REQ-083-03] TC-083-03: Replayed envelope rejected; audit event emitted
- [ ] [REQ-083-04] TC-083-04: `go list -deps ./internal/agentmesh/...` → leaf; `make fitness-agentmesh-isolation` → PASS
- [ ] [REQ-083-05] TC-083-05: Missing binary → orchestrator startup fails with named error

## Verification plan

- **Highest level achievable:** L2 with stub agent-mesh process. L5 requires live
  agent-mesh binary.
- **Harness command (to be confirmed post-API survey):**
  ```
  go test -count=1 ./internal/agentmesh/...
  make fitness-agentmesh-isolation
  make check
  ```
  Expected: `ok`; PASS; `All checks passed.`

## Out of scope

- memory-guard (task 084).
- Multi-worker concurrent dispatch (task 086).
- Key management / key distribution for worker signing keys.

## Dependencies

- Task 081 (orchestrator core)
- Informs: tasks 085, 086 (containment/audit and concurrent dispatch both need the transport)
