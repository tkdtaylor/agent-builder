# Test spec — Task 085: Orchestrator self-containment + policy gating + fleet audit

**Linked task:** `docs/tasks/backlog/085-orchestrator-containment-policy-audit.md`
**Written:** 2026-06-27
**Status:** stub — blocked by tasks 081, 083, 084

## Context

The orchestrator is privileged, network-connected, and long-lived — so it must
itself be contained, gated, and audited, not merely the workers. This task:

1. Runs the orchestrator **inside exec-sandbox** (the orchestrator box).
2. Gates orchestrator actions — what recipes it may spawn, with what parameters,
   what egress it has — via **policy-engine `decide`** (same block, same IPC seam,
   new policy subject).
3. Records a **fleet-wide audit-trail** of every agent spawned and every action
   taken across both tiers (a single audit chain covers both the orchestrator and
   all its workers).

This task is **blocked by tasks 081, 083, 084** — the orchestrator must exist and
have both transport and state-guard before containment and auditing can be layered on.

**Detailed task shape is deferred** pending those prerequisites.

## Requirements coverage (preliminary)

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-085-01 | Orchestrator process runs inside exec-sandbox                                   | TC-085-01  |
| REQ-085-02 | policy-engine gates orchestrator recipe-spawn and egress actions                | TC-085-02  |
| REQ-085-03 | audit-trail records fleet-wide events across both tiers in one chain           | TC-085-03  |
| REQ-085-04 | Orchestrator's own egress is default-deny (same containment model as workers)  | TC-085-04  |
| REQ-085-05 | No agent at any tier edits agent-builder's own repo (invariant check)          | TC-085-05  |

## Pre-implementation checklist

- [ ] Task 081 merged (orchestrator core)
- [ ] Task 083 merged (agent-mesh transport)
- [ ] Task 084 merged (memory-guard state)
- [ ] policy-engine policy schema for orchestrator actions defined
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-085-01 — Orchestrator runs inside exec-sandbox

- **Requirement:** REQ-085-01
- **Level:** L2 (unit test) / L6 (live observation)
- **Status:** stub

**Input:** Start the orchestrator via `runtime.Run` with an orchestrator recipe.

**Expected output:**
- The run record includes `containment=exec-sandbox` for the orchestrator process.
- Same exec-sandbox isolation constraints apply (rootless, read-only rootfs,
  default-deny egress, resource limits).

---

### TC-085-02 — Policy-engine gates orchestrator spawn actions

- **Requirement:** REQ-085-02
- **Level:** L2 (unit test with stub policy-engine)
- **Status:** stub

**Input:** Orchestrator attempts to spawn a worker with a recipe name not in the
policy allowlist.

**Expected output:**
- policy-engine returns `deny`; no worker is spawned.
- Orchestrator reports the policy denial through the channel.

---

### TC-085-03 — Fleet-wide audit trail covers both tiers

- **Requirement:** REQ-085-03
- **Level:** L2 (unit test with FakeSink)
- **Status:** stub

**Input:** Orchestrator spawns two workers; both complete.

**Expected output:**
- The audit chain includes: orchestrator goal-intake event, plan-approved event,
  worker-spawned events (one per worker), worker-completed events.
- `audit-trail verify` on the chain returns `valid=true`.

---

### TC-085-04 — Orchestrator egress is default-deny

- **Requirement:** REQ-085-04
- **Level:** L2 / L5 (egress probe)
- **Status:** stub

**Input:** Orchestrator container attempts a connection to a non-allowlisted host.

**Expected output:**
- Connection blocked (same nftables egress enforcement as worker boxes).

---

### TC-085-05 — No agent edits agent-builder's own repo (invariant)

- **Requirement:** REQ-085-05
- **Level:** L3 (fitness check or policy assertion)
- **Status:** stub

**Input:** Inspect the policy rules configured for orchestrator + worker recipe-spawn.

**Expected output:**
- No recipe may target `github.com/tkdtaylor/agent-builder` as a result sink.
- A fitness check or policy-engine test asserts this is unreachable.

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 (unit) / L5 (full orchestrator run with stubbed
  workers and real exec-sandbox). L6 requires a live multi-worker orchestrator run.
- **L2 harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/orchestrator/...
  ```
  Expected: `ok`.

## Out of scope

- Multi-worker concurrent dispatch (task 086) — this task adds containment/policy/
  audit to the single-orchestrator case.
- Key rotation for orchestrator signing keys.
