# Test spec — Task 070: ADR-038 policy-engine integration decision

**Linked task:** `docs/tasks/backlog/070-adr038-policy-engine-integration.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task produces one artifact: `docs/architecture/decisions/038-policy-engine-integration.md`.
The "test cases" are documentation consistency and content assertions — the same style
used by task 064 (ADR-036 vault) and task 067 (ADR-037 signed-checkpoint). Every
requirement is verifiable by `grep`/`cat` on the committed file.

The ADR must capture:

1. The host-side per-run decide call: `decide` runs on the trusted host, before
   `box.Create`/`sandboxBox` construction, so a denied run never starts the box.
2. The full AuthZEN request shape agent-builder constructs:
   subject = agent-builder identity; action = `run-task`; resource = repo + egress
   hosts; context.risk = static value (env `AGENT_BUILDER_POLICY_RISK`, default `low`).
3. The obligation→seam map for all four obligations: `decision=deny`, `tier_select`,
   `vault_injection_floor`, `audit_emit` (`require_approval` is addressed by task 073).
4. Fail-closed semantics: unknown decision, malformed response, socket/dial error, or
   timeout all map to deny; the box never starts.
5. The `--allow` flag is fed from agent-builder's existing `Limits.EgressAllowlist`.
6. Static risk with dynamic scoring explicitly deferred.
7. Opt-in via `AGENT_BUILDER_POLICY_BIN` (unset = today's behavior, zero regression),
   mirroring `AGENT_BUILDER_VAULT_BIN` and `AGENT_BUILDER_AUDIT_BIN`.
8. The out-of-process invariant: agent cannot self-grant; in-process decide is
   explicitly ruled out.
9. References ADR 035 (tier seam) and ADR 036 (vault floor raise-only).
10. Names the spec files tasks 072/073 will update.

## Requirements coverage

| Req ID     | Test cases      | Covered? |
|------------|-----------------|----------|
| REQ-070-01 | TC-070-01       | yes      |
| REQ-070-02 | TC-070-02       | yes      |
| REQ-070-03 | TC-070-03       | yes      |
| REQ-070-04 | TC-070-04       | yes      |
| REQ-070-05 | TC-070-05       | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-070-01 — ADR-038 file exists with required structural sections

- **Requirement:** REQ-070-01
- **Level:** L5 (doc-content assertion; `grep` on committed file)
- **Artifact:** `docs/architecture/decisions/038-policy-engine-integration.md`

**Assertions (all must pass):**
- The file exists and is non-empty.
- The file contains a line matching `Status:` (either `Proposed` or `Accepted`).
- The file contains a `## Context` section.
- The file contains a `## Decision` section.
- The file contains a `## Consequences` section.
- The file references `ADR 035` (the tier seam predecessor).
- The file references `ADR 036` (the vault floor predecessor).

---

### TC-070-02 — ADR-038 documents the host-side pre-box decide placement and AuthZEN request shape

- **Requirement:** REQ-070-02
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains the string `decide` (the IPC operation name).
- The file contains at least one of `host-side` or `before` near `box` (the decide-gate
  ordering rule: decide runs before the box starts).
- The file contains `subject` (the AuthZEN request subject field).
- The file contains `action` (the AuthZEN request action field).
- The file contains `resource` (the AuthZEN request resource field).
- The file contains `context` (the AuthZEN request context field, carrying risk).
- The file contains `risk` (the risk context field; static value).
- The file contains `AGENT_BUILDER_POLICY_RISK` (the env var that sets risk level).
- The file contains `low` (the default risk value) OR contains `default` near `risk`
  (naming the default state explicitly).

---

### TC-070-03 — ADR-038 documents the obligation→seam map and fail-closed semantics

- **Requirement:** REQ-070-03
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains `tier_select` (the tier obligation type).
- The file contains `vault_injection_floor` (the vault floor obligation type).
- The file contains `audit_emit` (the audit obligation type).
- The file contains `require_approval` (the require-approval obligation type).
- The file contains `deny` (the deny decision value and the fail-closed default).
- The file contains `fail-closed` OR (`fail` AND `closed` within 10 words of each other)
  — naming the fail-closed posture.
- The file contains `never` near `box` OR `never starts` OR `box never` — capturing the
  "denied run never starts the box" invariant.
- The file contains `sandbox.Request.Tier` OR `Tier` near `tier_select` — naming the
  seam field that tier_select feeds.
- The file contains `InjectionMode` OR `injection_mode` near `vault_injection_floor` —
  naming the seam field that vault_injection_floor raises.

---

### TC-070-04 — ADR-038 documents opt-in env vars and the out-of-process invariant

- **Requirement:** REQ-070-04
- **Level:** L5 (doc-content assertion)

**Assertions:**
- The file contains `AGENT_BUILDER_POLICY_BIN` (the opt-in binary path env var).
- The file contains `AGENT_BUILDER_POLICY_SOCKET` OR `AGENT_BUILDER_POLICY_RISK`
  (at least one other policy env var named; the full set lands in task 072's spec).
- The file contains `unset` near `AGENT_BUILDER_POLICY_BIN` OR
  `zero regression` OR `unchanged path` — documenting the backward-compat guarantee
  when the var is absent.
- The file contains `out-of-process` OR `out of process` (the invariant name).
- The file contains `self-grant` OR `self grant` (the threat the invariant forbids).
- The file contains `in-process` AND (`not` OR `never` OR `no`) — ruling out in-process
  decide.

---

### TC-070-05 — ADR-038 names which spec files implementation tasks will update; `make check` exits 0

- **Requirement:** REQ-070-05
- **Level:** L5 (doc-consistency assertion + `make check`)

**Assertions:**
- The file contains `configuration.md` (the spec file tasks 072/073 must update with
  new env vars).
- The file contains either `architecture.md` OR `behaviors.md` OR `data-model.md` OR
  `diagrams.md` — naming at least one other spec file the implementation tasks will update.
- The file contains `EgressAllowlist` OR `egress allowlist` — documenting that
  `--allow` is fed from the existing egress allowlist.
- `make check` exits 0 with no test regressions introduced by the ADR file.

---

## Verification plan

- **Highest level achievable:** L5 — the ADR is a doc-only artifact; verification is
  `grep`-based content assertions + `make check` confirming no regressions.
- **Harness command:**
  ```
  grep -q "Status:" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "## Decision" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "ADR 035" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "ADR 036" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "decide" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "AGENT_BUILDER_POLICY_BIN" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "tier_select" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "vault_injection_floor" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "audit_emit" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "fail-closed" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "out-of-process" docs/architecture/decisions/038-policy-engine-integration.md && \
  grep -q "configuration.md" docs/architecture/decisions/038-policy-engine-integration.md && \
  make check
  ```
  Expected: all `grep` calls exit 0; `make check` final line `All checks passed.`

## Out of scope

- Writing any Go code.
- Updating `docs/spec/` files (those land in tasks 072 and 073 with their code changes).
- Writing the `internal/policy` client package (task 071).
- Writing lifecycle or runtime wiring (task 072).
- Writing `require_approval` or `audit_emit` obligation wiring (task 073).
- Writing the F-006 fitness check (task 074).
- Dynamic risk scoring (deferred per the approved plan).
- Changing existing behavior — this task commits only the ADR file.
