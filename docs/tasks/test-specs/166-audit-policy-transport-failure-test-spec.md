# Test Spec 166: audit and distinguish a policy `Decide` transport/parse failure from a genuine deny

**Linked task:** [`docs/tasks/backlog/166-audit-policy-transport-failure.md`](../backlog/166-audit-policy-transport-failure.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

**Deviation note.** This task ID was originally scoped by planning to "emit an
audit event on every policy decide, satisfying the audit_emit obligation." That is
already fully built: task 073 wires `audit_emit` end to end
(`maybeEmitPolicyDecision`, `internal/runtime/run.go:1509-1527`), proven at
`internal/runtime/run_policy_audit_test.go` and tracked **Adopted (L5)** in
`docs/plans/roadmap.md`. This task ID is repurposed for a real, narrow gap found
while verifying that area: the audit_emit-driven event can never fire for the ONE
decide outcome that most needs an audit trail, a transport/parse failure that
fails closed to deny.

`decideGate` (`internal/runtime/run.go:1035-1037`) calls:
```go
resp, _ := policy.NewClient(socketPath).Decide(req)
```
The `error` return is discarded. `policy.Decide`
(`internal/policy/client.go:156-194`) is documented as fail-closed: any dial
error, timeout, malformed JSON, or unknown decision value returns
`DecideResponse{Decision: DecisionDeny}` with a non-nil `error` and, critically,
`Obligations == nil`. Back in `decideGate`, `policy.AuditEmit(nil)` is always
`false` (`internal/policy/obligation.go:53-63` iterates an empty/nil slice), so
`outcome.auditEmit` is always `false` on this path, `maybeEmitPolicyDecision`
no-ops (`!outcome.auditEmit` short-circuits at `run.go:1515`), and **zero** audit
trail is ever produced for a fail-closed deny caused by the policy engine being
unreachable, timing out, or returning garbage. Worse, the reason string is
identical to a genuine policy-authored deny: both the transport-error path and a
real `{"decision":"deny"}` response hit the exact same `default:` branch
(`run.go:1065-1074`) and produce the exact same `"policy: decision denied"`
reason. An operator reading a `needs-human` status or an audit chain today cannot
tell "the policy engine actively said no" from "the policy engine crashed and we
correctly failed closed", which matters operationally (the first needs a policy
change, the second needs the policy-engine daemon debugged).

**Module boundary touched:** `internal/runtime` only (`decideGate`,
`gateOutcome`). No change to `internal/policy` (its fail-closed contract is
already correct and unmodified by this task) or `internal/audit` (no new
`AuditAction`, this task reuses the existing `ActionPolicyDecision`/`EventDetail.Reason`
fields).

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-166-01 | `decideGate` captures the `Decide` call's error instead of discarding it. | TC-166-01 |
| REQ-166-02 | When `Decide` returns a non-nil error (transport/parse/unknown-decision failure), the returned `gateOutcome.reason` is a string distinct from the genuine-deny reason (e.g. contains `"decide call failed"` rather than `"decision denied"`), so the two cases are never string-identical. | TC-166-01, TC-166-02 |
| REQ-166-03 | When a transport/parse failure occurs AND an `audit.Sink` is configured, an `ActionPolicyDecision` event is emitted UNCONDITIONALLY (independent of `audit_emit`, since no obligation can exist in a response that never arrived), with `Detail.Reason` set to a classification string (e.g. `"policy_transport_error"`) distinguishing it from an obligation-driven emission. | TC-166-03, TC-166-04 |
| REQ-166-04 | A genuine `{"decision":"deny"}` response (no transport error) is completely unaffected: same reason string, same `audit_emit`-gated (not unconditional) emission behavior as before this task. | TC-166-05 |
| REQ-166-05 | A genuine `{"decision":"allow"}`/`{"decision":"require_approval"}` response is completely unaffected. | TC-166-06 |
| REQ-166-06 | Pre-existing `internal/runtime` suites (including tasks 073/160/161/162/164's) continue to pass unchanged. | TC-166-07 |

---

## Pre-implementation checklist

- [x] Task 073 merged (`gateOutcome`, `maybeEmitPolicyDecision`, `ActionPolicyDecision` exist)
- [x] Task 164 merged or in flight (this task edits the same `decideGate` function;
  sequence after 164 to avoid a merge conflict inside the `DecisionAllow` branch,
  though this task's own edits are in the pre-switch error-capture and the
  `default:` deny branch, not the `DecisionAllow` branch task 164 touches)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-166-01, transport error produces a distinct reason string

- **Requirement:** REQ-166-01, REQ-166-02
- **Level:** L2 (unit test, table pin on the routing contract, mirrors
  `TestRequireApprovalStatusReason` in `run_policy_audit_test.go`)
- **Test file:** `internal/runtime/run_166_test.go` (new)

**Step:** Pin the two `gateOutcome` shapes `decideGate` must now produce: (a) a
genuine deny (`Decide` returns `(DecideResponse{Decision: DecisionDeny}, nil)`,
no error) and (b) a transport failure (`Decide` returns
`(DecideResponse{Decision: DecisionDeny}, someTransportErr)`).

**Expected output:** (a)'s `reason == "policy: decision denied"` (byte-identical
to pre-task); (b)'s `reason` is a DIFFERENT string containing `"decide call
failed"` (or equivalent, executor's exact wording is free as long as it is
distinct and descriptive); `reason(a) != reason(b)` asserted directly.

