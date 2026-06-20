# Test spec — Task 073: require_approval + audit_emit obligations

**Linked task:** `docs/tasks/backlog/073-require-approval-and-audit-emit-obligations.md`
**Written:** 2026-06-19
**Status:** ready

## Context

Task 072 wires the decide-gate into `runtime.Run` and handles `deny`, `tier_select`,
and `vault_injection_floor`. This task completes the obligation set:

**`require_approval`** — when the policy engine returns `decision: require_approval`,
the run must:
1. Write a needs-human status reason distinct from `deny` (e.g.
   `"policy: requires human approval"` vs `"policy: decision denied"`).
2. NOT dispatch the box (same as deny — the box never starts).
3. Return from `runtime.Run` without an unexpected error (it is a valid terminal
   outcome, not a system error).

The status reason is the observable distinction between deny and require_approval.
This matters for operators: deny means "this action is forbidden"; require_approval
means "this action is allowed if a human reviews it." The agent must surface the
distinction so the operator can act on the correct signal.

**`audit_emit`** — when the policy engine returns an `audit_emit` obligation (value
`true`), agent-builder must emit a new `policy-decision` event on the existing
`audit.Sink`. The event carries:
- `Action: ActionPolicyDecision` (a new action constant in `internal/audit/audit.go`)
- `RunID`, `TaskID` (inherited from the run context)
- `Detail` fields: the policy decision (allow/deny/require_approval) and the reason
  string from the policy engine response.

The `audit_emit` obligation is a side-effect — the obligation DOES NOT change the
routing decision. An `allow` + `audit_emit` is still an allow. A `deny` + `audit_emit`
still blocks dispatch, and also emits the audit event.

**Spec updates in this task (same commit as code):**
- `docs/spec/behaviors.md` — `require_approval` routing, distinct status reason.
- `docs/spec/data-model.md` — new `policy-decision` event type with its fields.
- `docs/architecture/diagrams.md` — update if the audit emission flow changes the diagram.

## Requirements coverage

| Req ID     | Test cases                  | Covered? |
|------------|-----------------------------|----------|
| REQ-073-01 | TC-073-01, TC-073-02        | yes      |
| REQ-073-02 | TC-073-03, TC-073-04        | yes      |
| REQ-073-03 | TC-073-05                   | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-073-01 — `require_approval` routes to needs-human with a distinct status reason from `deny`

