# Test Spec 123: Wire dispatchPlan → ReevaluateBlockedSpawn on live deny path

**Linked task:** [`docs/tasks/backlog/123-orchestrate-wire-blocked-reevaluation.md`](../backlog/123-orchestrate-wire-blocked-reevaluation.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 4 — end-to-end wiring; closes the dead-code gap left after task 121)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-123-01 | TC-001, TC-002    | ✅ |
| REQ-123-02 | TC-003            | ✅ |
| REQ-123-03 | TC-004            | ✅ |
| REQ-123-04 | TC-005            | ✅ |

## Test locations

All new tests land in `internal/orchestrator/wire_blocked_reevaluation_test.go`
(package `orchestrator`, black-box against exported/package-level behaviour).
The harness reuses fakes already established in `blocked_action_test.go`
(`denyingPolicy`, `nopReporter`, `memWriter`, `fixedPlanner`, `rerouter`,
`dispatchNoop`); new fakes needed for the status-writer seam are defined in the same
file.

- **TC-001** (blocked outcome drives ReevaluateBlockedSpawn — live call, no dead code):
  `TestDispatchPlanCallsReevaluateOnBlockedOutcome`
- **TC-002** (nil status writer → reevaluation is skipped, not panicked):
  `TestDispatchPlanNilStatusWriterSkipsReevaluation`
- **TC-003** (escalated outcome folded into PlanResult):
  `TestDispatchPlanFoldsEscalatedOutcomeIntoPlanResult`
- **TC-004** (resolved outcome folded into PlanResult, no escalation write):
  `TestDispatchPlanFoldsResolvedOutcomeIntoPlanResult`
- **TC-005** (`WithStatusWriter` functional option sets field; `assembleOrchestrate`
  builds a live writer from the task status writer already available on the orchestrate
  path):
  `TestWithStatusWriterSetsField` in `internal/orchestrator/` +
  `TestAssembleOrchestrateWiresStatusWriter` in `internal/cli/`

## Unit under test

`(*Orchestrator).dispatchPlan` (`internal/orchestrator/orchestrator.go`) — the
post-join aggregation loop that currently ignores `outcome.Blocked`. After wiring,
for each `outcome` where `outcome.Blocked != nil`, `dispatchPlan` must call
`o.ReevaluateBlockedSpawn(goal, *outcome.Blocked, bound, statusWriter)` and fold the
`loop.ReevaluationOutcome` into the `PlanResult` and `SubGoalOutcome`.

Supporting change: `(*Orchestrator)` gains a `statusWriter loop.StatusWriter` field
populated by a new `WithStatusWriter(loop.StatusWriter) Option` functional option,
mirroring `WithWorkerSemaphore`. The orchestrate CLI wiring (`internal/cli/orchestrate.go`)
must **construct** the status writer — it does not already hold one (verified: the only
`tasksource.NewStatusWriter` call site is `internal/runtime/run.go`, the worker path).
The executor builds `tasksource.NewStatusWriter(<base config>.TaskRoot,
tasksource.DefaultTaskDirs...)` on the orchestrate path; the returned
`*tasksource.StatusWriter` satisfies `loop.StatusWriter`.

## Test cases

### TC-001: a blocked outcome drives ReevaluateBlockedSpawn on the live path

- **Requirement:** REQ-123-01
- **Setup:** construct an `Orchestrator` with:
  - `fixedPlanner` — plan has one sub-goal with recipe `"coding-agent"` that the
    re-derived plan STILL needs (StillBlocked = true after replan)
  - `denyingPolicy` — denies `spawn-worker` for `"coding-agent"` so `dispatchOne`
    produces `outcome.Blocked != nil`
  - `WithStatusWriter(&memWriter{})` (spy status writer)
  - `WithDispatchFunc(dispatchNoop)`
  - `WithAuditSink(audit.NewFakeSink())`
  - a reevaluation bound of 1 (use the minimum non-zero bound so the test is fast)
  - `WithReevaluationBound(1)` (new option that supplies the bound; see REQ-123-03)
- **Action:** call `o.dispatchPlan(ctx, plan)`.
- **Expected:**
  - `memWriter.writes` has exactly one entry `"goal-1:needs-human"` — confirming
    `ReevaluateBlockedSpawn` was invoked (the dead-code gap is closed).
  - The returned `PlanResult` contains the blocked sub-goal's outcome with an
    attached `ReevaluationOutcome` field (see REQ-123-02) indicating escalation.
  - `dispatchPlan` returns no error (the reevaluation path is not a hard plan halt —
    it routes to escalation, not a fatal stop).

### TC-002: nil status writer means reevaluation is skipped, not panicked

- **Requirement:** REQ-123-01
- **Setup:** same denyingPolicy plan as TC-001 but `WithStatusWriter` is NOT called
  (field remains nil).
