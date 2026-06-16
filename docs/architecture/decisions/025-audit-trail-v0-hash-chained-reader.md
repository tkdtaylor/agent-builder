# ADR 025: audit-trail v0 — hash-chained reader over the RunRecord stream

**Date:** 2026-06-16
**Status:** Accepted

## Context

Phase 2 opens the `audit-trail` block (roadmap order: audit-trail → policy-engine
→ vault). Per `autonomous-builder.md` §2, audit-trail removes the human checkpoint
*"git history is enough"* — it earns its place "when actions aren't captured in git
(deploys, API calls)." §3 names the seam it plugs into: "stream stdout/stderr +
command log out (audit-trail seam)."

That seam already exists. Task 019 built the **RunRecord**: a host-side, append-only
NDJSON stream of run lifecycle events (`run_started`, `command`, `stdout`, `stderr`,
`run_finished`) written by `internal/supervisor` during one `Supervisor.Run()`
dispatch, flushed and closed before containment teardown so it survives the
ephemeral box (`docs/spec/data-model.md` §Wire formats — RunRecord NDJSON). The
default run wiring (`internal/runtime/run.go`) already emits the security-relevant
actions as `command` events: `containment=podman launcher=…`, `pick task`,
`attempt`, `verify`, `publish branch … remote=…`, `finish … outcome=…`, plus
`escalated` evidence on the failure path.

So the raw material is captured. What is **not** yet present is what makes a stream
an *audit trail* rather than a log file:

1. **Tamper-evidence.** A plain NDJSON file can be edited or truncated after the fact
   with no detectable trace. "git history is enough" is only safely retired if the
   replacement is *harder* to silently rewrite than git, not easier.
2. **A stable audit event taxonomy.** The RunRecord mixes raw stdout/stderr bytes
   (high-volume, unstructured) with semantically meaningful action events
   (`command` lines that happen to encode "published a branch to remote X"). An
   auditor cares about the latter; today they are free-text strings, not typed,
   queryable events.
3. **A read/query surface.** There is no consumer-side contract for asking "what
   actions did run N take?" — only the file and human eyeballs.

The product question is how much of this v0 builds. The CLAUDE.md Unix-philosophy
rules ("defer premature decisions / smallest thing that works") and the
"distinguish v0 from the whole block" instruction push hard against building a
queryable store, signing infrastructure, or a daemon now. The seam pattern is
already set by `sandbox.Runner` (ADR 020): a typed, compile-checked, fakeable
interface in a small dedicated package, with the supervisor depending only on the
interface.

This ADR decides the **shape** of v0: whether audit-trail is a consumer of the
existing stream, a replacement writer, or a wrapping layer; and what the v0
integrity story is.

## Options considered

### Option A — Post-hoc reader/verifier over the existing RunRecord (consumer only)

A new `internal/audit` package reads a completed RunRecord NDJSON file, projects it
into a typed `AuditEvent` taxonomy (filtering raw stdout/stderr noise from
action-class events), and offers a verifier that re-reads the file and reports
structural integrity. The supervisor is **unchanged**; audit-trail is a pure
downstream consumer of the artifact 019 already produces.

**Pros**
- Smallest possible blast radius — zero change to the trusted supervisor or its
  isolation boundary (F-003 import graph untouched).
- Fully fakeable and testable in isolation against fixture NDJSON files; no
  containment or executor needed.
- The taxonomy/projection work is reusable later by policy-engine and any query UI.

**Cons**
- **No real tamper-evidence.** A reader over a plain file can detect *malformed*
  lines but cannot detect a clean post-hoc edit or a dropped line — the exact
  failure mode that distinguishes an audit trail from a log. It does not actually
  retire "git history is enough"; git at least chains commits.
- Two artifacts to reconcile (raw RunRecord + derived audit view) with no binding
  between them.

