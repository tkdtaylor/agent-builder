# ADR 037: audit-trail signed-checkpoint integration

**Date:** 2026-06-19
**Status:** Proposed
**Extends:** ADR 026 (audit-trail v0 — consume the shipped block via its CLI/IPC seam)

## Context

ADR 026 established the integration pattern for consuming the `audit-trail` block in
agent-builder: **Option A, CLI subprocess**. The block binary is invoked via
`audit-trail emit` and `audit-trail verify`; the `internal/audit` package stays a
stdlib-only leaf with a typed `AuditEvent` taxonomy, `Sink` seam, and `FakeSink`. No
Go-module import of the block; no `go.mod replace` binding the two repos.

ADR 026 explicitly deferred one capability:

> "Surfacing the block's signed-checkpoint / Rekor-anchor verbs through agent-builder —
> v0 uses only `emit` + `verify`; the richer verbs are a later integration."

The `audit-trail` block now ships those richer verbs as a stable, tested surface:

```
audit-trail checkpoint create --logfile <path> --log-id <id> --signing-key <key.pem> [--out <path.json>]
audit-trail checkpoint verify --checkpoint <path.json> --public-key <pub.pem> [--logfile <path>]
```

`checkpoint create` produces a `SignedCheckpoint` JSON over the verified chain head —
a portable attestation that a third party can verify OFFLINE with only the public key.
`checkpoint verify` validates the signature and (optionally) cross-checks against the
live log. Exit 0 = valid; exit 1 = invalid. The block also ships Rekor anchoring verbs
(`checkpoint anchor` / `verify-anchor`), which are explicitly out of scope for this
ADR — a future follow-up only (see "Out of scope" below).

This ADR lifts the deferral for signed checkpoints (not Rekor) and defines the
integration pattern, configuration surface, and behavior contract. Tasks 068 and 069
implement the design; this ADR is the prerequisite.

### Why signed checkpoints add value now

After a run, agent-builder can already verify that its audit log has not been tampered
with (via `audit-trail verify`). What it cannot currently produce is a **portable
proof** — something a third party can check offline without access to the live log.
A signed checkpoint is that proof: it binds the chain head hash, the log-id, and a
sequence number into a JSON blob signed by an Ed25519 key held on the trusted
supervisor host. Given only the public key and the checkpoint file, any party can
confirm the chain state was good at the point the checkpoint was sealed.

This is a forensic upgrade, not a behavioral change. Runs that do not configure a
signing key are unaffected.

### Sequencing

Tasks 067–069 are sequenced after vault tasks 064–066 per the 2026-06-19 roadmap
decision. Vault closes the token-in-box risk (the most concrete active risk at that
date). Signed-checkpoint integration is self-contained — it does not depend on vault —
but it does not jump the vault queue.

## Decision

### Integration approach: CLI subprocess, consistent with ADR 026 Option A

`checkpoint create` and `checkpoint verify` are invoked as CLI subprocesses, matching
the same arm's-length coupling that ADR 026 selected for `emit` and `verify`. The
`internal/audit` package remains a stdlib-only leaf. No Go-module import of the
`audit-trail` package is introduced. This preserves the F-005 fitness invariant
(no cross-package imports into the leaf) and the F-003 supervisor-isolation boundary.

The block binary location is already configurable via `AGENT_BUILDER_AUDIT_BIN`.
Signed-checkpoint invocations use the same binary.

### Single checkpoint trigger: supervisor seal, after VerifyChain passes

One signed checkpoint is created per run, at supervisor **seal** time — the point
after the supervisor has invoked `audit-trail verify` (VerifyChain) and confirmed the
chain is valid (`IsTampered() == false`). This ordering is load-bearing:

- The checkpoint attests the **verified chain head**, not a potentially tampered one.
- Creating the checkpoint before verify would allow a tampered chain to receive a
  valid signature, defeating the forensic guarantee.
- One checkpoint per run bounds the key-use frequency and keeps the attestation model
  simple: each run produces at most one artifact whose existence certifies the chain
  was intact at seal.

If VerifyChain reports tampering, the supervisor escalates (existing behavior) and does
**not** create a checkpoint. No checkpoint for a corrupted chain.

### Key management: Ed25519 signing key by file path on the supervisor host

The Ed25519 private key is supplied to `checkpoint create` as a PEM file path via
the `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` environment variable. The key lives on the
trusted supervisor host — outside the execution box. The key is never mounted into,
visible to, or injectable from inside the container; the compromised-agent threat does
not see it because `checkpoint create` runs supervisor-side at seal, after the box has
exited.

Key management by file path is the correct v0 choice because:
1. The supervisor host is already the trust boundary for all other sensitive material
   (OAuth tokens, run configuration).
2. The signing operation happens outside the box — the key is never at risk from
   in-box code.
3. File-path custody is simple, auditable, and matches the existing pattern for
   `AGENT_BUILDER_AUDIT_BIN` and `AGENT_BUILDER_AUDIT_RECORD`.

**Forward-link to vault:** Brokering the signing key through the vault block (ADR 036,
tasks 064–066) is a natural follow-on: the vault would supply the PEM material at seal
time, replacing the raw file-path reference. This is explicitly **not a prerequisite**
for tasks 068 and 069 — the feature is independent and ships without vault involvement.

### Configuration surface: four new env vars in the AGENT_BUILDER_AUDIT_CHECKPOINT_* family

The following four environment variable names are canonical for this feature and for
tasks 068 and 069:

| Env var | Purpose | Required? |
|---------|---------|-----------|
| `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` | File path to the Ed25519 PEM private key used by `checkpoint create`. Setting this var enables checkpoint creation; absent means opt-in off. | optional |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID` | Log identifier string passed as `--log-id` to `checkpoint create`. | required when `CHECKPOINT_KEY` is set |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` | File path where the `SignedCheckpoint` JSON is written (`--out`). Defaults to a run-scoped path when not set but `CHECKPOINT_KEY` is set. | optional |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY` | File path to the Ed25519 PEM public key used by `checkpoint verify`. Required to enable checkpoint verification in task 069's verify surface. | optional (required for verify) |

These names are added to `docs/spec/configuration.md` in task 068 (alongside the
implementation code that reads them). The `verify` surface update to `docs/spec/interfaces.md`
lands in task 069.

### Opt-in behavior: absent signing key means no checkpoint, run unchanged

If `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is not set, no checkpoint is created and the
run proceeds exactly as before this feature was introduced. Signed checkpoints are
opt-in; existing deployments that do not set the var are unaffected.

This mirrors the existing `AGENT_BUILDER_AUDIT_RECORD` opt-in pattern: absent the
record var, auditing is disabled and the run proceeds. The checkpoint feature is an
additional opt-in layer on top of auditing.

### Fail-fast behavior: configured but unresolvable key or binary

If `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is set (the feature is configured) but either:
- the signing key file does not exist or is not readable, or
- the `audit-trail` binary is not resolvable (via `AGENT_BUILDER_AUDIT_BIN` or `$PATH`),

then agent-builder **fails before dispatch** with a config error. It never silently
skips the checkpoint when the user has explicitly configured it. This is the same
fail-before-dispatch behavior that ADR 026 mandated for the `resolveAuditBin` check:
when `AGENT_BUILDER_AUDIT_RECORD` is set but the binary cannot be found, the run fails
with a config error before dispatch — auditing is never silently skipped. The
checkpoint extension directly mirrors `resolveAuditBin` behavior.

This is not fail-fast for the absence case (absent key = opt-in off, not an error).
Fail-fast applies only when the feature is configured but the configured resource is
unresolvable.

## Out of scope

- **Rekor anchoring** (`checkpoint anchor` / `verify-anchor`) — explicitly deferred to a
  future follow-up ADR. The block ships these verbs, but they require external network
  access to the Rekor transparency log and introduce operational complexity (key
  anchoring, witness verification) that is not a v1 requirement. Named out of scope here
  so tasks 068 and 069 do not reach for them.
- **Vault key brokering** — a named future follow-on once the vault integration matures.
  The file-path approach in `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` is the v1 contract;
  vault replaces the file-path reference in a future task, not a prerequisite.
- **IPC-socket transport** for checkpoint calls — consistent with ADR 026's deferral of
  Option B (socket) for `emit`/`verify`; checkpoint calls are also CLI for v1.
- **Key rotation / log segmentation** — the block ships segmented log support; v1 uses
  a single key per deployment and a single chain per run.
- **Writing any Go code in this task** — tasks 068 and 069 carry the implementation.

## Spec files updated by implementation tasks

- `docs/spec/configuration.md` — task 068 adds the four `AGENT_BUILDER_AUDIT_CHECKPOINT_*`
  env var entries in the same commit as the Go code that reads them.
- `docs/spec/interfaces.md` — task 069 adds the new `checkpoint verify` CLI verb and the
  verify surface contract in the same commit as the Go code that exposes it.

## Consequences

**Positive**
- Agent-builder gains a portable, offline-verifiable forensic artifact per run without
  writing any new cryptographic code — the block owns the Ed25519 implementation.
- The integration pattern (CLI subprocess at seal, one checkpoint per run, fail-fast
  on misconfiguration) is consistent with the ADR 026 design: no new coupling
  patterns, no new architectural risks.
- Existing runs are unaffected (opt-in). Zero behavior change for unconfigured
  deployments.
- The `internal/audit` leaf discipline (F-005) is preserved: no block-package import,
  subprocess seam only.
- Vault can be wired in as the key source in a future task without changing the
  consumer code — the checkpoint call takes a file path, and the vault adapter would
  write the PEM to a temp file before seal.

**Negative / what gets harder**
- Two additional env vars to document and validate at startup when checkpoint creation
  is enabled (`CHECKPOINT_KEY` and `CHECKPOINT_LOG_ID`). Mitigated by the fail-fast
  pre-dispatch check making misconfiguration loud.
- A run-time dependency on the signing key file being present and readable when the
  feature is configured. Same mitigation: fail-fast before dispatch, clear error
  message naming the unresolvable resource.
- The checkpoint output file path (`CHECKPOINT_OUT`) must be durable across the run;
  the supervisor is responsible for not overwriting or cleaning it up before the
  operator can retrieve it.

**Unchanged from ADR 026**
- The `audit-trail` binary run-time dependency (already accepted in ADR 026).
- The per-action-event subprocess call pattern for `emit` (unchanged).
- The `AGENT_BUILDER_AUDIT_RECORD` opt-in model (unchanged; checkpoint adds a second
  opt-in layer on top).
- The `internal/audit` package shape: typed `AuditEvent` taxonomy, `Sink` seam,
  `FakeSink` for tests, `BlockSink` production adapter (checkpoint calls are added to
  `BlockSink` in task 068).
