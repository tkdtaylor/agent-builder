# ADR 026: audit-trail v0 — consume the shipped block via its CLI/IPC seam

**Date:** 2026-06-16
**Status:** Accepted
**Supersedes:** ADR 025 (audit-trail v0 — hash-chained reader over the RunRecord stream)

## Context

ADR 025 accepted **Option B**: build a small `internal/audit` package owning a
hash-chained append-only NDJSON `ChainWriter` and a companion `audit.Verify`
tamper-detector, and wire the supervisor to write action events through it. Tasks
038–042 were authored against that decision.

ADR 025 was written without checking the state of the **canonical `audit-trail`
block**. That block exists and is shipped: `github.com/tkdtaylor/audit-trail`
(`$HOME/Code/Public/audit-trail`, Go 1.26, standard library only, zero
third-party dependencies). A survey of it found that ADR 025's planned
`internal/audit` is a from-scratch reimplementation of work the block already
owns — and a *strictly weaker* one:

| Concern | ADR 025 plan (`internal/audit`) | `audit-trail` block (shipped) |
|---|---|---|
| Storage | append-only NDJSON | append-only JSONL ✅ |
| Hash chain | SHA-256 over canonical bytes of prev record | `SHA256(prev_hash + JCS(record))` ✅ |
| Canonical encoding | "sorted keys, hash fields excluded", golden fixture | RFC 8785 JCS, floats excluded, fixtures ✅ |
| Genesis | "documented sentinel constant" | 64 zeros, frozen ✅ |
| Verify | first-broken-link; edit/reorder/truncation | `verify() → {valid, tamper_detected_at}`, exit 0/1 ✅ |
| Seam | `Sink{Append; Seal}` | `emit()` + IPC `{op:emit}` + Go API ✅ |
| Isolation fitness | F-005 (no executor/LLM/web deps) | `fitness-no-deps` (zero `require`) ✅ |
| **Signing vs full rewrite** | **deferred** ("later task/ADR") | **shipped** — Ed25519 signed checkpoints |
| **External anchoring** | **deferred** | **shipped** — Rekor / witness anchoring |
| **Rotation** | not in scope | **shipped** — segmented logs + manifest |

The block's v1 emit/verify contract is **frozen** (`docs/CONTRACT.md`):

```
emit(event) -> { seq, hash }
  event = { ts, actor, action, target, decision?, refs, context?, +server: seq, prev_hash, hash }
verify() -> { valid, tamper_detected_at, message }
Transports: IPC (newline-JSON over Unix socket) and CLI (audit-trail emit / verify)
Canonicalization: RFC 8785 / JCS
```

Two facts make the ADR 025 plan the wrong call:

1. **It duplicates a shipped block.** Re-implementing a hash chain, canonical
   encoding, genesis sentinel, and tamper-detection verifier — the block's core
   competency, frozen and fitness-covered — is exactly the duplication the
   "one task = one repo" and "defer premature decisions / smallest thing that
   works" rules exist to prevent.
2. **It inverts agent-builder's thesis.** `CLAUDE.md` is explicit: agent-builder
   "builds the secure-agent ecosystem blocks (exec-sandbox, vault, policy-engine,
   **audit-trail**)" and is "the **first concrete consumer** of those blocks."
   `audit-trail` is one of those four blocks, and it is already built. The correct
   relationship is therefore *consume* it (the supervisor self-audits its own
   actions into the block), not *re-build* it. ADR 025's `internal/audit` builds a
   block that is done.

This ADR decides how agent-builder reaches the existing block instead.

## Options considered

### Option A — Consume via the block's CLI subprocess (selected)

