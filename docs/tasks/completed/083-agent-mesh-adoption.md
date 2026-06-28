# Task 083: orchestrator↔worker transport (signed envelopes via internal/envelope)

**Project:** agent-builder
**Created:** 2026-06-27
**Revised:** 2026-06-27 — agent-mesh framing corrected per ADR 045: the sign/verify
  primitive is `internal/envelope` (task 096), not a separate `internal/agentmesh` IPC
  client wrapping an agent-mesh binary. REQ-083-04 (was: `internal/agentmesh` leaf
  reached IPC-only) and REQ-083-05 (was: `AGENT_BUILDER_AGENT_MESH_BIN` missing →
  fail closed) are updated. Dependency on task 096 added. If agent-mesh A2A transport
  is also needed, that is a distinct, separately-scoped concern to confirm post-task-081.
**Status:** backlog

## Goal

Build the signed-envelope transport for orchestrator↔worker messaging. Work items and
results are carried in `internal/envelope.Envelope` (Ed25519-signed, X25519+AEAD-sealed)
so they cannot be forged, tampered, or replayed. The signing/encryption primitives come
from `internal/envelope` (task 096) — the same shared leaf that the Telegram channel
adapter (task 080) uses.

## Context

ADR 042: "The orchestrator coordinates N workers concurrently; agent-mesh becomes the
orchestrator↔worker transport."

**ADR 045 (accepted 2026-06-27) corrects the integration assumption:**

The original task assumed `internal/agentmesh` would be an IPC leaf wrapping an
agent-mesh binary for sign/verify, analogous to `internal/audit` wrapping `audit-trail`.
The agent-mesh survey (ADR 045 §Context) found:

1. agent-mesh is `package main` — not importable as a Go library.
2. agent-mesh exposes no sign/verify filter CLI verb. Its only subcommands are
   `identity | demo | serve | serve-svid | sendto | tracer` — a two-process A2A
   HTTP+JSON-RPC pair. There is no `agent-mesh sign <stdin> → envelope` one-shot to wrap.
3. The correct integration for sign/verify is `internal/envelope` (task 096), which
   reimplements the thin Ed25519 construction over stdlib `crypto/ed25519`, adopting
   agent-mesh's wire format (Envelope JSON + `signingBytes()`) as the contract.

**Retained from the original spec:** this task is **blocked by task 081** (orchestrator
core must exist before the transport is needed). The task shape — package name, transport
mechanism (in-process channels, gRPC, or agent-mesh A2A serve/sendto), and the exact
config surface — is deferred to post-task-081 scoping. What is now fixed:

- The sign/verify primitive is `internal/envelope`, not a hypothetical `internal/agentmesh`.
- There is no `AGENT_BUILDER_AGENT_MESH_BIN` startup check (there is no such binary IPC
  for the sign/verify operation).
- If this task additionally wants agent-mesh's live **A2A transport** between worker
  processes (running `agent-mesh serve` on workers and `agent-mesh sendto` to dispatch),
  that is a distinct, clearly-separated concern from envelope signing and must be
  scoped separately with its own IPC adapter and config surface.

## Requirements

| Req ID     | Description                                                                                                                                                                         | Priority  |
|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-083-01 | Work items dispatched from orchestrator to workers are signed `internal/envelope.Envelope` objects (Ed25519); workers verify the signature before processing. | must have |
| REQ-083-02 | Worker results returned to orchestrator are signed envelopes; orchestrator verifies the worker's signature before aggregating the result. | must have |
| REQ-083-03 | Replayed envelopes (same nonce, whether at worker or orchestrator receiver) are rejected; an audit event is emitted. | must have |
| REQ-083-04 | The transport adapter package (name TBD post-task-081) is a leaf: no internal/ imports except `internal/envelope` and `internal/supervisor`; a fitness check asserts this (mirrors F-005/F-006/F-007 pattern). | must have |
| REQ-083-05 | Orchestrator worker-transport startup fails loudly when required key material or transport configuration is absent; the error names the missing configuration before any goals are accepted. | must have |

## Readiness gate

- [x] Test spec `083-agent-mesh-adoption-test-spec.md` exists (written first — 2026-06-27; revised 2026-06-27)
- [ ] Task 081 merged (orchestrator core — hard blocker)
- [ ] Task 096 merged (`internal/envelope` leaf — hard dependency for sign/verify)
- [ ] Post-081 scoping: confirm transport mechanism (in-process / gRPC / agent-mesh A2A) and determine whether agent-mesh A2A is needed and, if so, scope as a sub-task
- [ ] Package name for the transport adapter confirmed and documented
- [ ] All test cases in test spec refined into full inputs/expected-outputs (post-task-081)
- [ ] `make check` green before branching

## Acceptance criteria

- [ ] [REQ-083-01] TC-083-01: Orchestrator sends signed work item; stub worker receives a verifiable envelope (signature valid, `envelope.Verify` returns nil, nonce is unique per dispatch).
- [ ] [REQ-083-02] TC-083-02: Worker result signed; orchestrator verifies before aggregating; tampered result (bad signature) is NOT incorporated and an audit event is emitted.
- [ ] [REQ-083-03] TC-083-03: Replayed envelope rejected; audit event emitted; work item NOT processed.
- [ ] [REQ-083-04] TC-083-04: `go list -deps ./internal/channel/worker/...` (or equivalent) → leaf; `make fitness-worker-transport-isolation` (or equivalent name) → PASS.
- [ ] [REQ-083-05] TC-083-05: Required key material or transport config unset → orchestrator startup fails loudly with named error before accepting goals.

## Verification plan

- **Highest level achievable:** L2 with stub worker processes. L5 requires a live
  orchestrator + worker round-trip (post-task-081). L6 requires live multi-process run.
- **Harness command (to be confirmed post-task-081):**
  ```
  go test -count=1 ./internal/channel/worker/...
  make fitness-worker-transport-isolation
  make check
  ```
  Expected: `ok`; `PASS fitness-...`; `All checks passed.`

## Out of scope

- `internal/envelope` implementation — task 096.
- Orchestrator core — task 081.
- memory-guard for orchestrator state — task 084.
- Multi-worker concurrent dispatch — task 086.
- Key management / key distribution for worker signing keys.
- agent-mesh A2A transport (`serve`/`sendto`) — if needed, a separate concern confirmed
  and scoped post-task-081; NOT part of the envelope signing layer.

## Dependencies

- **Task 081** (orchestrator core — hard blocker; the transport adapter cannot exist
  before there is an orchestrator to wire it into)
- **Task 096** (`internal/envelope` leaf — provides `Sign`/`Verify`, `Seal`/`Open`,
  `ReplayCache`)
- Informs: tasks 085, 086 (containment/audit and concurrent dispatch both need the transport)
