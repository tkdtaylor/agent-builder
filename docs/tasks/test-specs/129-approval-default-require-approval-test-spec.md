# Test Spec 129: Approval-default — AGENT_BUILDER_REQUIRE_APPROVAL

**Linked task:** [`docs/tasks/backlog/129-approval-default-require-approval.md`](../backlog/129-approval-default-require-approval.md)
**Written:** 2026-06-29
**ADR:** 056 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-129-01 | TC-129-01, TC-129-02 | ✅ |
| REQ-129-02 | TC-129-03         | ✅ |
| REQ-129-03 | TC-129-04         | ✅ |
| REQ-129-04 | TC-129-05         | ✅ |

## Test locations

- `internal/orchestrator/orchestrator_test.go` — TC-129-01, TC-129-02, TC-129-03
- `internal/cli/orchestrate_test.go` — TC-129-04, TC-129-05

Test function names:
- **TC-129-01:** `TestRequireApprovalDefaultIsTrueForPolicyAllow`
- **TC-129-02:** `TestRequireApprovalFalseAutoDispatchesOnPolicyAllow`
- **TC-129-03:** `TestRequireApprovalDoesNotAffectPolicyDeny`
- **TC-129-04:** `TestWithRequireApprovalOptionSetsField`
- **TC-129-05:** `TestAssembleOrchestrateReadsRequireApprovalEnv`

## Unit under test

`internal/orchestrator/orchestrator.go` — two changes:

1. `Orchestrator` gains a `requireApproval bool` field, defaulting to `true` when
   `New` is called without `WithRequireApproval(false)`. The new functional option
   `WithRequireApproval(bool) Option` sets the field.

2. In `ConfirmAndPlan` (task 128 extraction) / the existing `Handle` policy-decision
   branch: when policy returns `DecisionAllow`, the new code interposes:
   - If `o.requireApproval == true` → pause (the same `AwaitingApproval` path as
     `DecisionRequireApproval`, extracted into `pauseForApproval(ctx, plan)`).
   - If `o.requireApproval == false` → dispatch immediately (the existing
     `DecisionAllow` path, no change).
   The `pauseForApproval(ctx, plan)` helper is extracted from the pre-existing
   `DecisionRequireApproval` body — extraction, not rewrite.

`internal/cli/orchestrate.go` — reads `AGENT_BUILDER_REQUIRE_APPROVAL` from env;
lenient false values (`"false"`, `"0"`, `"no"`) → `WithRequireApproval(false)`;
any other value (including unset / empty) → default (`true`).

## Test cases

### TC-129-01: require-approval default (true) forces AwaitingApproval on policy-Allow plans

- **Requirement:** REQ-129-01
- **Setup:** construct `orchestrator.New(planner, policy, reporter, cfg)` — no
  `WithRequireApproval` option. The policy stub returns `DecisionAllow` for all
  `spawn-plan` decisions. Call `o.ConfirmAndPlan(ctx, goal)` (or `o.Handle` if task
  128 is not yet merged — the test uses whichever entry point is live at the time).
- **Expected:**
  - The orchestrator does NOT dispatch the plan immediately.
  - The registry state for the goal transitions to `StateAwaitingApproval`.
  - `reporter.Reported()` contains the approval solicitation (the `"Approve?"` text
    from `renderApprovalRequestWithInfo`). Assert `strings.Contains(reported[0], "Approve?")`.
  - The `PlanStore` contains the held plan (`o.HasPendingPlan(goalID) == true`).
  - `DispatchFunc` is NOT called (spy records zero calls).

### TC-129-02: require-approval=false auto-dispatches on policy-Allow (opt-out path)

- **Requirement:** REQ-129-01
- **Setup:** construct `orchestrator.New(planner, policy, reporter, cfg, orchestrator.WithRequireApproval(false))`.
  The policy stub returns `DecisionAllow`. Call `o.ConfirmAndPlan(ctx, goal)`.
- **Expected:**
  - `DispatchFunc` IS called (spy records exactly one call, one per sub-goal in the plan).
  - The registry state for the goal does NOT enter `StateAwaitingApproval` —
    it transitions directly to `StateDispatching`.
  - `reporter.Reported()` does NOT contain `"Approve?"` — no approval solicitation
    is sent when `requireApproval == false`.
  - `o.HasPendingPlan(goalID) == false` after `ConfirmAndPlan` returns (the plan was
    consumed by dispatch, not held).

