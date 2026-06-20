# Task 073: require_approval + audit_emit obligations

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** done

## Goal

Complete the policy-engine obligation set wired by task 072:

1. **`require_approval`** — route to the needs-human path with a status reason distinct
   from `deny` (`"policy: requires human approval"` vs `"policy: decision denied"`).
2. **`audit_emit`** — emit a new `policy-decision` event on the existing `audit.Sink`
   when the obligation is present (regardless of the routing decision).

Update `docs/spec/behaviors.md`/`data-model.md` (new event type + require_approval
routing) and `docs/architecture/diagrams.md` if the audit emission flow changes the diagram.

## Context

Task 072 routes `require_approval` identically to `deny` (placeholder reason) and
skips `audit_emit`. This task fills both gaps.

### `require_approval` routing

The policy-engine block defines `require_approval` as a decision distinct from `deny`:
- `deny` = the action is forbidden unconditionally.
- `require_approval` = the action is allowed if a human reviews it.

Agent-builder's v0 behavior for `require_approval` is:
- The box does NOT start (same as deny — we cannot safely dispatch without human
  review, and the agent loop cannot wait for asynchronous human input in v0).
- The task status reason is `"policy: requires human approval"` — observably different
  from `"policy: decision denied"`.
- `runtime.Run` returns nil (valid terminal outcome, not a system error).

This distinction matters for operators: the different reason string tells them *why*
the task needs human attention. A `deny` reason means "fix the policy or the request";
a `require_approval` reason means "a human needs to authorize this specific run."

### `audit_emit` obligation

When the policy engine includes `{"type":"audit_emit","value":true}` in obligations,
agent-builder must emit a structured event on the existing `audit.Sink`. The event type
is `ActionPolicyDecision` — a new constant added to `internal/audit/audit.go`.

The `policy-decision` event carries:
```go
AuditEvent{
    Action: ActionPolicyDecision,  // new constant: "policy-decision"
    RunID:  runID,
    TaskID: task.ID,
    Detail: EventDetail{
        PolicyDecision: string(response.Decision),  // "allow", "deny", or "require_approval"
        PolicyReason:   response.Context.Reason,    // from the policy engine response
    },
}
```

`EventDetail` gains two new optional fields (`PolicyDecision`, `PolicyReason`) — since
they are only set for `ActionPolicyDecision` events, they do not affect any other
event type.

**The `audit_emit` obligation is a side-effect, not a routing modifier.** An `allow` +
`audit_emit` is still an allow; a `deny` + `audit_emit` blocks dispatch AND emits the
event; a `require_approval` + `audit_emit` writes the needs-human status AND emits.
If the `audit.Sink` is nil (audit not configured), the obligation is a no-op.

### Validation update

`internal/audit/audit.go`'s `Validate` function must accept `ActionPolicyDecision` as
a valid action. The `validActions` map gains the new constant.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                                               | Priority  |
|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-073-01 | `require_approval` decision routes to needs-human path with status reason containing `"approval"` or `"require_approval"` — observably different from the `deny` reason. `runtime.Run` returns nil. The sandbox runner receives zero calls. | must have |
| REQ-073-02 | `audit_emit` obligation (when present and `audit.Sink` is configured) emits `AuditEvent{Action: ActionPolicyDecision, …}` carrying the decision and reason. Obligation absent = no `ActionPolicyDecision` event emitted. Nil sink = no-op. | must have |
| REQ-073-03 | `internal/audit/audit.go` adds `ActionPolicyDecision AuditAction = "policy-decision"` to the closed enum and `validActions` map. `EventDetail` gains optional `PolicyDecision` and `PolicyReason` string fields. `docs/spec/data-model.md`/`behaviors.md` updated. `make check` exits 0. | must have |

## Readiness gate

- [x] Test spec `073-require-approval-and-audit-emit-obligations-test-spec.md` exists (written first)
- [ ] Task 072 (PolicyDaemon lifecycle + decide-gate) merged and verified
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-073-01] TC-073-01: `require_approval` → needs-human status with `"approval"` in reason; box not started
- [ ] [REQ-073-01] TC-073-02: deny and require_approval status reasons are observably different strings; both block dispatch
- [ ] [REQ-073-02] TC-073-03: `audit_emit` + `allow` → `FakeSink` receives `ActionPolicyDecision` event with decision + reason
- [ ] [REQ-073-02] TC-073-04: `audit_emit` + `deny` → event emitted AND dispatch blocked; no `audit_emit` = no event; nil sink = no panic
- [ ] [REQ-073-03] TC-073-05: `ActionPolicyDecision` in `validActions`; `EventDetail` has PolicyDecision/PolicyReason; spec files updated; `make check` green; Phase-0 capstone unaffected

## Verification plan

- **Highest level achievable:** L5 — all unit tests + `make check` + fake-binary e2e.
- **Harness command:**
  ```
  go test -count=1 ./internal/audit/... ./internal/policy/... ./internal/runtime/...
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPolicyGate'
  make check
  ```
  Expected: all `ok`; `make check` → `All checks passed.`
- **L6 capstone (operator-gated; not a backlog task):** Live policy-engine run where
  `require_approval` is triggered and the audit ledger carries a `policy-decision` event
  with the correct `decision` and `reason` fields. Tracked in
  `docs/plans/l6-operator-runbook.md`.

## Out of scope

- `tier_select` and `vault_injection_floor` obligation wiring (task 072).
- PolicyDaemon lifecycle (task 072).
- The F-006 fitness check (task 074).
- Asynchronous require_approval workflows (the agent cannot wait for human input in
  v0; needs-human status is the terminal outcome).
- Rekor anchoring of the policy-decision event.
- Rate-limiting or caching of audit events (these are side-effects of the obligation;
  the `audit.Sink` handles buffering).

## Dependencies

- Task 072 (PolicyDaemon lifecycle + decide-gate) — the decide-gate must be wired;
  `require_approval` placeholder routing must already be in place before this task
  replaces it with the distinct reason.
- `internal/audit` package must be importable and `FakeSink` must exist (task 038 —
  already done; no new dependency).