*Sketch:* `audit.Reader.Read(path) ([]AuditEvent, error)` plus
`audit.Verify(path) (Report, error)`. Pure functions over `os` / `io`. No writer.

### Option B — Hash-chained audit sink the supervisor writes through (the seam, writer side)

Define an `audit.Sink` interface (typed `Append(AuditEvent) error` + `Seal()`),
implemented by a `audit.ChainWriter` that writes append-only NDJSON where **each
record carries the SHA-256 of the previous record's canonical bytes** (a hash
chain / tamper-evident log). The supervisor writes lifecycle action events through
the `Sink` seam instead of (or alongside) the raw RunRecord stream. A companion
`audit.Verify` walks the chain and reports the first broken link. The taxonomy
(Option A's projection) is the event type the Sink accepts.

**Pros**
- **Genuine tamper-evidence at v0.** Any edit, reorder, or truncation in the middle
  of the file breaks the chain at a detectable point — this is the property that
  actually retires "git history is enough." Plain-text, no keys, no external
  service (per CLAUDE.md: plain-text interchange, defer infra).
- The seam is a typed, compile-checked, fakeable interface matching the
  `sandbox.Runner` pattern — `audit.Sink` is the block's stable contract that
  policy-engine and vault later consume.
- One artifact carries both the action record and its integrity proof; no
  reconciliation problem.

**Cons**
- Touches the supervisor write path. Must be done without widening the F-003 import
  boundary (the Sink interface lives in `internal/audit`, leaf package, no
  executor/LLM/web deps — same discipline as `sandbox`).
- Hash-chain is tamper-*evident*, not tamper-*proof*: an attacker who can rewrite the
  whole file can recompute the chain. v0 detects truncation/edit-in-place, not a
  full rewrite by a privileged attacker. (Signing/external anchoring is the deferred
  upgrade — see Consequences.)
- Slightly more than the absolute minimum: a chain is more than a log line.

*Sketch:* `internal/audit` owns `AuditEvent` (typed action taxonomy),
`Sink interface { Append(AuditEvent) error; Seal() error }`, `ChainWriter`
(NDJSON + `prev_hash`/`hash` fields), `FakeSink` (records events, no I/O), and
`Verify(path) (ChainReport, error)`. The supervisor's run-record wiring emits typed
`AuditEvent`s for the action-class lifecycle events (pick/attempt/verify/publish/
escalate/finish + egress-attempt when available) through a `Sink`; raw stdout/stderr
stay in the existing RunRecord stream, unchained, as high-volume diagnostic context.

### Option C — Replace the RunRecord with a single unified audit log

Retire the 019 RunRecord format and have the supervisor write one audit log that is
both the raw stream *and* the chained action record, superseding `data-model.md`'s
RunRecord NDJSON contract.

**Pros**
- One format, one writer, no "two artifacts" question at all.
- Conceptually clean — there is exactly one durable record of a run.

**Cons**
- **Chaining high-volume raw stdout/stderr is the wrong granularity** — it makes
  every byte of build output an integrity-bearing audit event, bloating the chain
  and conflating "diagnostic noise" with "agent took an action." Auditors want the
  action layer.
- Breaks an accepted, specced, shipped contract (RunRecord NDJSON, consumed by tests
  and the e2e harness) for no v0 benefit — violates "defer premature decisions" and
  the reversibility preference.
- Largest blast radius; most spec churn; hardest to reverse.

*Sketch:* rewrite `run_record.go` into `audit_log.go`, rewrite `data-model.md`
§Wire formats, migrate every RunRecord assertion in supervisor/e2e tests.

## Recommendation

**Option B.** The deciding factor is **honesty about the checkpoint being removed.**
The autonomous-builder design says audit-trail exists to retire "git history is
enough." A v0 that cannot detect a post-hoc edit (Option A) does not actually clear
that bar — it ships a derived view and calls it an audit trail. Option B's hash
chain is the minimum that genuinely makes the record *harder to silently rewrite
than git*, which is the whole point. It does so in plain text, with no keys and no
external service, so it stays inside the project's "smallest thing that works /
defer infra" rules.

Option B wins over C on **reversibility and blast radius**: it keeps the shipped
RunRecord raw-stream contract intact (raw stdout/stderr stay where they are) and
adds the chained *action* layer beside it, rather than rewriting a specced format.
The action layer is the right granularity for an auditor and for the policy-engine
that consumes it next. Option A is the right *internal structure* for B's event
projection — so B subsumes A's taxonomy work rather than discarding it.

The genuine product calls inside Option B (severity of the chain, whether egress
attempts are in v0 scope, whether the Sink replaces or sits beside the raw stream)
were surfaced for the human and are resolved in the Decision section below.

## Decision

**Option B, accepted.** audit-trail v0 is a small `internal/audit` package owning a
typed `AuditEvent` action taxonomy and an `audit.Sink` seam (typed, compile-checked,
fakeable — matching the `sandbox.Runner` pattern), with a hash-chained append-only
NDJSON `ChainWriter` implementation and a `Verify` reader that detects the first
broken link. The supervisor emits action-class lifecycle events through the `Sink`;
raw stdout/stderr remain in the existing 019 RunRecord stream.

The three product calls inside Option B are resolved:

1. **Storage relationship — beside, not replace.** The chained `audit.Sink` action
   log sits *alongside* the unchanged 019 RunRecord raw stream (Option C rejected).
   Two durable artifacts per run is the accepted v0 trade; the action layer is the
   right granularity to chain, raw stdout/stderr is not.
2. **Egress-attempt capture — deferred, spike-gated.** v0 ships only the action
   events the run loop already emits (containment, pick, attempt, verify+verdict,
   publish, escalate, finish). Egress-attempt audit events become a v0 task **only
   if** a short spike confirms the execution-box egress proxy already exposes
   attempts host-side; otherwise they are deferred to a later task. v0 scope does not
   block on a new containment data path.
3. **Chain-integrity severity — block.** Once `ChainWriter` ships, `audit.Verify`
   over a produced run's chain is a `block`-severity check (consistent with the
   project's "verification gate is blocking, no skip path" ethos, ADR 002 / F-002),
   not a warning.

## Consequences

**Positive**
- A run's *actions* (containment used, task picked, gate verdict, branch published,
  escalation) are recorded in a tamper-evident chain that survives box teardown —
  the checkpoint "git history is enough" can be retired for the action layer.
