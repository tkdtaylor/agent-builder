# Test spec — Task 083: agent-mesh adoption (orchestrator↔worker transport)

**Linked task:** `docs/tasks/backlog/083-agent-mesh-adoption.md`
**Written:** 2026-06-27
**Status:** stub — blocked by task 081 (orchestrator core)

## Context

agent-mesh provides Ed25519-signed envelopes + replay prevention for inter-agent
messaging. This task adopts agent-mesh as the transport for orchestrator↔worker
messaging, replacing any in-process or localhost call with the agent-mesh block's
signed-envelope contract.

This task is **blocked by task 081** (orchestrator core must exist before there is
a transport to replace).

**Detailed task shape is deferred** pending orchestrator core delivery. Shape
parameters that are known now:
- `internal/agentmesh` — a new adapter package (mirrors the pattern of
  `internal/audit` for audit-trail, `internal/vault` for vault, `internal/policy`
  for policy-engine).
- The block lives at `~/Code/Public/agent-mesh` (Ed25519 signed envelopes + replay
  prevention, v0 single commit).
- An ADR covering the adoption decision may be needed before implementation.
- Must add an isolation fitness check (F-0XX) mirroring F-005/F-006 pattern:
  `internal/agentmesh` is a leaf; `internal/orchestrator` reaches agent-mesh only
  over IPC/signed envelopes.

## Requirements coverage (preliminary)

| Req ID     | Description                                                                   | Test cases |
|------------|-------------------------------------------------------------------------------|------------|
| REQ-083-01 | Orchestrator sends work items to workers as Ed25519-signed envelopes           | TC-083-01  |
| REQ-083-02 | Worker results returned to orchestrator via signed envelopes                   | TC-083-02  |
| REQ-083-03 | Replayed envelope (same nonce) is rejected at the receiving end               | TC-083-03  |
| REQ-083-04 | internal/agentmesh is a leaf; orchestrator reaches it only over IPC           | TC-083-04  |
| REQ-083-05 | agent-mesh binary not present → orchestrator fails closed, not silently        | TC-083-05  |

## Pre-implementation checklist

- [ ] Task 081 merged (orchestrator core)
- [ ] agent-mesh block API surveyed (Go library vs binary IPC vs network socket)
- [ ] ADR for agent-mesh adoption written (if scope warrants one)
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-083-01 — Orchestrator sends a work item as a signed envelope

- **Requirement:** REQ-083-01
- **Level:** L2 (unit test with stub agent-mesh process)
- **Status:** stub

**Input:** Orchestrator dispatches a sub-goal to a worker.

**Expected output:**
- The work item is serialized as an Ed25519-signed envelope.
- The stub worker receives a verifiable envelope (signature valid, nonce fresh).

---

### TC-083-02 — Worker result returned as a signed envelope

- **Requirement:** REQ-083-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Worker completes successfully; returns result.

**Expected output:**
- Result is signed by the worker's key.
- Orchestrator verifies the worker signature before accepting the result.

---

### TC-083-03 — Replayed envelope is rejected

- **Requirement:** REQ-083-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** A valid signed work-item envelope replayed (same nonce, different timestamp).

**Expected output:**
- The replay check fires; the envelope is dropped.
- An audit event is emitted.

---

### TC-083-04 — internal/agentmesh is a leaf (fitness check)

- **Requirement:** REQ-083-04
- **Level:** L3 (fitness check — mirrors F-005/F-006 pattern)
- **Status:** stub

**Input:** `go list -deps ./internal/agentmesh/...`

**Expected output:**
- No `agent-builder/internal/` path other than `internal/agentmesh` itself.
- A `make fitness-agentmesh-isolation` target is added and wired into `make fitness`.

---

### TC-083-05 — Missing agent-mesh binary → fail closed

- **Requirement:** REQ-083-05
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** `AGENT_BUILDER_AGENT_MESH_BIN` unset or pointing to a nonexistent path.

**Expected output:**
- Orchestrator startup fails loudly before accepting any goals.
- Error message names the missing binary.

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 with stub agent-mesh process. L5 requires a live
  agent-mesh binary; L6 requires a live orchestrator↔worker round-trip.
- **L2 harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/agentmesh/...
  ```
  Expected: `ok`.

## Out of scope

- memory-guard for orchestrator state (task 084).
- Multi-worker dispatch (task 085).
- Key management / key distribution for worker signing keys.