### TC-129-03: policy-Deny is unaffected by the requireApproval field

- **Requirement:** REQ-129-02
- **Setup:** construct with `WithRequireApproval(false)`. The policy stub returns
  `DecisionDeny` for `spawn-plan`.
- **Expected:**
  - `DispatchFunc` is NOT called.
  - The reporter receives a denial message (not an approval solicitation).
  - `o.HasPendingPlan(goalID) == false` (no plan held on deny).
  This asserts that `requireApproval=false` does NOT auto-approve a policy-Deny —
  it only affects the approval pause for policy-Allow. The deny path is orthogonal.

### TC-129-04: WithRequireApproval functional option sets the field

- **Requirement:** REQ-129-03
- **Setup:** construct `orchestrator.New(planner, pol, reporter, cfg, orchestrator.WithRequireApproval(false))`.
- **Expected:**
  - The `requireApproval` field on the returned `*Orchestrator` is `false`.
  Assert via a package-level accessor or a white-box field read in an intra-package test.
  (If the field is unexported and no accessor is provided, prove via TC-129-02's
  observable side-effect — dispatch fires on Allow when `WithRequireApproval(false)` is
  set, confirming the field was applied. TC-129-04 may combine with TC-129-02 for this.)

### TC-129-05: assembleOrchestrate reads AGENT_BUILDER_REQUIRE_APPROVAL from env

- **Requirement:** REQ-129-04
- **Setup (sub-case A — unset):** call `assembleOrchestrate` (or equivalent) with
  `getenv("AGENT_BUILDER_REQUIRE_APPROVAL") == ""`. Assert the assembled orchestrator
  has `requireApproval == true` (default). Prove via TC-129-01's observable behavior
  (policy-Allow → AwaitingApproval).
- **Setup (sub-case B — false values):** test each of `"false"`, `"0"`, `"no"`.
  Assert the assembled orchestrator has `requireApproval == false` (opt-out). Prove
  via TC-129-02's observable behavior (policy-Allow → dispatch fires).
- **Setup (sub-case C — truthy values):** test `"true"`, `"1"`, `"yes"`, `"TRUE"`.
  Assert the assembled orchestrator has `requireApproval == true`. Prove via TC-129-01.
- **Expected (each sub-case):** the assembled orchestrator's behavior on `DecisionAllow`
  matches the env setting. Assert at least sub-cases A and B explicitly; sub-case C
  is a non-regression check.

## Post-implementation verification

- [ ] `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes
  with all five TCs non-vacuous (hard assertions on DispatchFunc call count,
  registry state, Reporter content — not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] `docs/spec/configuration.md` updated: `AGENT_BUILDER_REQUIRE_APPROVAL` documented
  (default `true`; lenient-false values opt out; effect: forces AwaitingApproval
  pause even on policy-Allow plans) in the same commit
- [ ] `docs/spec/behaviors.md` updated: the approval gate section notes that
  `requireApproval=true` (default) makes the AwaitingApproval pause apply to
  `DecisionAllow` plans, not just `DecisionRequireApproval` plans
- [ ] `pauseForApproval(ctx, plan)` is a named function (not inline code) extracted
  from the pre-existing `DecisionRequireApproval` body — the extraction is the
  "not rewrite" invariant (TC-129-01 observable outcome must be identical to the
  pre-existing `DecisionRequireApproval` path)

## Test framework notes

- Go `testing`. Reuse the `FakeReporter` (task 098), the spy `DispatchFunc` pattern
  from task 086, and the stub `PolicyClient` returning configurable decisions.
- TC-129-04 may be white-box (intra-package) if `requireApproval` is unexported and
  no accessor is added; otherwise use TC-129-02's observable side-effect.
- `pauseForApproval` extraction: the spec-verifier will check that the extracted body
  produces IDENTICAL observable output to the original `DecisionRequireApproval` path.
  Any difference is a regression.
- L5: `scripts/validate-orchestrate-intake.sh` extended to cover the
  approval-default path — the policy returns Allow, `AGENT_BUILDER_REQUIRE_APPROVAL`
  is unset (default true), and the assertion is that the approval solicitation line
  is emitted. L6: the operator observes the same in a Telegram round-trip.