- `audit.Sink` becomes the stable block contract the policy-engine consumes next
  (it can read the action stream to enforce richer rules), keeping the
  build-early/self-leveraging order intact.
- The taxonomy is typed and plain-text, so tests, humans, and later blocks read it
  without a query engine.

**Negative / what gets harder**
- The supervisor gains one more leaf dependency (`internal/audit`) on its write
  path. The F-003 isolation discipline now covers `internal/audit` too — it must
  stay free of executor/LLM/web imports, and a fitness check should assert this.
- Tamper-evidence is not tamper-proofness. A privileged attacker who can rewrite the
  whole file can recompute the chain. **Deferred to a later task/ADR:** signing the
  chain head (or periodic external anchoring) to defeat full-file rewrite. v0
  detects edit-in-place and truncation, not a privileged full rewrite — this limit
  is stated in `data-model.md` so no one over-trusts v0.
- Two durable artifacts per run (raw RunRecord stream + chained audit action log)
  until/unless a later phase unifies them. The brief notes this as a deliberate v0
  trade, not drift.

**Explicitly deferred (NOT in v0)**
- A query/index surface beyond linear read + verify (no DB, no search).
- Cryptographic signing / external timestamp anchoring.
- Egress-attempt capture *if* the egress proxy does not yet emit attempts the
  supervisor can see (in scope only if the data is already available host-side).
- Unifying the raw RunRecord stream and the action chain into one format (Option C).
