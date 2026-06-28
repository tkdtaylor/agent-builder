# ADR 048 — orchestrator↔worker transport: post-081 scope (in-process v1, envelope as the security layer, A2A deferred)

**Status:** Accepted (2026-06-28) — design-only. Resolves the post-task-081 scoping
questions in task 083's readiness gate (transport mechanism; adapter package name;
whether agent-mesh A2A is needed) so its stub test spec can be expanded and implemented.
No code, spec, or diagram changes land with this ADR.
**Date:** 2026-06-28
**Extends:** ADR 042 (the orchestrator coordinates N workers; the orchestrator↔worker
boundary is a trust boundary), ADR 045 (the sign/verify/seal/replay primitive is
`internal/envelope`, task 096 — agent-mesh is `package main`, not an importable
sign/verify filter). Does not contradict either.
**Motivated by:** task 083 (`docs/tasks/backlog/083-agent-mesh-adoption.md`) whose
readiness gate explicitly defers three decisions to "post-task-081 scoping." Task 081
(orchestrator core) is now merged, which fixes the facts needed to decide.

## Context

Task 081 shipped the orchestrator core: it dispatches one worker per sub-goal
**sequentially, in-process**, by invoking `runtime.Run` (the existing per-worker
assembly) via a `DispatchFunc` seam. The actual agent runs inside an exec-sandbox
container, but the orchestrator→worker *coordination* (hand the sub-goal's
`supervisor.Task` to the assembly, get a result back) is an in-process function call
today — there is no separate worker process and no wire between orchestrator and worker.

ADR 045 already established that the sign/verify/seal/replay primitive is
`internal/envelope` (task 096), and that agent-mesh's live A2A transport
(`serve`/`sendto`) is a *separate* concern from envelope signing. Task 083's test spec
names `internal/channel/worker` as the adapter package and scopes its TCs to: work-items
and results carried as signed+sealed envelopes, replay rejection at the receiver, leaf
isolation, and a loud startup failure when key material is absent.

The open question 083 left for "post-081": is the orchestrator↔worker transport an
in-process mechanism, gRPC, or agent-mesh A2A — and does signing an *in-process* message
even make sense?

## Decision

### 1. Transport mechanism — **in-process delivery for v1**, behind a transport-adapter seam

The v1 orchestrator↔worker transport is **in-process** (matching task 081's
sequential `runtime.Run` dispatch). No separate worker process, no gRPC, no agent-mesh
A2A is introduced now. The transport adapter is an abstraction (`internal/channel/worker`)
with an in-process concrete for v1; an out-of-process/cross-host concrete can implement
the same seam later without changing the orchestrator.

### 2. `internal/envelope` is the load-bearing security layer **regardless of the wire**

Even in-process, work-items and results are wrapped in `internal/envelope.Envelope`
(Ed25519-signed, X25519+AEAD-sealed, replay-checked). The value is **not** protecting an
in-process call from a network attacker — it is three things ADR 042 wants at this trust
boundary now, so they don't have to be retrofitted:
- **Tamper-evidence + provenance** — signed work-items/results are auditable records (the
  fleet audit chain, task 085, records them).
- **Replay resistance** — the `ReplayCache` from task 096 rejects a re-delivered envelope
  (same nonce) at the receiver, exactly as the inbound channel does.
- **A ready seam** — when a worker becomes out-of-process or cross-host (concurrency in
  task 086, or a future distributed mode), the wire is already hostile-by-assumption and
  the envelope already protects it; no security retrofit is needed.

This mirrors ADR 045's treatment of the Telegram channel: the transport (Telegram /
in-process) is dumb and assumed untrusted; the envelope is the boundary.

### 3. Adapter package — **`internal/channel/worker`**, a leaf

The transport adapter lives at `internal/channel/worker` (sibling of
`internal/channel/telegram`). It is a **leaf**: its only `agent-builder/internal/`
imports are `internal/envelope` and `internal/supervisor` (REQ-083-04). A fitness check
(`make fitness-worker-transport-isolation` or equivalent, mirroring F-005/F-006/F-007)
asserts this. This keeps the crypto/transport off the supervisor's import graph (F-003)
and out of every other package.

### 4. Key material + startup check (REQ-083-05)

The orchestrator's worker-transport requires signing/sealing key material (the
orchestrator's Ed25519 + X25519 private keys and the workers' trusted public keys). When
that material is **absent or unreadable at startup**, the orchestrator fails **loudly with
a named error before accepting any goals** — not at first message receipt. The exact
config knob name (e.g. `AGENT_BUILDER_WORKER_SIGNING_KEY`) is fixed by task 083 against
the orchestrator's existing config surface; the principle (fail-closed, named, at startup)
is the binding part.

### 5. agent-mesh A2A live transport — **deferred as a distinct concern**

Running `agent-mesh serve` on workers and `agent-mesh sendto` from the orchestrator for
real cross-process delivery is **not** adopted here. It is a separate, clearly-scoped
concern (its own IPC adapter + config surface) to be picked up only if/when out-of-process
workers are actually needed. The envelope signing layer built in 083 is independent of it
and does not change if A2A is later adopted (A2A would be a new transport concrete behind
the same `internal/channel/worker` seam).

## Consequences

- **Design-only.** No code/spec/diagram change lands with this ADR; task 083 makes them.
- **Task 083 is unblocked.** Its TCs expand concretely: TC-083-01/02 sign+seal a
  work-item/result and verify+open at the receiver (byte-exact round-trip); TC-083-03
  replays a nonce and asserts `ReplayCache` rejection + an audit event; TC-083-04 is the
  leaf fitness check on `internal/channel/worker`; TC-083-05 is the startup
  missing-key-material loud failure.
- **No premature distribution.** v1 stays a single process (Unix "defer premature
  decisions" — no gRPC/A2A until a second concrete use case demands it), while the
  envelope seam means the distributed mode is a drop-in, not a rewrite.
- **Feeds 085 + 086.** Task 085 (fleet audit) records the signed work-items/results as
  tamper-evident chain entries; task 086 (concurrent dispatch) reuses the same envelope
  transport for N concurrent workers — concurrency does not weaken the per-message
  signing/replay guarantees.
- **All ADR 042/045 invariants hold:** the orchestrator↔worker boundary is treated as a
  trust boundary even in-process; the envelope is the single sign/verify/seal/replay
  primitive; agent-mesh stays a non-importable external concern, adopted (if ever) only
  for live A2A delivery behind the same adapter seam.
