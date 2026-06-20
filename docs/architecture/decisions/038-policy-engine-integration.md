# ADR 038 — Policy-engine integration: host-side decide gate before exec-sandbox dispatch

**Status:** Proposed
**Date:** 2026-06-19
**Preceded by:** ADR 035 (adopt exec-sandbox block as default run backend), ADR 036 (vault integration — vault_injection_floor obligation references the `InjectionMode` seam introduced there)

## Context

ADR 035 wired the exec-sandbox block as agent-builder's default `run()` backend. ADR 036
brokered git/GitHub tokens through the vault block's egress proxy, adding the
`vault_injection_floor` concept: an obligation from the policy engine may raise
`RunWiring.InjectionMode` beyond what the calling code sets, but it must never lower it.

The policy-engine block (`~/Code/Public/policy-engine`, `make check` passes as of
2026-06-19) is the next un-adopted block on the roadmap. It provides an AuthZEN-compatible
`/decide` RPC over a Unix domain socket: the caller sends a subject/action/resource/context
tuple and receives `{ "decision": "allow" | "deny" | "require_approval", "obligations": [...] }`.

### The core design question

*Where* in the run pipeline does the `decide` call go?

Two wrong placements exist:

1. **In-process (inside the box):** a compromised agent could self-grant permission by
   patching the policy client before it fires. The out-of-process invariant (see § Decision)
   forbids this placement.

2. **After the box starts:** a `deny` verdict cannot stop work that has already begun.
   Containment has already been paid; the agent is already running.

The only correct placement is **host-side, in `runtime.Run`, before `sandboxBox.Create`
is called.** A denied run never reaches the box.

### Ordering invariant (load-bearing)

`decide` runs **after** vault handle resolution (so `vault_injection_floor` can raise the
already-resolved `InjectionMode`) but **before** `RunWiring.InjectionMode` is finalized and
before `sandboxBox.Create`. This sequencing is required so the `vault_injection_floor`
obligation can act on the fully-resolved set of vault handles.

### AuthZEN request shape

agent-builder constructs the following AuthZEN request for every `runtime.Run` call:

```json
{
  "subject":  {"type": "agent", "id": "agent-builder"},
  "action":   {"name": "run-task"},
  "resource": {
    "type": "task",
    "id":   "<task_id>",
    "properties": {"egress_hosts": ["api.github.com", "github.com"]}
  },
  "context": {"risk": "low"}
}
```

- `subject` identifies agent-builder itself (not the in-box agent).
- `action.name` is `"run-task"` — the single action agent-builder performs on the policy engine.
- `resource` carries the task id and the egress host set from `Limits.EgressAllowlist`.
- `context.risk` is a static value read from `AGENT_BUILDER_POLICY_RISK` (default `"low"`).

Dynamic risk scoring is **explicitly deferred**. The policy-engine block v0 is an allowlist
engine, not a risk scorer; risk scoring would require runtime telemetry not yet collected.

### Opt-in and rollout

The policy-engine integration is opt-in, controlled by `AGENT_BUILDER_POLICY_BIN` (the path
to the policy-engine block binary). When the var is unset, the `decide` call is skipped and
agent-builder behaves exactly as before this integration (zero regression). This mirrors
`AGENT_BUILDER_VAULT_BIN` and `AGENT_BUILDER_AUDIT_BIN`.

A second env var, `AGENT_BUILDER_POLICY_SOCKET`, controls the Unix socket path that
agent-builder uses to connect to the running policy daemon. A third env var,
`AGENT_BUILDER_POLICY_RISK`, sets the static risk value (default `"low"`).

### Fail-closed

Any failure on the `decide` path — socket dial error, timeout, malformed response, unknown
decision value, or any other error — maps to an implicit `deny`. The box never starts on
any failure path. This is the fail-closed posture: uncertainty about authorization is treated
as denial. There is no fallback to "allow on error."

## Decision

**Adopt the policy-engine block as a host-side, pre-box authorization gate for every
`runtime.Run` dispatch.**

### Out-of-process invariant

The `decide` call **must** run out-of-process on the trusted supervisor host — never
in-process inside the sandbox, and never inside the execution box. The policy-engine is a
separate daemon (started by agent-builder at launch via `AGENT_BUILDER_POLICY_BIN`) that
listens on a Unix socket. agent-builder connects to this socket and sends the AuthZEN
request host-side before `sandboxBox.Create` is invoked.

**In-process decide is explicitly ruled out.** An agent that can modify its own code (e.g.
via a prompt-injection or a supply-chain attack on a dependency) could patch an in-process
policy client to self-grant every request. Moving `decide` out-of-process removes this
self-grant threat: the policy daemon is a separate process on the trusted host that the
in-box agent cannot reach or modify.

### Placement in `runtime.Run`

