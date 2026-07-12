# Task 164: fail-closed validation of the `tier_select` obligation value

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add an exported tier allowlist to `internal/sandbox` and make `internal/runtime`'s
policy decide gate (`decideGate`) reject an `allow` decision whose `tier_select`
obligation names a tier agent-builder does not recognize, instead of silently
forwarding the unvalidated string to the exec-sandbox block.

## Context

**Deviation note.** This task ID was originally scoped by planning as "consume the
`tier_select` obligation" on the assumption nothing did. That assumption is stale:
tasks 072/073 (2026-06-19) already wire `tier_select`, `vault_injection_floor`, and
`audit_emit` end to end, proven live in `tests/e2e/policy_gate_e2e_test.go` and
tracked as **Adopted (L5)** in `docs/plans/roadmap.md`'s block-status table. This
task instead closes a real, narrow, residual gap found while verifying that area:
the tier VALUE a `tier_select` obligation carries is never validated before it
reaches the exec-sandbox block.

**The gap, with exact evidence:**
- `policy.TierSelect` (`internal/policy/obligation.go:38-48`) returns whatever
  string the policy engine sent, coerced only by a `string` type assertion (no
  allowlist).
- `decideGate`'s `DecisionAllow` branch (`internal/runtime/run.go:1046-1054`) passes
  that string straight through into `gateOutcome.tier`.
- `Run` assigns it to `sandboxBox.tier` (`internal/runtime/run.go:868`), which
  becomes `Request.Tier` (`internal/sandbox/run.go:26-32`).
- `sandbox.ValidateRequest` (`internal/sandbox/run.go:70-75`) checks only that the
  command is non-empty; it has no tier check.
- `execsandbox.run` (`internal/sandbox/execsandbox/run.go:281-283,369-370`) defaults
  an EMPTY tier to `"bubblewrap"` but otherwise forwards whatever string it was
  given straight into the wire request sent to the external exec-sandbox block
  binary.

Nothing in agent-builder's own trust boundary checks the value against the two
tiers it actually documents (`"bubblewrap"`, `"gvisor"`, doc comments at
`internal/sandbox/run.go:30,64`). A misconfigured or malformed policy-engine
response currently reaches the external block unfiltered. `docs/architecture/decisions/038-policy-engine-integration.md`
establishes fail-closed as the load-bearing posture for every other obligation on
this path (a `Decide` transport error already fails closed to deny); this task
extends the same posture to the VALUE of `tier_select`, not just its presence.

**Reference:**
- `internal/policy/obligation.go:35-48` (`TierSelect`, unchanged by this task)
- `internal/runtime/run.go:1002-1075` (`decideGate`, the edit site)
- `internal/runtime/run.go:1509-1527` (`maybeEmitPolicyDecision`, unchanged, but its
  behavior must be exercised correctly by the new halt path)
- `internal/sandbox/run.go:1-75` (`Request`, `ValidateRequest`, the addition site
  for the new constants/validator)
