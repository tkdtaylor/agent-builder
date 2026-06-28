# ADR 050 — orchestrator policy schema + the scope of self-containment, gating, and fleet audit

**Status:** Accepted (2026-06-28) — design-only. Resolves OQ-7 (policy schema for
orchestrator actions) and scopes task 085's three coupled controls (containment, policy
gating, fleet audit) so its stub test spec can be expanded and implemented. No
code/spec/diagram lands with this ADR.
**Date:** 2026-06-28
**Extends:** ADR 042 (the orchestrator is itself privileged → must be contained, gated,
audited; the bright line "no agent at any tier edits agent-builder's own repo"), ADR 046
(names the `spawn-plan`/`spawn-worker` actions distinct from the worker `run-task` gate),
ADR 038 (the policy-engine `decide` IPC seam, AuthZEN request shape), ADR 026 (the
audit-trail chain). Does not contradict any.
**Motivated by:** task 085 readiness gate ("policy-engine policy schema for orchestrator
actions defined") and its three tightly-coupled requirements.

## Context

Task 081 already issues one orchestrator policy decision: `spawn-plan`
(`internal/orchestrator/orchestrator.go` — `policy.DecideRequest{Subject:{agent,
orchestrator}, Action:{spawn-plan}, Resource:{plan, goalID}, Context:{risk}}`,
fail-closed: any error → deny). `policy.Decide` returns `allow | deny |
require_approval`. Task 085 must extend this into a full schema and add two more
controls (containment, fleet audit) that share the orchestrator's run config, the
exec-sandbox launch path, and the audit sink — which is why ADR 042 / the task keep them
in one task rather than re-touching the same wiring three times.

The verification reality: real exec-sandbox (rootless Podman + runsc) and real nftables
egress enforcement are **L6 operator-run on a provisioned host** (exactly as tasks
014/015/016 established — they are ✅ via L6 real-host runs, not CI). So task 085's
containment/egress requirements are **L2 in CI** (the run config/record carries the right
containment + egress posture, unit-asserted) and **L6 deferred** (the live Podman/nftables
enforcement is observed on a real host, not in CI). The policy-gating and fleet-audit
requirements are L2/L5 (stub policy + FakeSink, and the real `audit-trail verify` binary as
tasks 039/040 did).

## Decision

### 1. Orchestrator policy schema — three actions over the existing AuthZEN `decide` seam

The orchestrator issues exactly three policy actions (Subject is always
`{Type:"agent", ID:"orchestrator"}`, decisions are always fail-closed → deny on error):

| Action | When | Resource | Decision handling |
|--------|------|----------|-------------------|
| `spawn-plan` | once, after decomposition, before any dispatch (exists, task 081) | `{Type:"plan", ID:goalID}`, `Context:{risk}` | allow→dispatch; deny→report+stop; require_approval→pause-and-resume |
| `spawn-worker` | **per sub-goal**, immediately before dispatching that worker (NEW) | `{Type:"recipe", ID:recipeName, Properties:{target_repo, sink}}` | allow→dispatch this worker; deny→skip this worker + record a denied outcome + report; require_approval→hold (reuse 081's pause path) |
| `egress` | the orchestrator's own outbound network attempts (NEW) | `{Type:"host", ID:"host:port"}` | allow→permit; deny→block (default-deny) |

`spawn-plan` gates the whole plan; `spawn-worker` gates each dispatch (so a per-recipe
policy can deny one worker without denying the plan). These are additive to the worker's
own `run-task` gate inside `runtime.Run` — a dispatched worker is gated twice (orchestrator
`spawn-worker` + the worker's own `run-task`), which is the intended defense-in-depth.

### 2. Self-repo bright line (REQ-085-05) — belt-and-suspenders: a runtime policy deny + a static fitness check

ADR 042's non-negotiable invariant is "no agent at any tier edits agent-builder's own
repo." Task 085 enforces it two independent ways:
- **Runtime:** the `spawn-worker` decision **denies** any worker whose `target_repo` /
  result sink is `github.com/tkdtaylor/agent-builder` (the orchestrator refuses to
  dispatch it, fail-closed, regardless of what the policy file says).
- **Static:** a **fitness check** asserts no registered recipe's `RoutingSpec` / result
  sink targets the own-repo, so a recipe that would target it can't even be registered
  without the gate firing.

Either alone would be a single point of failure; both together make the bright line
unreachable by construction *and* at runtime.

### 3. Containment (REQ-085-01/04) — L2 run-record posture now, L6 live enforcement deferred

The orchestrator process is launched with the **same exec-sandbox containment profile as
a worker**: rootless, read-only rootfs, resource limits, and **default-deny egress** (the
nftables allowlist). In CI this is asserted at **L2**: the orchestrator's run config /
run record carries `containment=exec-sandbox` and an egress posture of default-deny (a
unit assertion on the config/record, mirroring how the worker boxes are configured). The
**live enforcement** (a real connection to a non-allowlisted host being blocked by
nftables under rootless Podman+runsc) is **L6 operator-run on a provisioned host**, exactly
as tasks 014/015/016 — it is recorded as deferred in the verify row, not claimed in CI.

### 4. Fleet audit (REQ-085-03) — one chain across both tiers

Orchestrator events (goal-intake, plan-decided, spawn-decided per worker, tamper, etc.)
and **all** worker events append to a **single** `audit.Sink` chain, so the chain is
tamper-evident across both tiers. Verification: `audit-trail verify` on the chain returns
`valid=true` — at **L5** using the real `audit-trail` binary (as tasks 039/040 ran it),
with an L2 `FakeSink` ordering/coverage assertion that all expected events
(orchestrator + N workers) are present in one chain.

## Consequences

- **Design-only.** No code/spec/diagram lands with this ADR; task 085 makes them.
- **Task 085 unblocked.** TCs expand: TC-085-01 run-record `containment=exec-sandbox`
  (L2) + L6 deferred; TC-085-02 `spawn-worker` deny → no worker + denial reported (stub
  policy); TC-085-03 one fleet chain covering orchestrator + 2 workers, `audit-trail
  verify` valid=true; TC-085-04 egress default-deny posture asserted (L2) + L6 probe
  deferred; TC-085-05 the runtime self-repo deny + the static fitness check both fire.
- **`spawn-worker` is the new per-dispatch gate** the orchestrator issues from
  `dispatchPlan`, additive to 081's `spawn-plan`. The orchestrator's `PolicyClient` seam
  is unchanged (same `Decide` interface) — only new request shapes.
- **Defense-in-depth, not new mechanism.** Containment reuses the worker exec-sandbox
  profile; gating reuses `policy.Decide`; audit reuses the existing chain/sink — the task
  is wiring, not new primitives. No accidental-monolith: the orchestrator stays
  decompose→gate→dispatch→aggregate→report; containment/policy/audit are cross-cutting
  controls applied to it, the same ones applied to workers.
- **Feeds task 086.** Concurrent dispatch runs on the now-fully-secured orchestrator; the
  fleet audit chain must cover N concurrent workers (086 REQ), building on this single-chain
  design.
- **Invariants hold:** orchestrator is contained/gated/audited like a worker; the self-repo
  bright line is enforced twice; gating is fail-closed; the audit chain is single and
  tamper-evident across tiers; live containment is honestly marked L6-deferred, never
  claimed in CI.