```
runtime.Run:
  1. Resolve vault handles (AGENT_BUILDER_VAULT_BIN path)
  2. → [NEW] decide(subject=agent-builder, action=run-task, resource=task+egress, context.risk)
       → on deny or require_approval: set needs-human status, return — box never starts
       → on allow: apply obligations, continue
  3. Finalize RunWiring.InjectionMode (vault_injection_floor may have raised it)
  4. sandboxBox.Create  ← the box starts here, and only if step 2 returned allow
```

`decide` is called at step 2; `InjectionMode` is finalized at step 3 so the
`vault_injection_floor` obligation (which may raise the mode) can act first.

### Obligation→seam map

The policy engine returns zero or more obligations alongside its decision. agent-builder
maps each obligation to a specific seam:

| Obligation | Seam | Behavior |
|---|---|---|
| `decision=deny` | `runtime.Run` | Abort dispatch; write needs-human status reason `"policy: decision denied"`; box never starts |
| `decision=require_approval` | `runtime.Run` | Same as deny; status reason is `"policy: requires human approval"` (distinct — task 073) |
| `tier_select` | `sandbox.Request.Tier` | Set the Tier field (default `"bubblewrap"`) to the obligation value before box.Create |
| `vault_injection_floor` | `sandbox.RunWiring.InjectionMode` | Raise to obligation value if stricter; never lower (raise-only: `""` < `"env"` < `"proxy"`) |
| `audit_emit` | `audit.Sink` | Emit new `policy-decision` event (task 073) |

The `require_approval` path and `audit_emit` obligation are implemented in task 073.

### `--allow` feeds from `Limits.EgressAllowlist`

The policy daemon is started with `--allow <hosts-from-allowlist>`. The allowlist is drawn
from agent-builder's existing `Limits.EgressAllowlist` — the same set of hosts fed to the
exec-sandbox `NetConnect` capability. This ensures the policy engine's allowlist stays
synchronized with the sandbox's egress allowlist without a separate configuration surface.

### No unattended self-modification

The implementation tasks (071–073) add wiring code to agent-builder's `internal/` packages.
They do not edit agent-builder's own orchestration logic, Gate, loop, or task-source code
autonomously. The no-unattended-self-modification invariant from CLAUDE.md is preserved.

## Consequences

- **Every `runtime.Run` dispatch is gated by a host-side `decide` call** when
  `AGENT_BUILDER_POLICY_BIN` is set. The self-grant threat is removed: the policy daemon
  runs out-of-process and is unreachable from inside the execution box.
- **Fail-closed posture:** dial error, timeout, malformed response, and unknown decision
  values all map to deny. The box never starts on any error path. No silent fallback to
  allow.
- **Zero regression when unconfigured:** `AGENT_BUILDER_POLICY_BIN` unset = today's
  behavior. Existing deployments that do not set the var are unaffected.
- **Dynamic risk scoring deferred:** `context.risk` is a static value from
  `AGENT_BUILDER_POLICY_RISK` (default `"low"`). Dynamic scoring requires runtime telemetry
  not yet collected; this is a named follow-on, not a gap in the current design.
- **One new runtime dependency: policy daemon.** When `AGENT_BUILDER_POLICY_BIN` is set,
  the daemon must be startable at agent-builder launch; a missing or non-executable binary
  is a startup error (fail-loud, consistent with `AGENT_BUILDER_EXEC_SANDBOX_BIN` behavior).
- **`tier_select` obligation promotes the exec-sandbox tier** (ADR 035 seam) from a
  run-configuration default to a policy-driven field. The `sandbox.Request.Tier` field is
  the seam; `tier_select` writes it before `sandboxBox.Create`.
- **`vault_injection_floor` raise-only** (ADR 036 seam): the obligation may raise
  `RunWiring.InjectionMode` from `""` to `"env"` or `"proxy"`, or from `"env"` to `"proxy"`.
  It never lowers it. This prevents a policy obligation from weakening vault protections
  that the caller already configured.
- **New `policy-decision` audit event** (via `audit_emit` obligation, task 073): the audit
  chain gains a typed event for every policy decision, providing a forensic record of what
  the policy engine authorized.

## Spec files updated by implementation tasks

- `docs/spec/configuration.md` — task 072 adds `AGENT_BUILDER_POLICY_BIN`,
  `AGENT_BUILDER_POLICY_SOCKET`, and `AGENT_BUILDER_POLICY_RISK` env var entries in the
  same commit as the Go code that reads them. Not updated in this task.
- `docs/spec/architecture.md` — task 072 updates the run-pipeline component diagram to
  show the policy `decide` gate between vault handle resolution and `sandboxBox.Create`.
- `docs/spec/behaviors.md` — task 073 adds the `require_approval` behavior entry and the
  `audit_emit` obligation behavior.
- `docs/spec/data-model.md` — task 073 adds the `policy-decision` audit event type.
- `docs/architecture/diagrams.md` — task 072 updates the runtime flow diagram to show the
  out-of-process `decide` call on the host-side run path.
