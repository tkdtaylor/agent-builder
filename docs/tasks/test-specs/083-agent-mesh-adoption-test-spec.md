# Test spec — Task 083: orchestrator↔worker transport (signed envelopes via internal/envelope)

**Linked task:** `docs/tasks/backlog/083-agent-mesh-adoption.md`
**Written:** 2026-06-27
**Revised:** 2026-06-27 — agent-mesh framing corrected per ADR 045: the shared envelope
  leaf is `internal/envelope` (task 096), not a separate `internal/agentmesh` IPC client;
  REQ-083-04/05 updated accordingly; dependency on 096 added. If agent-mesh's live
  A2A transport between worker processes is also needed (distinct from envelope signing),
  that is a clearly-separated concern deferred to task detailing after task 081 lands.
**Status:** stub — blocked by task 081 (orchestrator core)

## Context

The orchestrator↔worker transport provides signed, replay-resistant envelopes for
work items dispatched to workers and results returned from workers. This ensures that
work items cannot be forged and results cannot be replayed or tampered before the
orchestrator accepts them.

**ADR 045 (accepted 2026-06-27) corrects the original agent-mesh framing:**

- The original task assumed `internal/agentmesh` would be an IPC leaf client wrapping
  an `agent-mesh` binary for sign/verify operations (similar to how `internal/audit`
  wraps `audit-trail emit`).
- The agent-mesh survey (ADR 045 §Context) found: agent-mesh is `package main` (not
  importable), and exposes **no** sign/verify filter verb — only a two-process A2A
  `serve`/`sendto` pair with no `sign <stdin> → envelope` one-shot. There is no binary
  to wrap for the sign/verify operation.
- **The sign/verify primitive is `internal/envelope` (task 096)**, the same shared leaf
  that task 080 (Telegram adapter) uses. Both tasks import it; it is already the
  correct factoring.
