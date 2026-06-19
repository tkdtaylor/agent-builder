# Test spec — Task 067: ADR for audit-trail signed-checkpoint integration

**Linked task:** `docs/tasks/backlog/067-adr-signed-checkpoint.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task produces one artifact: `docs/architecture/decisions/037-signed-checkpoint-integration.md`.
The "test cases" are documentation consistency and content assertions — the same style used
by tasks 052 (ADR 031 doc-honesty), 064 (ADR 036 vault integration), and 031 (verification
ledger cleanup). Every requirement is verifiable by `grep`/`cat` on the committed file.

The ADR must:
1. Decide that signed checkpoints are consumed via CLI subprocess (`audit-trail checkpoint
   create/verify`), consistent with ADR 026 Option A — NOT via a go-module import of the
   audit-trail package. The arm's-length coupling (subprocess, not go-module) is the
   invariant that keeps the `internal/audit` package a stdlib-only leaf.
2. Name the single checkpoint trigger: one signed checkpoint per run, created at supervisor
   **seal** time (after `VerifyChain` passes), so the checkpoint attests the final,
   verified chain state.
3. Document key management: the Ed25519 signing key is supplied by file path via a new
   `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env var family. Explicitly forward-links brokering
   the key through vault as a future follow-on (NOT a prerequisite for this feature).
4. Name the four new config surface env vars (signing-key path, log-id, checkpoint output
   path, public-key path for verification) in the `AGENT_BUILDER_AUDIT_CHECKPOINT_*` family.
5. Specify behavior when checkpoint config is absent: opt-in (no signing-key configured
   means no checkpoint, run unchanged). When configured but the key or binary is
   unresolvable: fail fast before dispatch, never silently skip.
6. Name which `docs/spec/` files the implementation tasks will update (configuration.md for
   the new env vars in task 068; interfaces.md for the new CLI verb and verify surface in
   task 069).
7. Reference ADR 026 as the predecessor that established the subprocess/CLI coupling
   pattern being extended.
8. Name Rekor anchoring (`checkpoint anchor` / `verify-anchor`) as explicitly OUT OF SCOPE
   for this feature — a future follow-up only.

## Requirements coverage

| Req ID     | Test cases      | Covered? |
|------------|-----------------|----------|
| REQ-067-01 | TC-067-01       | yes      |
| REQ-067-02 | TC-067-02       | yes      |
| REQ-067-03 | TC-067-03       | yes      |
| REQ-067-04 | TC-067-04       | yes      |
| REQ-067-05 | TC-067-05       | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-067-01 — ADR-037 file exists with required structural sections

- **Requirement:** REQ-067-01
- **Level:** L5 (doc-content assertion; `grep` on committed file)

**Assertions (all must pass):**
- `docs/architecture/decisions/037-signed-checkpoint-integration.md` exists and is non-empty.
- The file contains a `## Context` section.
- The file contains a `## Decision` section.
- The file contains a `## Consequences` section.
- The file contains `Status: Accepted` or `Status: Proposed` (either is valid at task merge
  time — the human reviewer sets `Accepted` when they approve the scope).
- The file references `ADR 026` (the predecessor establishing CLI-subprocess coupling).

---

### TC-067-02 — ADR-037 specifies CLI-subprocess coupling and the single checkpoint trigger

- **Requirement:** REQ-067-02
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains `checkpoint create` (the CLI verb that produces a signed checkpoint).
- The file contains `checkpoint verify` (the CLI verb used on the verify surface).
- The file does NOT state that agent-builder imports the audit-trail Go module directly
  (guard: the file must NOT contain `go-module import` or `import "github.com/` alongside
  `audit-trail` in a way that describes it as the chosen integration path).
- The file contains the word `seal` or `Seal` (naming the supervisor seal event as the
  checkpoint trigger).
- The file states that `VerifyChain` (or equivalent) passes before the checkpoint is created
  (i.e., the checkpoint attests a verified chain, not a potentially tampered one). A phrase
  like `after verify` or `verify passes` or `verified chain` must appear.
- The file contains `one` or `single` alongside `checkpoint` and `run` (documenting the
  one-per-run policy).

---

### TC-067-03 — ADR-037 documents key management and vault forward-link

- **Requirement:** REQ-067-03
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains `Ed25519` (naming the signing algorithm).
- The file contains `file path` or `file-path` or `PEM` (describing how the signing key
  is supplied).
- The file contains a reference to `vault` as a forward-link or future follow-on for key
  brokering (words like `vault`, `future`, `follow-on`, or `forward` near each other).
- The file does NOT state that vault is a prerequisite for this feature (guard: the file
  must NOT contain language like "requires vault" or "blocked on vault" in the context
  of these tasks — the feature must be described as independent).
- The file names Rekor anchoring as out of scope (contains `Rekor` or `rekor` alongside
  a word like `out of scope`, `deferred`, or `future`).

---

### TC-067-04 — ADR-037 names the four new AGENT_BUILDER_AUDIT_CHECKPOINT_* env vars

- **Requirement:** REQ-067-04
- **Level:** L5 (doc-content assertion)

**Assertions (all four env var names must appear in the file):**
- The file contains `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` (signing-key file path) or an
  equivalent `AGENT_BUILDER_AUDIT_*` name for the private key path.
- The file contains `AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID` or an equivalent name for
  the log identifier.
- The file contains `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` or an equivalent name for the
  checkpoint JSON output path.
- The file contains `AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY` or an equivalent name
  for the public key path (used by the verify surface in task 069).

Note: the exact env var names are decided in this ADR. The test assertions must be
updated to match the names the ADR actually proposes — the executor writes the ADR first,
then these names are canonical for tasks 068 and 069.

---

### TC-067-05 — ADR-037 specifies opt-in behavior and fail-fast for configured-but-missing keys

- **Requirement:** REQ-067-05
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file states that when checkpoint config is absent (no signing-key configured), no
  checkpoint is created and the run proceeds unchanged (contains language like `opt-in`,
  `no signing-key`, `disabled`, or `absent` alongside `no checkpoint` or `unchanged`).
- The file states that when the signing key is configured but the key file or binary is
  unresolvable, agent-builder fails fast before dispatch (contains `fail fast` or `fail-fast`
  or `fail before dispatch` or `pre-dispatch` alongside `configured`).
- The file states the pattern explicitly mirrors `AGENT_BUILDER_AUDIT_RECORD` / `resolveAuditBin`
  behavior (contains `AGENT_BUILDER_AUDIT_RECORD` or `resolveAuditBin` or `mirrors` alongside
  the fail-fast description).
- The file names `docs/spec/configuration.md` and `docs/spec/interfaces.md` as the spec
  files that will be updated by the implementation tasks (068 and 069 respectively).
- `make check` exits 0 with no linter or test regressions introduced by the ADR file.

---

## Verification plan

- **Highest level achievable:** L5 — the ADR is a doc-only artifact; verification is
  `grep`-based content assertions + `make check` confirming no regressions.
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
  Expected: all `grep` calls exit 0; `make check` final line `All checks passed.`

## Out of scope

- Writing any Go code (`CheckpointSigner`, config wiring, CLI verb).
- Updating `docs/spec/` configuration.md or interfaces.md with the new env var names
  (those land in tasks 068 and 069 respectively, in the same commit as the code).
- Rekor anchoring (`checkpoint anchor` / `verify-anchor`).
- Vault key brokering (a named future follow-on, NOT a prerequisite).
- Changing existing behavior — this task commits only the ADR file.
