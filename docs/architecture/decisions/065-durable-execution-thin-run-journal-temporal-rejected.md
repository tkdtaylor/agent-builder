# ADR 065 — Durable execution via a thin run journal; Temporal rejected

**Status:** accepted
**Date:** 2026-07-11

**Motivated by:** the forward arc (roadmap "Forward arc — the general agent") needs durable cross-session run state, resume-after-restart, retries around flaky executors, human-approval pauses, and eventually an always-on daemon. Today the orchestrator is Handle → dispatchPlan → report → stop with an in-memory PlanStore; a crash mid-goal loses everything. Before building, we evaluated adopting Temporal (temporal.io) as the durable-execution engine.

## Context

Temporal scores well on paper. It directly implements four of the five capabilities we are missing: durable workflow state that survives restarts, per-activity retry policies, the signal + await + timeout pattern for human approval gates, and server-side schedules that approximate a heartbeat. Its Go SDK is the most mature one, the server and SDKs are MIT, and the precedent is strong: OpenAI Codex and Replit Agent, both shaped like our coding reference build, run on it in production.

The costs are structural, not incidental:

- **Operational weight.** Production posture is three services plus Postgres plus a metrics pipeline plus a separately deployed worker fleet. The single-binary SQLite path is officially a stepping stone, not blessed production. We are a single-node, single-operator system; this is a second distributed system inside a project whose stated rule is "adopt thin tools, don't build a framework."
- **Trusted-core exposure.** The workflow engine becomes control-plane code under the no-unattended-self-modification invariant, and the server is a very large Go dependency tree for dep-scan and code-scanner to gate.
- **Data boundary.** Every activity result (goals, plan contents, executor output) lands in Temporal's event history, a second durable store of sensitive payloads outside the vault and audit-trail boundary. Self-hosting contains it but it is still a new surface to defend.
- **Permanent determinism and versioning tax.** Workflow code must stay deterministic and every deploy over in-flight workflows requires `GetVersion` patching or worker build-ID pinning, forever.
- **Lock-in gradient.** The operator's independent assessment, which this ADR adopts: the product is set up so that the versioning, ops, and visibility pain of self-hosting pushes adopters toward the paid Temporal Cloud platform. Event histories would then live off-box, which the data-boundary point above rules out regardless of price.

Lighter alternatives in the same space (DBOS, River, Restate) fit the philosophy better but still add a Postgres dependency or a BSL-licensed sidecar for a problem our scale does not have.

## Decision

Reject Temporal, and durable-execution frameworks generally, for the orchestrator. Build the capability as a thin seam we own:

- A `RunStore` seam plus a stdlib file-backed run journal (append-only JSONL with snapshot/compaction, crash-safe temp+rename writes) recording goal, plan, per-task attempt state, pending approvals, and terminal status (task 167).
- Resume-after-restart rehydration from the journal with idempotent re-dispatch (task 168).
- The sustained-autonomy loop, approval pause/resume, daemon mode, and schedules all build on that journal (tasks 169-171, 174-175) instead of on an external engine.

The seam is the escape hatch: if agent-builder ever becomes multi-node or the workflow graph genuinely outgrows a journal, an engine slots in behind `RunStore` and the migration is a new ADR, not a rewrite.

## Consequences

- We own retries, timers, resume, and idempotency logic ourselves, in stdlib Go, tested like every other block adapter. That is real code, but it is small, auditable, and inside the existing gate.
- No new services, no new database, no new dependency tree for the scanners to gate. Forensic history stays in audit-trail, secrets stay in vault.
- The run journal becomes part of the trusted core: it is written by the orchestrator only, covered by fitness functions, and never edited unattended by the agent itself.
- Re-evaluation trigger: multi-node execution, or a demonstrated need for cross-process workflow signaling that the channel layer cannot carry. Either reopens this decision with a fresh ADR.
