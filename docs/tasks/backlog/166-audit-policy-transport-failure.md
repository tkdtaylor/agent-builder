# Task 166: audit and distinguish a policy `Decide` transport/parse failure from a genuine deny

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Make `decideGate` capture the `policy.Decide` call's error instead of discarding
it, produce a distinct halt reason for a transport/parse failure versus a genuine
policy-authored deny, and emit an `ActionPolicyDecision` audit event
unconditionally on a transport/parse failure (not gated on the, necessarily
absent, `audit_emit` obligation) whenever an audit sink is configured.

## Context

**Deviation note.** This task ID was originally scoped by planning to "emit an
audit event on every policy decide, satisfying the audit_emit obligation." That is
already fully built in task 073 (`maybeEmitPolicyDecision`,
`internal/runtime/run.go:1509-1527`) and tracked **Adopted (L5)** in
`docs/plans/roadmap.md`. This task ID is repurposed for a real, narrow residual
gap in the same code: the one decide outcome that most needs an audit trail (the
engine was unreachable or returned garbage, so agent-builder correctly failed
closed) can never produce one.

**The gap, with exact evidence:**
- `decideGate` (`internal/runtime/run.go:1035-1037`) does
  `resp, _ := policy.NewClient(socketPath).Decide(req)`, the error is discarded.
- `policy.Decide` (`internal/policy/client.go:156-194`) is fail-closed by design: a
  dial/timeout/parse/unknown-decision error returns
  `DecideResponse{Decision: DecisionDeny}` with `Obligations == nil` and a non-nil
  `error`.
- `policy.AuditEmit(nil)` is always `false` (`internal/policy/obligation.go:53-63`),
  so `outcome.auditEmit` is always `false` on this path and
  `maybeEmitPolicyDecision` no-ops (`run.go:1515`). Zero audit trail is ever
  produced for a fail-closed deny caused by transport/parse failure.
- Both this path and a genuine `{"decision":"deny"}` response hit the same
  `default:` branch (`run.go:1065-1074`) and produce the byte-identical
  `"policy: decision denied"` reason. An operator cannot tell "the policy engine
  said no" from "the policy engine crashed", which changes what they should do
  next (change policy vs. debug the daemon).

**Reference:**
- `internal/runtime/run.go:1002-1075` (`decideGate`, the edit site)
- `internal/runtime/run.go:1509-1527` (`maybeEmitPolicyDecision`, extended or
  paired with a small sibling call for the unconditional case)
- `internal/policy/client.go:149-194` (`Decide`, its documented fail-closed
  contract, unmodified by this task)