`internal/audit` keeps the typed `AuditEvent` taxonomy + `Sink` seam + `FakeSink`
from task 038, but the **production** `Sink` is a thin `BlockSink` adapter that
maps each `AuditEvent` onto one `audit-trail emit --logfile <path> …` subprocess
call. Integrity verification is the block's own `audit-trail verify --logfile
<path>` invoked as the block-severity gate. The block binary is located via
`AGENT_BUILDER_AUDIT_BIN` (or `$PATH`); the chain file is `AGENT_BUILDER_AUDIT_RECORD`.

**Pros**
- **Zero duplication.** The chain format, canonicalization, genesis, verifier,
  and (for free, later) signing/anchoring/rotation all stay owned by the block.
- **Arm's-length coupling.** No Go import across the two private repos, no `go.mod`
  `replace` directive binding agent-builder's build to the block's source. Matches
  the project's "drive provider tools as subprocesses" pattern and "one task = one
  repo" boundary. The block is an external dependency reached over a process
  boundary, like the executor CLIs.
- **Isolation holds trivially.** `internal/audit` uses `os/exec` (and optionally
  `net` for the socket), not a Go import of executor/LLM/web code — the F-003 /
  F-005 leaf discipline survives unchanged.
- The block's CLI `emit` resumes chain state from disk on each invocation
  (`seq`/`prev_hash` reconstructed on open), so per-action-event subprocess calls
  produce one correct, continuous chain.

**Cons**
- One subprocess per action event (~7 per run). Acceptable for the action layer's
  low frequency; the IPC socket (Option B) is the documented upgrade if it ever
  matters. The raw stdout/stderr stream is **not** routed here (it stays in the
  019 RunRecord), so volume stays low.
- Requires the `audit-trail` binary to be present at run time. Resolved by failing
  fast (config error before dispatch) when `AGENT_BUILDER_AUDIT_RECORD` is set but
  the binary cannot be found — never by silently skipping the audit.

### Option B — Consume via the block's Unix-socket IPC

Run `audit-trail serve --socket <path>` as a sidecar; `BlockSink` dials the socket
and sends `{"op":"emit","event":{…}}` per action event, `{"op":"verify"}` for the
gate.

**Pros**
- One long-lived connection, no per-event process spawn; the block's intended
  "hot path" transport.

**Cons**
- Requires a sidecar lifecycle (start/stop/health) the supervisor must manage —
  more moving parts than v0 needs for ~7 events per run. The action layer is not a
  hot path. **Deferred** as the throughput upgrade; the `BlockSink` seam is shaped
  so swapping CLI → socket is an adapter-internal change.

### Option C — Import the block as a Go module

`import "github.com/tkdtaylor/audit-trail"`, call `NewChain()/Emit()/Verify()`
in-process via a `go.mod` `replace => ../../Public/audit-trail`.

**Pros**
- No subprocess; typed in-process calls.

**Cons**
- **Tight cross-repo coupling.** Binds agent-builder's build graph to the block's
  source via a local `replace` (both repos are private, no published module). A
  break or refactor in the block breaks agent-builder's compile. Drags the block's
  package into the supervisor's transitive import graph — directly in tension with
  the F-003 isolation boundary the audit wiring is supposed to preserve. Rejected
  for v0; the process boundary is the cleaner seam.

### Option D — Keep building `internal/audit` from scratch (ADR 025)

Proceed with the hand-rolled `ChainWriter` + `Verify`.

**Cons**
- Ships a strictly weaker reimplementation of a frozen, tested block (no signing,
  anchoring, or rotation), duplicates effort, and inverts the consumer thesis. This
  is the decision this ADR reverses.

## Decision

**Option A, accepted.** audit-trail v0 in agent-builder is a *consumer integration*
with the shipped `audit-trail` block, not a reimplementation.

`internal/audit` stays a small leaf package that owns:
- the typed `AuditEvent` action taxonomy (`containment, pick, attempt, verify,
  publish, escalate, finish`) + `Sink` seam + `FakeSink` — **unchanged from task
  038**, because the supervisor should depend on a typed in-process contract, not
  on the block's wire shape directly; and
- a production `BlockSink` adapter that maps each `AuditEvent` to one
  `audit-trail emit` CLI call, and surfaces the block's `audit-trail verify` as the
  integrity gate.

The mapping from agent-builder's taxonomy to the block's frozen `emit` schema:

| `AuditEvent` field | block `emit` field |
|---|---|
| action (enum) | `action` (verb string) |
| — (constant `"agent-builder"` / run identity) | `actor` |
| task id / branch / remote / launcher | `target` |
| verdict (verify) / outcome (finish) | `decision` |
| run id, task id, and other typed sub-fields | `context` (integer/string values only) |
| event time (injectable clock) | `ts` |

The three product calls from ADR 025 carry forward unchanged, now realized through
the block:

1. **Storage relationship — beside, not replace.** The block's chain sits
   *alongside* the unchanged 019 RunRecord raw stream. Raw stdout/stderr stay in
   the RunRecord; only the typed action layer is emitted to the block. Two durable
   artifacts per run remains the accepted v0 trade.
2. **Egress-attempt capture — deferred, spike-gated.** Unchanged: v0 emits only the
   action events the run loop already produces. Egress-attempt events become a task
   only if a spike confirms the egress proxy exposes attempts host-side.
3. **Chain-integrity severity — block.** `audit-trail verify` over a produced run's
   chain is a `block`-severity gate (consistent with F-002), invoked through the
   block, not a reimplemented verifier.

Configuration:
- `AGENT_BUILDER_AUDIT_RECORD=<path>` — the chain logfile passed to the block;
  blank/absent disables auditing (mirrors `AGENT_BUILDER_RUN_RECORD`).
- `AGENT_BUILDER_AUDIT_BIN=<path>` — the `audit-trail` binary; falls back to `$PATH`.
  When `AGENT_BUILDER_AUDIT_RECORD` is set but the binary is not resolvable, the run
  fails with a config error **before dispatch** — auditing is never silently skipped.

## Consequences

**Positive**
- No duplicated integrity code. The hash chain, RFC 8785 canonicalization, genesis,
  tamper-detection verifier, and the already-shipped signing / Rekor anchoring /
  rotation all stay owned by the block. agent-builder inherits those upgrades for
  free the day it points at a newer block binary.
- agent-builder takes its intended role as the *first concrete consumer* of a block,
  validating the block's CLI/IPC seam against a real caller — exactly the
  build-early/self-leveraging order the roadmap wants.
- The `audit.Sink` in-process seam is preserved, so the supervisor still depends on a
  typed, fakeable interface (tests use `FakeSink`, no subprocess in unit tests). The
  CLI→IPC transport choice is an adapter-internal detail.
- The F-003 supervisor-isolation boundary is *easier* to hold than under ADR 025: the
  adapter reaches the block over a process boundary (`os/exec`), so no block package
  enters the supervisor's Go import graph.

**Negative / what gets harder**
- A run-time dependency on the `audit-trail` binary being installed/resolvable when
  auditing is enabled. Mitigated by fail-fast config validation before dispatch and
  by auditing being opt-in via `AGENT_BUILDER_AUDIT_RECORD`.
- A process boundary per action event (CLI). Bounded (~7 events/run, action layer
  only); the IPC socket is the pre-shaped upgrade if frequency ever grows.
- A cross-repo version contract: agent-builder depends on the block's *frozen* v1
  `emit`/`verify` CLI surface. This is the block's stated stable contract, so the
  coupling is to a deliberately stable interface, not to its internals.

**Explicitly deferred (NOT in v0)**
- IPC-socket transport / a managed `audit-trail serve` sidecar (Option B) — upgrade
  path, not v0.
- Go-module import of the block (Option C) — rejected for coupling reasons.
- Routing egress *attempts* into the block — spike-gated (carried over from ADR 025).
- Surfacing the block's signed-checkpoint / Rekor-anchor verbs through agent-builder —
  v0 uses only `emit` + `verify`; the richer verbs are a later integration.

## Relationship to ADR 025

ADR 025's *problem framing* stands: agent-builder needs a tamper-evident record of
its action layer that survives box teardown, and the RunRecord alone is not that.
ADR 025's *solution* — build the chain in-repo — is superseded here: the tamper-
evident record is produced by the shipped block, which agent-builder consumes. The
typed `AuditEvent` taxonomy + `Sink` seam that ADR 025 introduced (task 038) is the
one piece that survives intact, because it is the in-process contract, not the chain
implementation.