- **Requirement:** REQ-073-01
- **Level:** L5 (e2e with fake policy binary)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go`
- **Test name:** `TestPolicyGateRequireApprovalDistinctFromDeny`

**Setup:**
1. Fake policy binary that responds with:
   ```json
   {"decision":"require_approval","context":{"reason":"high risk task","obligations":[]}}
   ```
2. Fake sandbox runner (`FakeRunner`) configured to record whether it was called.
3. `runtime.Run` with a ready task in the task root.

**Assertions:**
- `runtime.Run` returns without error.
- The sandbox runner records zero `Run` calls — the box never starts.
- The task status file shows a needs-human status whose `reason` field (or equivalent)
  contains `"approval"` or `"require_approval"` — distinctly NOT the same string used
  for a `deny` decision.
- When run with a `deny`-responding fake binary, the status reason does NOT contain
  `"approval"` (the two reasons are observably different).

---

### TC-073-02 — `require_approval` does not dispatch; distinguishable from `deny` in status

- **Requirement:** REQ-073-01
- **Level:** L5 (unit/integration test)
- **Test name:** `TestRequireApprovalStatusReason`

**Table-driven test (comparing deny vs require_approval):**

| Decision policy returns | Expected status reason keyword | Box started? |
|-------------------------|-------------------------------|-------------|
| `"deny"` | contains `"denied"` or `"deny"` | no |
| `"require_approval"` | contains `"approval"` | no |

**Assertions:**
- Both decisions result in zero box invocations.
- The two status reasons are observably different strings (string inequality).
- `runtime.Run` returns nil for both (both are valid terminal outcomes, not panics).

---

### TC-073-03 — `audit_emit` obligation causes a `policy-decision` event on the `audit.Sink`

- **Requirement:** REQ-073-02
- **Level:** L5 (unit test with `FakeSink`)
- **Test file:** `internal/runtime/run_test.go` or `tests/e2e/policy_gate_e2e_test.go`
- **Test name:** `TestPolicyGateAuditEmitObligation`

**Setup:**
1. Configure a `FakeSink` as the audit sink.
2. Fake policy client returning:
   ```json
   {"decision":"allow","context":{"reason":"allowlisted","obligations":[{"type":"audit_emit","value":true}]}}
   ```
3. Run `runtime.Run` with a ready task, fake executor, and fake runner.

**Assertions:**
- `FakeSink.Events()` contains at least one event with `Action == ActionPolicyDecision`
  (the new action constant).
- That event's `Detail` carries the decision value (`"allow"`) and the reason string
  (`"allowlisted"`).
- The event's `RunID` and `TaskID` are non-empty (inherited from the run context).
- No error is returned from `runtime.Run` (the audit obligation is a side-effect; it
  does not change the routing decision — `allow` still proceeds normally).

---

### TC-073-04 — `audit_emit` with `deny` or `require_approval` still emits; box still blocked

- **Requirement:** REQ-073-02
- **Level:** L5 (unit test with `FakeSink`)
- **Test name:** `TestPolicyGateAuditEmitWithDenyDecision`

Sub-cases:

| Decision | `audit_emit` obligation | Expected sink events | Box started? |
|----------|------------------------|----------------------|-------------|
| `"deny"` | `{"type":"audit_emit","value":true}` | ≥ 1 with `Action == ActionPolicyDecision` | no |
| `"require_approval"` | `{"type":"audit_emit","value":true}` | ≥ 1 with `Action == ActionPolicyDecision` | no |
| `"allow"` | (no `audit_emit` obligation) | 0 with `Action == ActionPolicyDecision` | yes |

**Assertions for each sub-case:**
- `audit_emit` obligation present → the `FakeSink` receives the `ActionPolicyDecision`
  event regardless of the routing outcome.
- `audit_emit` obligation absent → no `ActionPolicyDecision` event is emitted.
- `FakeSink` is nil / not configured → `audit_emit` obligation is a no-op (no panic).

---

### TC-073-05 — spec files updated; `ActionPolicyDecision` in `audit.go`; `make check` green

- **Requirement:** REQ-073-03
- **Level:** L3 / L5
- **Test file:** `Makefile` + spec files (grep assertions)

**Assertions:**
- `internal/audit/audit.go` defines `ActionPolicyDecision AuditAction = "policy-decision"`.
- `validActions` map in `audit.go` includes `ActionPolicyDecision`.
- `docs/spec/data-model.md` (or `behaviors.md`) describes the `policy-decision` event
  type with its fields: decision, reason, run_id, task_id.
- `docs/spec/behaviors.md` documents the `require_approval` routing path with a
  distinct status reason from `deny`.
- `go test ./internal/audit/... -count=1` → `ok` (the audit package tests pass with
  the new action constant in the closed enum).
- `make check` → `All checks passed.`
- `go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance` passes with
  policy unconfigured (opt-in; existing capstone unaffected).

---

## Verification plan

- **Highest level achievable:** L5 — all unit tests + `make check` + fake-binary e2e.
- **L5 harness command:**
  ```
  go test -count=1 ./internal/audit/... ./internal/policy/... ./internal/runtime/...
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPolicyGate'
  make check
  ```
  Expected: all `ok`; final line `All checks passed.`
- **L6 capstone (operator-gated, not a backlog task):** A live policy-engine run where
  `require_approval` is triggered and the audit ledger carries the `policy-decision`
  event with the correct decision and reason fields. See `docs/plans/l6-operator-runbook.md`.

## Out of scope

- `tier_select` and `vault_injection_floor` obligation wiring (task 072).
- PolicyDaemon lifecycle (task 072).
- The F-006 fitness check (task 074).
- Dynamic risk scoring or require_approval workflow escalation beyond the needs-human
  status reason (policy-engine v1 defers this).
- Rekor anchoring of the policy-decision event (deferred; separate follow-up).