- `internal/policy/obligation.go:50-63` (`AuditEmit`, unmodified)
- `internal/runtime/run_policy_audit_test.go` (the existing unit-test pattern this
  task's tests extend)
- `tests/e2e/policy_gate_e2e_test.go` (the fake-policy-engine e2e harness this
  task's L5 test extends)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-166-01 | `decideGate` captures the `Decide` error instead of discarding it. | must have |
| REQ-166-02 | A transport/parse failure produces a `gateOutcome.reason` distinct from the genuine-deny reason string. | must have |
| REQ-166-03 | A transport/parse failure with a configured audit sink emits an `ActionPolicyDecision` event unconditionally, `Detail.Reason` classifies it (e.g. `"policy_transport_error"`). | must have |
| REQ-166-04 | A genuine deny is byte-for-byte unaffected (same reason string, same `audit_emit`-gated emission). | must have |
| REQ-166-05 | A genuine allow/require_approval is byte-for-byte unaffected. | must have |
| REQ-166-06 | Pre-existing `internal/runtime` suites (073/160/161/162/164) pass unchanged. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/166-audit-policy-transport-failure-test-spec.md` exists (written first)
- [x] Task 073 merged (`gateOutcome`, `maybeEmitPolicyDecision`, `ActionPolicyDecision` exist)
- [ ] Task 164 merged (touches the same function; sequence after to avoid a
  conflict, though this task's edits sit in the pre-switch error capture and the
  `default:` deny branch, not the `DecisionAllow` branch task 164 edits)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/runtime/run.go`, `decideGate`: capture the error.
   ```go
   resp, decideErr := policy.NewClient(socketPath).Decide(req)
   ```
2. Compute obligations/tier/auditEmit exactly as today (unaffected: `resp` is
   still the fail-closed zero-obligation value on error, so `tier`/`auditEmit`
   stay empty/false on this path, unchanged).
3. Before the `switch resp.Decision` block, branch on `decideErr != nil`
   separately from the decision switch, since a transport failure needs BOTH a
   distinct reason AND unconditional emission (the decision switch's existing
   branches only vary the reason, they all reuse the same `auditEmit`-gated
   emission at the `Run` call site):
   ```go
   if decideErr != nil {
       return gateOutcome{
           allowed:          false,
           reason:           fmt.Sprintf("policy: decide call failed, fail-closed to deny: %v", decideErr),
           auditEmit:        true, // unconditional: no obligation can exist in a response that never arrived
           policyDecision:   string(policy.DecisionDeny),
           policyReason:     "policy_transport_error",
           transportFailure: true, // new field, see step 4
       }, nil
   }
   ```
   Do NOT include the raw `decideErr` string if it could ever carry secret
   material; `policy.Decide`'s documented contract already guarantees its errors
   never do (dial/timeout/parse errors only), so `%v` is safe here, matching
   `internal/vault`'s equivalent convention noted in `internal/vault/client.go:14-17`
   (compare, don't copy: vault's constraint is about secret VALUES, policy has no
   such payload).
4. Add a `transportFailure bool` field to `gateOutcome` and thread it into
   `maybeEmitPolicyDecision` (or a new small sibling, executor's choice) so the
   `Detail.Reason` on the emitted event is set to `outcome.policyReason`
   (`"policy_transport_error"`) specifically for this case, distinguishing it in
   the audit chain from a normal `ActionPolicyDecision` event (which carries no
   `Reason`, only `PolicyDecision`/`PolicyReason` from the engine). The simplest
   correct shape: `maybeEmitPolicyDecision` emits when `outcome.auditEmit ||
   outcome.transportFailure` (transport failure forces emission regardless of the
   normal `auditEmit` gate), and additionally sets `Detail.Reason =
   "policy_transport_error"` only when `outcome.transportFailure` is true (a
   normal event leaves `Detail.Reason` empty, unchanged from today).
5. Leave the `switch resp.Decision` block's three cases (`DecisionAllow`,
   `DecisionRequireApproval`, `default`) completely unmodified beyond task 164's
   own edit inside `DecisionAllow` (this task never reaches the switch on the
   transport-failure path, it returns earlier).
6. Add tests per the test spec.

## Acceptance criteria

- [ ] [REQ-166-01/02] TC-166-01/02: distinct reason string, pinned and e2e-proven.
- [ ] [REQ-166-03] TC-166-03/04: unconditional emission with classified Reason; nil sink no panic.
- [ ] [REQ-166-04] TC-166-05: genuine deny byte-for-byte unaffected.
- [ ] [REQ-166-05] TC-166-06: genuine allow/require_approval byte-for-byte unaffected.
- [ ] [REQ-166-06] TC-166-07: `go test -race -count=1 ./internal/runtime/... ./internal/policy/... ./tests/e2e/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, the existing fake-policy-engine e2e harness
  can drive a malformed daemon response through the real binary.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... -run TestTC166
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./tests/e2e/... -run TestPolicyGateTransportFailureAudited
  ```
  Expected: distinct halt wording naming the decide-call failure; zero box invocations.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/data-model.md`: the `audit.EventDetail.Reason` example value list
  gains `"policy_transport_error"`.
- `docs/spec/behaviors.md`: the policy-decide-gate entry notes a transport/parse
  failure is now distinguishable from a genuine deny in both the operator-facing
  halt message and the audit trail.

## Out of scope

- Retrying a failed `Decide` call.
- Any change to `policy.Decide`'s fail-closed contract or `internal/policy`'s leaf
  isolation (F-006 unaffected).
- Finer-grained failure classification (dial vs. timeout vs. parse) beyond one new
  `Reason` value.
- Task 164's `tier_select` validation (independent edit in the same function,
  sequenced but not combined).

## Dependencies

- **Blocks on:** task 073 (already merged), task 164 (sequenced to avoid a merge
  conflict in `decideGate`, not a functional dependency).
- **Blocks:** none.
