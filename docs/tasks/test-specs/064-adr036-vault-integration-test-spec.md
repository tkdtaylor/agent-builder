# Test spec — Task 064: ADR-036 vault integration decision

**Linked task:** `docs/tasks/backlog/064-adr036-vault-integration.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task produces one artifact: `docs/architecture/decisions/036-vault-integration.md`.
The "test cases" are documentation consistency and content assertions — the same style
used by tasks 052 (ADR 031 doc-honesty) and 031 (verification ledger cleanup). Every
requirement is verifiable by `grep`/`cat` on the committed file and on the spec files
it must update or note as pending-update.

The ADR must:
1. Name the decision (adopt vault as the token broker for executor/git/GitHub tokens).
2. Record what proxy mode wires today (exec-sandbox v0 egress proxy injects
   `<scheme> <cred>` on the `Authorization` header for allowlisted hosts).
3. Identify the proxy-mode feasibility risk for the Claude provider token and the
   git/GitHub tokens (different host targets, different header names).
4. Name the chosen starting scope: git/GitHub tokens first (cleaner proxy target);
   provider token (Claude) as a follow-on once git/GitHub proxy path is proven.
5. Reference ADR 035 (deferred vault fields) and note that this decision pulls vault
   forward ahead of the roadmap's audit-trail/policy-engine sequencing.
6. Record the env-mode stub status in exec-sandbox v0 (env mode is a STUB — recorded
   but NOT loaded into the sandbox env) so future implementors know the constraint.
7. Record the injection_mode="proxy" choice and explain why env mode is explicitly
   ruled out for v0.
8. Call out the no-unattended-self-modification invariant: agent-builder's own source
   is not edited by the tasks that follow; only wiring code is added.

## Requirements coverage

| Req ID     | Test cases                   | Covered? |
|------------|------------------------------|----------|
| REQ-064-01 | TC-064-01                    | yes      |
| REQ-064-02 | TC-064-02                    | yes      |
| REQ-064-03 | TC-064-03                    | yes      |
| REQ-064-04 | TC-064-04                    | yes      |
| REQ-064-05 | TC-064-05                    | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-064-01 — ADR-036 file exists with required structural sections

- **Requirement:** REQ-064-01
- **Level:** L5 (doc-content assertion; `grep` on committed file)
- **Test file:** `tests/e2e/phase0_e2e_test.go` or a standalone `scripts/tests/adr036-test.sh`

**Assertions (all must pass):**
- `docs/architecture/decisions/036-vault-integration.md` exists and is non-empty.
- The file contains a `## Context` section.
- The file contains a `## Decision` section.
- The file contains a `## Consequences` section.
- The file contains `Status: Accepted` (or `Proposed`; either is valid at task merge
  time — the human reviewer sets `Accepted` when they approve the task).
- The file references `ADR 035` (the deferred-vault-fields predecessor).

---

### TC-064-02 — ADR-036 names proxy mode and explains env-mode stub exclusion

- **Requirement:** REQ-064-02
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains the string `injection_mode` (the field in the RunRequest wiring).
- The file contains the string `proxy` at least once in the Decision section.
- The file contains a statement explaining that env mode is a stub in exec-sandbox v0
  (i.e. `env mode` is mentioned alongside a word like `stub`, `not loaded`, or
  `not implemented`).
- The file does NOT state that env mode is available or wired in exec-sandbox v0
  (guard: no phrase like `env mode is wired` or `env mode works`).

---

### TC-064-03 — ADR-036 records the feasibility risk for the Claude provider token

- **Requirement:** REQ-064-03
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains either `api.anthropic.com` or `provider token` (naming the
  provider host that requires auth injection).
- The file contains a phrase indicating this is a risk or unproven path (words like
  `risk`, `unproven`, `feasibility`, `follow-on`, or `deferred`).
- The file identifies that git/GitHub tokens are the chosen starting scope (contains
  `api.github.com` or `git token` or `GitHub token` alongside a phrase like `first`,
  `starting`, or `initial scope`).

---

### TC-064-04 — ADR-036 names the vault socket protocol and the Binding shape

- **Requirement:** REQ-064-04
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains the string `put` (the vault admin verb for storing a secret).
- The file contains the string `resolve` (the verb that returns an opaque handle).
- The file contains the string `inject` (the verb that delivers plaintext at the
  injection edge).
- The file contains the string `handle` (the opaque reference that travels between
  agent-builder and exec-sandbox — not the secret value).
- The file contains the string `vault_socket` (the RunRequest wiring field).
- The file contains the string `secret_refs` (the RunRequest field for handles).
- The file contains the word `Binding` or `binding` (the host/header/scheme struct
  that controls which header the proxy injects on).

---

### TC-064-05 — ADR-036 is referenced by SPEC.md or leaves a note in configuration.md

- **Requirement:** REQ-064-05
- **Level:** L5 (doc-consistency assertion)

**Assertions:**
- Either `docs/spec/SPEC.md` or `docs/spec/configuration.md` references ADR 036
  (contains `ADR 036` or `036-vault-integration`), **or** the ADR itself contains a
  note naming the spec files that will be updated in the implementation tasks (064
  is a doc-only task; spec updates land in 065/066).
- `make check` exits 0 with no linter/test regressions introduced by the ADR file.

---

## Verification plan

- **Highest level achievable:** L5 — the ADR is a doc-only artifact; verification is
  `grep`-based content assertions + `make check` confirming no regressions.
- **Harness command:**
  ```
  grep -q "Status:" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "## Decision" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "ADR 035" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "proxy" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "injection_mode" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "secret_refs" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "vault_socket" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "resolve" docs/architecture/decisions/036-vault-integration.md && \
  grep -q "inject" docs/architecture/decisions/036-vault-integration.md && \
  make check
  ```
  Expected: all `grep` calls exit 0; `make check` final line `All checks passed.`

## Out of scope

- Writing the `SecretSource` interface (task 065).
- Writing any vault client code (task 066).
- Updating `docs/spec/` configuration.md or interfaces.md with the new secret names
  (those land in 065 and 066 respectively, in the same commit as the code).
- Changing existing behavior — this task commits only the ADR file.