- `tests/e2e/policy_gate_e2e_test.go:89-117` (`TC-072-03`, the existing valid-tier
  e2e precedent this task's e2e test mirrors)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-164-01 | `internal/sandbox` exports `TierBubblewrap`, `TierGvisor` constants and `func ValidTier(tier string) bool`; `""`, `TierBubblewrap`, `TierGvisor` are valid, every other string is not. | must have |
| REQ-164-02 | `decideGate`'s `DecisionAllow` branch calls `sandbox.ValidTier(tier)` before returning an allowed outcome; an invalid tier flips the outcome to `allowed: false` with a reason naming the bad value. | must have |
| REQ-164-03 | The halted-on-invalid-tier outcome still carries `auditEmit`/`policyDecision`/`policyReason` so `maybeEmitPolicyDecision` (unmodified) emits an `ActionPolicyDecision` event distinguishing "engine allowed, obligation value was bad" from a genuine `deny`. | must have |
| REQ-164-04 | End-to-end: an unknown `tier_select` value never starts the box (zero exec-sandbox invocations); task marked `needs-human`. | must have |
| REQ-164-05 | Valid tier values (`""`, `"bubblewrap"`, `"gvisor"`) are byte-for-byte unaffected. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/164-sandbox-tier-select-fail-closed-test-spec.md` exists (written first)
- [x] Task 072/073 merged (`decideGate`, `gateOutcome`, `maybeEmitPolicyDecision`,
  and the `tests/e2e` fake-policy-engine harness already exist)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. `internal/sandbox/run.go` (or a new `internal/sandbox/tier.go` if preferred, no
   new imports either way): add
   ```go
   const (
       TierBubblewrap = "bubblewrap"
       TierGvisor     = "gvisor"
   )

   // ValidTier reports whether tier is a recognized exec-sandbox execution tier.
   // The empty string is valid (it means "backend default").
   func ValidTier(tier string) bool {
       switch tier {
       case "", TierBubblewrap, TierGvisor:
           return true
       default:
           return false
       }
   }
   ```
2. `internal/runtime/run.go`, inside `decideGate`'s `case policy.DecisionAllow:`
   branch (currently lines 1046-1054), insert the validation BEFORE constructing the
   allowed outcome:
   ```go
   case policy.DecisionAllow:
       if !sandbox.ValidTier(tier) {
           return gateOutcome{
               allowed:        false,
               reason:         fmt.Sprintf("policy: tier_select obligation names unknown tier %q", tier),
               auditEmit:      auditEmit,
               policyDecision: string(policy.DecisionAllow),
               policyReason:   policyReason,
           }, nil
       }
       return gateOutcome{
           allowed:        true,
           tier:           tier,
           auditEmit:      auditEmit,
           policyDecision: string(policy.DecisionAllow),
           policyReason:   policyReason,
       }, nil
   ```
   `internal/runtime` already imports `internal/sandbox` (the `wiring
   *sandbox.RunWiring` parameter), so no new import is needed. `fmt` is already
   imported in `run.go`.
3. Do not touch the `require_approval`/`default` (deny) branches, `maybeEmitPolicyDecision`,
   or anything downstream of `gateOutcome` (the halt-on-`!outcome.allowed` path at
   `run.go:847-856` already handles this new case correctly, unmodified, because it
   only branches on `outcome.allowed`).
4. Add `internal/sandbox/tier_test.go` (TC-164-01), extend
   `internal/runtime/run_164_test.go` (new file, TC-164-02/03), and extend
   `tests/e2e/policy_gate_e2e_test.go` (TC-164-04) per the test spec.

## Acceptance criteria

- [ ] [REQ-164-01] TC-164-01: `ValidTier` table test over valid/invalid/case/alias/whitespace inputs.
- [ ] [REQ-164-02] TC-164-02: pinned `gateOutcome` shapes for valid vs. invalid tier.
- [ ] [REQ-164-03] TC-164-03: `maybeEmitPolicyDecision` on the halted outcome emits `PolicyDecision == "allow"`.
- [ ] [REQ-164-04] TC-164-04: e2e, unknown tier halts with zero block invocations, task marked needs-human.
- [ ] [REQ-164-05] TC-164-05: existing `TC-072-03` e2e case passes unchanged.
- [ ] TC-164-06: `go test -race -count=1 ./internal/sandbox/... ./internal/runtime/... ./tests/e2e/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L5, real `agent-builder` binary + fake policy-engine
  + fake exec-sandbox block (the harness `tests/e2e/policy_gate_e2e_test.go` already
  provides). No L6 needed; the mechanism is a string-allowlist comparison, fully
  exercised at L5.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/sandbox/... ./internal/runtime/... -run 'TestValidTier|TestTC164'
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./tests/e2e/... -run 'TestPolicyGateFakeBinaryE2E|TestPolicyGateUnknownTierHalts'
  ```
  Expected: new unknown-tier case halts with zero block invocations; pre-existing
  `TC-072-03`/`TC-072-06C` pass unchanged.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/behaviors.md`: the policy-decide-gate behavior entry (grep
  `tier_select`) gains a sentence noting an unknown tier value is treated as a
  fail-closed halt, not forwarded.
- `docs/spec/interfaces.md`: the `internal/sandbox` seam section gains
  `TierBubblewrap`/`TierGvisor`/`ValidTier` to its documented exported surface.

## Out of scope

- `vault_injection_floor` or `audit_emit` obligation handling (task 166 covers a
  distinct residual gap in the same file; unrelated logic).
- Validating tier values inside the external exec-sandbox block's own wire
  protocol, that is a separate trust boundary with its own contract.
- Adding new tier values (Kata/Firecracker); this task validates against the
  current known set only.

## Dependencies

- **Blocks on:** task 072/073 (already merged).
- **Blocks:** none.