- If task 083 additionally needs agent-mesh's **live A2A transport** (running `agent-mesh
  serve` on workers and `agent-mesh sendto` from the orchestrator for actual TCP/HTTP
  message delivery between processes), that is a **distinct and separately-scoped
  concern** from the envelope signing. The scope of that A2A concern is to be confirmed
  once task 081 (orchestrator core) lands — it may be that the orchestrator uses a
  different in-process or gRPC transport instead, with `internal/envelope` providing
  only the signing layer.

**Retained from the original spec (unchanged):** the task is blocked by task 081
(orchestrator core must exist before the transport is needed). This task's
REQ-083-04 and REQ-083-05 are revised below.

## Requirements coverage

| Req ID     | Description                                                                                       | Test cases  |
|------------|---------------------------------------------------------------------------------------------------|-------------|
| REQ-083-01 | Orchestrator sends work items to workers as Ed25519-signed envelopes (via `internal/envelope`)    | TC-083-01   |
| REQ-083-02 | Worker results returned to orchestrator as signed envelopes; orchestrator verifies before accepting | TC-083-02  |
| REQ-083-03 | Replayed envelope (same nonce) is rejected at the receiving end; audit event emitted             | TC-083-03   |
| REQ-083-04 | The transport adapter (`internal/channel/worker` or equivalent) is a leaf: no other `agent-builder/internal/` imports except `internal/envelope` and `internal/supervisor`; a fitness check asserts this | TC-083-04 |
| REQ-083-05 | Orchestrator worker-transport startup fails loudly when required key material or transport config is missing | TC-083-05 |

**Notes on the revised REQ-083-04 and REQ-083-05:**
- REQ-083-04 no longer references `internal/agentmesh` (that package does not exist
  and was predicated on a non-existent binary IPC seam). The transport adapter package
  name and its exact isolation invariant are to be confirmed post-task-081; the requirement
  is that whatever adapter package is created is a leaf using `internal/envelope`.
- REQ-083-05 replaces the `AGENT_BUILDER_AGENT_MESH_BIN` binary-missing check with a
  startup check for required **key material** or transport configuration. The exact
  config knob names are to be confirmed once the orchestrator core (task 081) defines
  its configuration surface. The principle (fail loudly before accepting goals when
  required config is absent) is preserved.

## Pre-implementation checklist

- [ ] Task 081 merged (orchestrator core — this task is blocked until then)
- [ ] Task 096 merged (`internal/envelope` leaf — required before this task can implement)
- [ ] Post-081 scoping: confirm whether the orchestrator↔worker transport uses agent-mesh A2A (serve/sendto) or a different mechanism (gRPC, in-process channels, etc.); if agent-mesh A2A, scope the IPC wiring separately
- [ ] All test cases refined into full inputs/expected-outputs (post-081 scoping)
- [ ] `make check` green before branching

---

## Test cases (stubs — to be expanded once task 081 is merged and transport mechanism confirmed)

### TC-083-01 — Orchestrator sends a work item as a signed envelope

- **Requirement:** REQ-083-01
- **Level:** L2 (unit test with stub worker)
- **Status:** stub

**Input:** Orchestrator dispatches a sub-goal to a worker via the transport adapter.

**Expected output:**
- The work item is serialized as an Ed25519-signed `internal/envelope.Envelope`.
- The stub worker can call `envelope.Verify(received, orchEdPub)` and receive `nil`.
- The envelope's `Payload` field (when decrypted with `envelope.Open`) equals the
  original sub-goal plaintext.
- The nonce is unique per dispatch (a fresh random nonce, not reused).

---

### TC-083-02 — Worker result returned as a signed envelope; orchestrator verifies before aggregating

- **Requirement:** REQ-083-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Worker completes successfully; returns result through the transport adapter.

**Expected output:**
- Result is sealed and signed by the worker's Ed25519 private key.
- Orchestrator calls `envelope.Verify(result, workerEdPub)` before incorporating the
  result — verification succeeds.
- A result with a bad signature (tampered `Sig` field) is NOT incorporated; an audit
  event is emitted with a rejection reason.

---

### TC-083-03 — Replayed work-item envelope is rejected at the receiver

- **Requirement:** REQ-083-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** A valid signed work-item envelope is delivered once (accepted), then
delivered again with the same nonce (replayed).

**Expected output:**
- First delivery: accepted; worker processes the work item.
- Second delivery (same nonce): `ReplayCache.Check` rejects with a replay error.
- An audit event is emitted with `"replay"` or `"nonce"` in the reason.
- The replayed work item is NOT processed.

---

### TC-083-04 — Transport adapter is a leaf; fitness check asserts its isolation

- **Requirement:** REQ-083-04
- **Level:** L3 (fitness check — to be defined post-task-081 scoping)
- **Status:** stub

**Note:** The exact package name and fitness-check target are to be confirmed after
task 081 lands. The pattern mirrors F-005 / F-006 / F-007:

**Input (when defined):** `go list -deps ./internal/channel/worker/...` (or
`./internal/transport/...` — name TBD)

**Expected output:**
- No `agent-builder/internal/` import other than the adapter itself, `internal/envelope`,
  and `internal/supervisor`.
- A `make fitness-worker-transport-isolation` target (or equivalent) added to the
  `.PHONY` list and `fitness:` prerequisites.

---

### TC-083-05 — Missing key material or transport config → orchestrator startup fails loudly

- **Requirement:** REQ-083-05
- **Level:** L2 (unit test)
- **Status:** stub

**Note:** The exact config knob name(s) for worker transport key material are to be
confirmed post-task-081. The requirement pattern:

**Input:** Start the transport adapter with required key material unset or pointing to
a nonexistent path (e.g. `AGENT_BUILDER_WORKER_SIGNING_KEY` unset, or key file absent).

**Expected output:**
- Orchestrator startup fails loudly (non-zero exit or named error) before accepting any goals.
- Error message names the missing configuration (not a generic "config error").
- The failure occurs at startup, not at first message receipt.

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 with stub worker processes. L5 requires a live
  orchestrator + worker round-trip (post-task-081). L6 requires a live multi-process
  A2A run with real envelopes.
- **L2 harness command (to be confirmed post-task-081):**
  ```
  go test -count=1 ./internal/channel/worker/...
  ```
  (Package name TBD — confirmed when task 081 defines the transport shape.)
  Expected: `ok`.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- `internal/envelope` implementation — task 096.
- Orchestrator core — task 081 (this task depends on it).
- memory-guard for orchestrator state — task 084.
- Multi-worker concurrent dispatch — task 086.
- Key management / key distribution for worker signing keys.
- If agent-mesh A2A transport (`serve`/`sendto` HTTP+JSON-RPC) is adopted for
  the actual delivery mechanism between processes, that wiring is a distinct, clearly-
  separated concern from envelope signing and may be treated as a sub-task or
  follow-on once the transport mechanism is confirmed post-task-081.
