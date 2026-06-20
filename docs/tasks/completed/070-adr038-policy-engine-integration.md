# Task 070: ADR-038 — policy-engine integration decision

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** completed

## Goal

Write `docs/architecture/decisions/038-policy-engine-integration.md`. This is the
architectural decision record for adopting the policy-engine block to gate every
`runtime.Run` dispatch before exec-sandbox is invoked. The ADR is a human-reviewable
planning artifact; the implementation tasks (071–074) depend on it being accepted
before they begin.

No production code is written or modified in this task.

## Context

The policy-engine block is shipped and gate-green (`~/Code/Public/policy-engine`,
`make check` passes). The roadmap names it the "next un-adopted block" as of
2026-06-19, after vault (tasks 064–066) and signed-checkpoints (tasks 067–069).

The key design question resolved by this ADR: *where* in the run pipeline does the
decide call go, and *what* does the AuthZEN request carry? The answers are pre-approved
per the scope lock above — this ADR records them as a permanent, referenceable decision.

**Why decide must run host-side, before the box starts:**
The policy-engine's primary security invariant is out-of-process: an agent cannot
self-grant by editing its own code. Agent-builder enforces this by calling `decide`
host-side (in `runtime.Run`) before `sandboxBox.Create` is called. If the decision
were made inside the box, the compromised agent could bypass it. If the decision were
made after the box starts, a `deny` verdict cannot stop the work that has already begun.

**AuthZEN request shape:**
```json
{
  "subject":  {"type": "agent", "id": "agent-builder"},
  "action":   {"name": "run-task"},
  "resource": {"type": "task", "id": "<task_id>", "properties": {"egress_hosts": ["api.github.com", "github.com"]}},
  "context":  {"risk": "low"}
}
```
`context.risk` is static: read from `AGENT_BUILDER_POLICY_RISK` (default `"low"`).
Dynamic risk scoring is deferred — the policy-engine block defers it too (v0 is an
allowlist engine, not a risk scorer).

**Obligation→seam map (all four, wired across tasks 072/073):**

| Obligation | Seam | Behavior |
|---|---|---|
| `decision=deny` | `runtime.Run` | Abort dispatch; write needs-human status reason `"policy: decision denied"`; box never starts |
| `decision=require_approval` | `runtime.Run` | Same as deny; status reason is `"policy: requires human approval"` (distinct — task 073) |
| `tier_select` | `sandbox.Request.Tier` | Set the tier field (default `"bubblewrap"`) to the obligation value |
| `vault_injection_floor` | `sandbox.RunWiring.InjectionMode` | Raise to obligation value if stricter; never lower |
| `audit_emit` | `audit.Sink` | Emit new `policy-decision` event (task 073) |

**Fail-closed:** unknown decision value, malformed response, dial error, timeout → deny.
The box never starts on any failure path.

**`--allow` flag:** policy-engine's allowlist is seeded from agent-builder's
`Limits.EgressAllowlist`. The daemon is started with `--allow <hosts-from-allowlist>`.

**Opt-in:** `AGENT_BUILDER_POLICY_BIN` (unset = today's behavior, zero regression),
mirroring `AGENT_BUILDER_VAULT_BIN` / `AGENT_BUILDER_AUDIT_BIN`.

**Ordering invariant (load-bearing, named in task 072 spec):** decide runs after vault
handle resolution but before `RunWiring.InjectionMode` is finalized, so the
`vault_injection_floor` obligation can raise the mode on already-resolved handles.

## Requirements

| Req ID     | Description                                                                                                                                                                           | Priority  |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-070-01 | ADR file `docs/architecture/decisions/038-policy-engine-integration.md` exists with Status, Context, Decision, and Consequences sections; references ADR 035 and ADR 036. | must have |
| REQ-070-02 | Decision documents: host-side per-run `decide` before box.Create; the full AuthZEN request shape (subject/action/resource/context.risk); static risk via `AGENT_BUILDER_POLICY_RISK` (default `"low"`); dynamic scoring deferred. | must have |
| REQ-070-03 | Decision documents the obligation→seam map for all four obligations; fail-closed semantics (deny on any error); "box never starts" on deny/require_approval; raise-only for `vault_injection_floor`. | must have |
| REQ-070-04 | Decision documents: opt-in `AGENT_BUILDER_POLICY_BIN`; out-of-process invariant; in-process decide explicitly ruled out; `--allow` fed from `Limits.EgressAllowlist`. | must have |
| REQ-070-05 | ADR names which spec files tasks 072/073 will update (`configuration.md`, `architecture.md`, `behaviors.md`/`data-model.md`, `diagrams.md`). `make check` exits 0. | must have |

## Readiness gate

- [ ] Test spec `070-adr038-policy-engine-integration-test-spec.md` exists (written first)
- [ ] ADR 035 and ADR 036 are in `docs/architecture/decisions/` (already done)
- [x] Human has reviewed and approved the scope (this ADR is "Ask-first" per CLAUDE.md) —
  approved 2026-06-19. Scope is pre-locked by the task description: the architectural
  decisions are constraint-forced (out-of-process is the policy-engine's core invariant;
  host-side pre-box is the only placement that satisfies it; static risk and defer-dynamic
  are policy-engine v0 design choices inherited from the block). No open product decision
  remains; the executor may author the ADR autonomously.

## Acceptance criteria

- [ ] [REQ-070-01] TC-070-01: ADR file exists; has Status/Context/Decision/Consequences; references ADR 035 + ADR 036
- [ ] [REQ-070-02] TC-070-02: host-side pre-box placement, AuthZEN request shape, AGENT_BUILDER_POLICY_RISK documented
- [ ] [REQ-070-03] TC-070-03: all four obligations mapped; fail-closed; deny → box never starts; vault_injection_floor raise-only
- [ ] [REQ-070-04] TC-070-04: AGENT_BUILDER_POLICY_BIN opt-in; out-of-process invariant; --allow from EgressAllowlist
- [ ] [REQ-070-05] TC-070-05: spec-file update note present; `make check` exits 0

## Verification plan

- **Highest level achievable:** L5 — doc-content `grep` assertions + `make check`
  (no runtime surface; ADR is a markdown file).
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
  Expected: all exit 0; `make check` → `All checks passed.`
- **Runtime observation:** N/A — no runtime surface.

## Out of scope

- Writing any Go or shell code.
- Updating `docs/spec/` files (those land in 072/073 with their code changes).
- Writing the `internal/policy` package (task 071).
- Writing lifecycle management or runtime wiring (task 072).
- Writing `require_approval` / `audit_emit` obligation wiring (task 073).
- Writing the F-006 fitness check (task 074).
- Dynamic risk scoring (deferred).
- Changing existing behavior — this task commits only the ADR file.

## Dependencies

- ADR 035 (adopt exec-sandbox block) — already written and accepted (task 062).
- ADR 036 (vault integration) — already written and accepted (task 064).
- Human approval of scope before the task begins (CLAUDE.md "Ask first" for ADR authoring).
