# Task 085: Orchestrator self-containment + policy gating + fleet audit

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Apply the full security model to the orchestrator itself — not only its workers.
Three sub-goals in one task (they are tightly coupled and touch the same seams):
(1) run the orchestrator inside exec-sandbox; (2) gate orchestrator actions via
policy-engine `decide`; (3) emit a fleet-wide audit chain covering events from both
tiers in a single tamper-evident chain.

## Context

ADR 042: "The orchestrator is itself privileged, network-connected, and long-lived,
so it must itself be contained, gated, and audited." These three controls share
plumbing (the orchestrator's run config, the exec-sandbox launch path, the audit
sink); separating them into three tasks would require re-touching the same wiring
three times.

**Blocked by tasks 081, 083, 084.** Containment, policy gating, and fleet audit
require the orchestrator to exist with transport and state-guard before layering
security on top. **Detailed task shape is deferred** pending those prerequisites.

## Requirements

| Req ID     | Description                                                                                                                                    | Priority  |
|------------|------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-085-01 | Orchestrator process runs inside exec-sandbox; run record carries `containment=exec-sandbox`. | must have |
| REQ-085-02 | policy-engine gates orchestrator recipe-spawn and egress; deny → no worker spawned; allow → worker dispatched. | must have |
| REQ-085-03 | Fleet-wide audit chain covers both tiers (orchestrator events + all worker events) in one chain; `audit-trail verify` returns `valid=true`. | must have |
| REQ-085-04 | Orchestrator's own egress is default-deny (same nftables enforcement as workers). | must have |
| REQ-085-05 | No recipe may target `github.com/tkdtaylor/agent-builder` as a result sink; a policy rule or fitness check asserts this. | must have |

## Readiness gate

- [x] Test spec `085-orchestrator-containment-policy-audit-test-spec.md` exists (written first)
- [ ] Task 081 merged (orchestrator core)
- [ ] Task 083 merged (agent-mesh transport)
- [ ] Task 084 merged (memory-guard state)
- [ ] policy-engine policy schema for orchestrator actions defined
- [ ] All test cases in test spec refined into full inputs/expected-outputs
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-085-01] TC-085-01: Orchestrator run record includes `containment=exec-sandbox`; same isolation constraints as workers
- [ ] [REQ-085-02] TC-085-02: Policy-engine stub returns `deny` for a recipe-spawn action → no worker started; denial reported
- [ ] [REQ-085-03] TC-085-03: 2 workers spawned and completed → fleet audit chain includes all 5+ events; `audit-trail verify` → `valid=true`
- [ ] [REQ-085-04] TC-085-04: Orchestrator container → non-allowlisted egress blocked (egress probe or unit assertion)
- [ ] [REQ-085-05] TC-085-05: Policy rule or fitness check asserts no recipe targets `agent-builder`'s own repo; test demonstrates the guard fires

## Verification plan

- **Highest level achievable:** L2 (unit) / L5 (full orchestrator run with real
  exec-sandbox). L6 requires a live multi-worker orchestrator.
- **Harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/orchestrator/...
  make check
  ```
  Expected: `ok`; `All checks passed.`

## Out of scope

- Multi-worker concurrent dispatch (task 086).
- Key rotation for orchestrator signing keys.

## Dependencies

- Task 081 (orchestrator core)
- Task 083 (agent-mesh transport)
- Task 084 (memory-guard state)
- Informs: task 086 (concurrent dispatch builds on the fully-secured orchestrator)