- **Action:** call `o.dispatchPlan(ctx, plan)`.
- **Expected:**
  - `dispatchPlan` does NOT panic.
  - No reevaluation write occurs (nil writer → no-op skip of the reevaluation call).
  - The blocked outcome is still present in `PlanResult.Outcomes[0].Blocked`.
  - `dispatchPlan` returns no error. Assert the nil-writer no-op is the documented
    behaviour (not an error return) — consistent with nil-registry being a no-op.

### TC-003: escalated reevaluation outcome folded into PlanResult

- **Requirement:** REQ-123-02
- **Setup:** same as TC-001 (fixedPlanner — still needs the denied recipe → escalation
  after bound).
- **Action:** call `o.dispatchPlan(ctx, plan)`.
- **Expected:**
  - `PlanResult.Outcomes[0]` carries a populated `ReevaluationOutcome` field
    (`Kind == loop.ReevaluationEscalated`).
  - `ReevaluationOutcome.Escalation.Blocked.Resource == "coding-agent"`.
  - `ReevaluationOutcome.Escalation.Reason()` contains the deny reason text.
  - Assert the Escalation field is non-zero (the human-facing information is not
    silently dropped).

### TC-004: resolved reevaluation outcome folded, no escalation write

- **Requirement:** REQ-123-02
- **Setup:** use `rerouter` as the planner — the re-derived plan does NOT need
  `"coding-agent"` (routes around), so `ReevaluateBlockedSpawn` resolves.
  `WithStatusWriter(&memWriter{})` is set.
- **Action:** call `o.dispatchPlan(ctx, plan)`.
- **Expected:**
  - `PlanResult.Outcomes[0].ReevaluationOutcome.Kind == loop.ReevaluationResolved`.
  - `memWriter.writes` is empty (no needs-human write on a resolved reevaluation).
  - `ReevaluationOutcome.AllowedResources` does NOT contain `"coding-agent"` (never-self-grant
    structural invariant preserved end-to-end through dispatchPlan).

### TC-005: WithStatusWriter functional option and CLI wiring

- **Requirement:** REQ-123-04
- **Part A** (`internal/orchestrator/`): construct `New(planner, policy, reporter, cfg, WithStatusWriter(w))`;
  assert `o.statusWriter == w` via a direct field read (white-box, since this is an
  intra-package test in `package orchestrator`) or via an observable side-effect
  (TC-001 already asserts the write — this test only checks the option wiring in
  isolation).
- **Part B** (`internal/cli/`): call `assembleOrchestrate` (or the equivalent config
  builder used in tests — the `orchestrateConfig` assembly path) with a stub task
  source that satisfies `loop.StatusWriter`. Assert the assembled `Orchestrator` was
  constructed with a non-nil status writer. The status writer supplied is a
  `tasksource.StatusWriter` the orchestrate path **constructs** via
  `tasksource.NewStatusWriter(<base config>.TaskRoot, tasksource.DefaultTaskDirs...)`
  (it is NOT pre-existing on this path — `run.go` is the only other construction site).
  If `assembleOrchestrate`
  is not directly testable, assert via a spy that the orchestrator would receive a
  non-nil writer when the path is configured with a real task-source.

## Post-implementation verification

- [ ] `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes
  with all five TCs non-vacuous (hard assertions, not smoke tests)
- [ ] `make check` passes (lint + fitness green)
- [ ] Cross-module trace explicitly asserted: `dispatchOne` deny → `outcome.Blocked
  != nil` (producer, pre-existing) → `dispatchPlan` post-join loop → `ReevaluateBlockedSpawn`
  call → `loop.ReevaluationPolicy.ReevaluateBlocked` → `memWriter.WriteStatus` (consumer,
  live). The `memWriter.writes` assertion in TC-001 is the load-bearing evidence.
- [ ] `SubGoalOutcome` struct carries a `ReevaluationOutcome loop.ReevaluationOutcome`
  field; the struct change is reflected in `docs/spec/data-model.md` in the same commit.
- [ ] `docs/spec/configuration.md` documents `AGENT_BUILDER_MAX_REEVALUATIONS` (the
  new env var that sets the reevaluation bound on the orchestrate path, defaulting to
  `AGENT_BUILDER_MAX_ATTEMPTS`).

## Test framework notes

- Go `testing`. Reuse fakes from `blocked_action_test.go` (same package).
- The reevaluation bound for TC-001/TC-003/TC-004 must come from a configured source
  (not a magic constant in dispatchPlan) — use `WithReevaluationBound(1)` (a new
  `int` field on `Orchestrator` set by the new option). Setting it to 1 keeps test
  runtime minimal while exercising the bound path.
- TC-002 (nil writer → skip) must be a genuine runtime check in `dispatchPlan`, not
  just an absence of a call. The skip must not surface as an error.
- L5/L6: reached by the end-to-end orchestrate run that exercises 118/119/120/121/122/123
  together — a goal whose plan needs a deployment-denied recipe → deny → blocked
  outcome → reevaluate (bound 1) → escalate, escalation text observed live. This is
  also what finally closes task 121's own L5/L6 gap (its consumer was unreachable
  before this task).
