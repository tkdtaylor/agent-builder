# Task 067: ADR-037 — audit-trail signed-checkpoint integration decision

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** ✅ (verified)

## Goal

Write `docs/architecture/decisions/037-signed-checkpoint-integration.md`. This is the
architectural decision record for surfacing the audit-trail block's Ed25519 signed-checkpoint
verbs through agent-builder — the forensic upgrade ADR 026 explicitly deferred ("v0 uses
only emit + verify; the richer verbs are a later integration"). The ADR is a
human-reviewable planning artifact; the implementation tasks (068, 069) depend on it being
accepted before they begin.

No production code is written or modified in this task.

## Context

### What ADR 026 established (foundation for this decision)

ADR 026 (`docs/architecture/decisions/026-audit-trail-consume-shipped-block.md`) chose
Option A: consume the audit-trail block via CLI subprocess (`audit-trail emit/verify`) using
an injectable `ExecRunner` seam, keeping `internal/audit` a stdlib-only leaf package (no
go-module import of the block). That coupling model is the load-bearing constraint:
`internal/audit` must remain a leaf (enforced by F-005 fitness check in task 042).

### What the audit-trail block now ships

The block (`~/Code/Public/audit-trail`) now ships Ed25519 signed checkpoints:

```
audit-trail checkpoint create --logfile <path> --log-id <id> --signing-key <key.pem> [--out <path.json>]
audit-trail checkpoint verify --checkpoint <path.json> --public-key <pub.pem> [--logfile <path>]
```

`checkpoint create` produces a `SignedCheckpoint` JSON over the verified chain head —
a portable attestation that a third party can verify OFFLINE with only the public key.
`checkpoint verify` validates the signature and (optionally) cross-checks against the live
log. Exit 0 = valid; exit 1 = invalid.

The block also ships Rekor anchoring verbs (`checkpoint anchor` / `verify-anchor`), but
those are explicitly out of scope for this feature.

### The ADR 026 explicit deferral

ADR 026 named this deferral in the "Decisions deferred" section:

> "Surfacing the block's signed-checkpoint / Rekor-anchor verbs through agent-builder —
> v0 uses only emit + verify; the richer verbs are a later integration."

This ADR (037) lifts the deferral for signed checkpoints (not Rekor) and defines the
integration pattern, configuration surface, and behavior contract that tasks 068 and 069
implement.

### Sequencing relative to vault tasks

These three tasks (067–069) are sequenced **after** vault tasks 064–066 per the 2026-06-19
roadmap decision: vault closes the token-in-box risk (the most concrete active risk) and is
already in flight, whereas signed-checkpoint integration is a deepening of already-working
audit machinery. It is self-contained — it does not depend on vault and could run in parallel —
but it does not jump the vault queue.

## Requirements

| Req ID     | Description                                                                                                                            | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-067-01 | ADR file `docs/architecture/decisions/037-signed-checkpoint-integration.md` exists with Status, Context, Decision, and Consequences sections, referencing ADR 026 as the predecessor. | must have |
| REQ-067-02 | Decision names CLI-subprocess coupling (`audit-trail checkpoint create/verify`) as the integration approach, consistent with ADR 026 Option A. Names the supervisor seal as the single checkpoint trigger (one checkpoint per run, after `VerifyChain` passes). | must have |
| REQ-067-03 | Decision documents key management: signing key supplied by file path via `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env var. Forward-links vault as a future key-brokering option (not a prerequisite). Names Rekor anchoring as explicitly out of scope. | must have |
| REQ-067-04 | Decision proposes the four new `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env var names: signing-key path, log-id, checkpoint output path, and public-key path for verification. These become the canonical names for tasks 068 and 069. | must have |
| REQ-067-05 | Decision specifies opt-in behavior (absent signing-key → no checkpoint, run unchanged) and fail-fast behavior (configured but unresolvable key or binary → fail before dispatch, never silently skip). Names `docs/spec/configuration.md` (task 068) and `docs/spec/interfaces.md` (task 069) as the spec files that implementation tasks will update. `make check` exits 0. | must have |

## Readiness gate

- [ ] Test spec `067-adr-signed-checkpoint-test-spec.md` exists (written first — already done)
- [x] Human has reviewed and approved the scope (this ADR is "Ask-first" per CLAUDE.md) — approved 2026-06-19. Decisions follow ADR 026's enforced CLI-subprocess/leaf pattern; the one substantive call (signing-key custody) is **approved as: Ed25519 key by file path on the trusted supervisor host for v0, vault-based key storage deferred.** This is safe because `checkpoint create` runs supervisor-side at seal (outside the box), so the compromised-agent threat never sees the key. The executor may author the ADR autonomously on these terms.
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-067-01] TC-067-01: ADR file exists, has Status/Context/Decision/Consequences, references ADR 026
- [ ] [REQ-067-02] TC-067-02: `checkpoint create`/`checkpoint verify` CLI verbs named; `seal` trigger named; one-per-run policy stated; `VerifyChain`-passes-first constraint stated
- [ ] [REQ-067-03] TC-067-03: Ed25519 key-by-file-path documented; vault forward-link present; Rekor explicitly out of scope
- [ ] [REQ-067-04] TC-067-04: All four `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env var names present
- [ ] [REQ-067-05] TC-067-05: Opt-in behavior and fail-fast behavior specified; `configuration.md` and `interfaces.md` named as spec-update targets; `make check` exits 0

## Verification plan

- **Highest level achievable:** L5 — doc-content `grep` assertions + `make check`
  (no runtime surface; ADR is a markdown file).
- **Harness command:**
  ```
  grep -q "Status:" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "## Decision" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "## Consequences" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "ADR 026" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "checkpoint create" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "checkpoint verify" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "seal" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "Ed25519" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "vault" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -qi "out of scope\|deferred\|future" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "AGENT_BUILDER_AUDIT_CHECKPOINT" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "fail.fast\|fail fast\|fail before dispatch" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "configuration.md" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  grep -q "interfaces.md" docs/architecture/decisions/037-signed-checkpoint-integration.md && \
  make check
  ```
  Expected: all exit 0; `make check` → `All checks passed.`
- **Runtime observation:** N/A — no runtime surface.

## Out of scope

- Writing any Go code.
- Updating `docs/spec/` files (those land in 068/069 with their code changes).
- Writing `CheckpointSigner`, `CheckpointVerifier`, config wiring, or CLI subcommands.
- Rekor anchoring (`checkpoint anchor` / `verify-anchor`) — explicitly deferred to a future
  follow-up and named as such in the ADR.
- Vault key brokering (named as a future follow-on, NOT a prerequisite for 068/069).
- Changing existing behavior — this task commits only the ADR file.

## Dependencies

- ADR 026 (audit-trail CLI coupling) — already written and accepted (task 038).
- Human approval of the scope before the task begins (CLAUDE.md "Ask first" for ADR
  authoring).
- Sequenced **after** vault tasks 064–066 per the 2026-06-19 roadmap decision.
  Tasks 068 and 069 depend on this ADR being merged and accepted.