---

### TC-166-02, the distinct reason surfaces in the halt message

- **Requirement:** REQ-166-02
- **Level:** L5 (e2e, extends `tests/e2e/policy_gate_e2e_test.go`'s existing
  fake-policy-engine harness)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go` (extend)

**Setup:** Configure the fake policy-engine binary to exit non-zero / write
unparseable output instead of a valid `FAKE_POLICY_RESPONSE` (the harness already
supports this: `buildFakePolicyEngine` reads `FAKE_POLICY_RESPONSE` and writes it
verbatim to its socket response; set it to `not valid json` to trigger a parse
failure on the `agent-builder` side).

**Step:** `runAgentBuilder(t, binary, env, "run")`.

**Expected output:** exit code `0` (still a terminal halt, not a process crash);
stdout contains `"run halted"` and the new distinct wording (e.g. `"decide call
failed"`), NOT the plain `"policy: decision denied"` string a genuine deny
produces; task file marked `needs-human`; box never started (mirrors
`assertNoPublishLog`).

---

### TC-166-03, transport failure with an audit sink emits an event unconditionally

- **Requirement:** REQ-166-03
- **Level:** L2 (unit test, extends `maybeEmitPolicyDecision`'s existing suite
  pattern; this task either extends `maybeEmitPolicyDecision` itself to accept a
  "transport failure" signal, or introduces a small sibling helper, executor's
  choice, both are acceptable as long as the call site in `decideGate`/`Run`
  guarantees the event fires)
- **Test file:** `internal/runtime/run_166_test.go`

**Step:** Simulate a transport-failure `gateOutcome` (per TC-166-01's shape (b))
with a fresh `audit.NewFakeSink()`, and drive it through whatever emission call
`decideGate`'s caller now makes for this case.

**Expected output:** exactly one `ActionPolicyDecision` event in the sink, with
`Detail.Reason == "policy_transport_error"` (or the executor's chosen
classification string, must be a NEW value distinct from any existing `Reason`
constant used elsewhere in the codebase, e.g. `"role_mismatch"`,
`"armor_blocked"`) and `Detail.PolicyDecision == "deny"` (the fail-closed
decision). This event fires even though no `audit_emit` obligation could possibly
be present (the response never arrived).

---

### TC-166-04, nil sink on transport failure does not panic

- **Requirement:** REQ-166-03
- **Level:** L2

**Step:** Same transport-failure scenario as TC-166-03, but with a `nil`
`audit.Sink`.

**Expected output:** no panic; the halt still proceeds correctly (mirrors the
existing `TC-073-04_nil_sink_no_panic` convention).

---

### TC-166-05, genuine deny is completely unaffected

- **Requirement:** REQ-166-04
- **Level:** L2 (regression)

**Step:** Re-run `TestPolicyGateAuditEmitWithDenyDecision`'s
`TC-073-04_deny_audit_emit_emits_event` sub-test and
`TestRequireApprovalStatusReason`'s deny case unmodified.

**Expected output:** byte-identical pass; `reason == "policy: decision denied"`;
emission still gated on `audit_emit` being present in the (real) response, NOT
unconditional (a genuine deny with no `audit_emit` obligation still emits nothing,
exactly as before this task).

---

### TC-166-06, genuine allow/require_approval unaffected

- **Requirement:** REQ-166-05
- **Level:** L2 (regression)

**Step:** Re-run `TestPolicyGateAuditEmitObligation` and the
`TC-073-04_require_approval_audit_emit_emits_event` sub-test unmodified.

**Expected output:** byte-identical pass.

---

### TC-166-07, full regression

- **Requirement:** REQ-166-06
- **Level:** L2/L3/L5

**Step:**
```
go test -race -count=1 ./internal/runtime/... ./internal/policy/... ./tests/e2e/...
make check
```

**Expected output:** all packages `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, the fake-policy-engine e2e harness can drive a
  real malformed/unreachable-daemon response through the real binary
  (`tests/e2e/policy_gate_e2e_test.go`'s existing pattern, extended).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... -run TestTC166
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./tests/e2e/... -run TestPolicyGateTransportFailureAudited
  ```
  Expected: distinct halt wording, task marked needs-human, zero box invocations.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Retrying a failed `Decide` call (still a single fail-closed attempt, matching the
  existing contract; retry policy is a separate, unscoped concern).
- Any change to `policy.Decide`'s own fail-closed contract or `internal/policy`'s
  leaf isolation (F-006 unaffected, no new import).
- Classifying WHICH kind of transport failure occurred (dial vs. timeout vs. parse)
  beyond a single new `Reason` value; finer classification is a follow-on if an
  operator need is demonstrated.
