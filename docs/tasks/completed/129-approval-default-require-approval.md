# Task 129: Approval-default — AGENT_BUILDER_REQUIRE_APPROVAL

**Project:** agent-builder · **Created:** 2026-06-29 · **Status:** backlog
**ADR:** 056 — Conversational human-gated orchestrate front door
**Test spec:** [129-approval-default-require-approval-test-spec.md](../test-specs/129-approval-default-require-approval-test-spec.md)

## Goal

Introduce `AGENT_BUILDER_REQUIRE_APPROVAL` (default `true`): when true, the
orchestrator forces the `AwaitingApproval` pause on `DecisionAllow` plans (the same
pause already used for `DecisionRequireApproval`). Extract the existing pause body
into `pauseForApproval(ctx, plan)`. When false, the existing `DecisionAllow`
auto-dispatch path is restored. Update `docs/spec/configuration.md` and
`docs/spec/behaviors.md`.

## Context

Today the orchestrator auto-dispatches when policy returns `DecisionAllow`. ADR 056
requires a human gate by default: even a plan that policy allows should pause for
human approval unless the operator explicitly opts out with
`AGENT_BUILDER_REQUIRE_APPROVAL=false`. This is orthogonal to policy: the policy
decision governs WHAT is allowed; `requireApproval` governs WHETHER a human sees the
plan before execution.

The implementation extracts the pre-existing `DecisionRequireApproval` pause body
into a named helper `pauseForApproval(ctx, plan)`, then calls it from the
`DecisionAllow` branch when `o.requireApproval == true`. This is an extraction, not a
rewrite — the pause body (approval solicitation, plan holding, Resume/ResumeWithFold)
is identical for both triggers.

## Requirements

| Req ID     | Description | Priority |
|------------|-------------|----------|
| REQ-129-01 | `Orchestrator` has a `requireApproval bool` field (default `true` in `New`, set by `WithRequireApproval(bool) Option`). When true and policy returns `DecisionAllow`, `ConfirmAndPlan` (or `Handle`) calls `pauseForApproval` instead of dispatching immediately. When false, `DecisionAllow` dispatches as before. | must have |
| REQ-129-02 | `requireApproval` does NOT affect the `DecisionDeny` path — a policy-deny is still a denial regardless of the field value. It also does NOT affect `DecisionRequireApproval` (which always pauses). | must have |
| REQ-129-03 | `pauseForApproval(ctx, plan)` is extracted from the pre-existing `DecisionRequireApproval` body — same behavior, named helper, no logic change. | must have |
| REQ-129-04 | `internal/cli/orchestrate.go` reads `AGENT_BUILDER_REQUIRE_APPROVAL` from env. Lenient-false values (`"false"`, `"0"`, `"no"`, case-insensitive) → `WithRequireApproval(false)`. Any other value (including unset/empty) → default (`true`). | must have |

## Acceptance criteria

1. `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes;
   all five TCs non-vacuous (hard assertions on DispatchFunc call count, registry
   state, reporter content).
2. TC-129-01: with default constructor (no `WithRequireApproval`), policy-Allow →
   `StateAwaitingApproval`, reporter contains `"Approve?"`, `DispatchFunc` call
   count == 0. Hard assertions.
3. TC-129-02: with `WithRequireApproval(false)`, policy-Allow → `DispatchFunc` called,
   no `"Approve?"` in reporter. Hard assertions.
4. TC-129-03: with `WithRequireApproval(false)`, policy-Deny → `DispatchFunc` NOT
   called. Hard assertion.
5. TC-129-05: `AGENT_BUILDER_REQUIRE_APPROVAL=false` (and `"0"`, `"no"`) → assembled
   orchestrator auto-dispatches on Allow. Hard assertion.
6. `docs/spec/configuration.md` updated: `AGENT_BUILDER_REQUIRE_APPROVAL` documented
   (default true, lenient-false values, effect) in the same commit.
7. `docs/spec/behaviors.md` updated: approval gate section notes that
   `requireApproval=true` extends the AwaitingApproval pause to `DecisionAllow` plans.
8. `make check` passes.
9. `git status` clean on commit.

## Files changed

- `internal/orchestrator/orchestrator.go` — `requireApproval` field, `WithRequireApproval` option, `pauseForApproval` extraction, `DecisionAllow` branch interposed.
- `internal/cli/orchestrate.go` — `EnvRequireApproval` constant, env-var read, `WithRequireApproval` passed to `New`.
- `internal/orchestrator/orchestrator_test.go` — TC-129-01, TC-129-02, TC-129-03.
- `internal/cli/orchestrate_test.go` — TC-129-04, TC-129-05.
- `docs/spec/configuration.md`, `docs/spec/behaviors.md`.

## Verification plan

**L2/L3:**
`go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` — all five TCs
pass with hard assertions.

`make check` — lint + build + fitness green.

**L5 (scripted):**
`scripts/validate-orchestrate-intake.sh` extended to assert that with
`AGENT_BUILDER_REQUIRE_APPROVAL` unset (default true) and a policy-Allow plan, the
approval solicitation line is emitted. With `AGENT_BUILDER_REQUIRE_APPROVAL=false`,
dispatch fires without an approval line.

**L6 (operator-observed):**
The full Telegram round-trip (task 128 L6) exercises the approval gate: `requireApproval=true`
means the operator sees the plan before it runs.

## Dependencies

- Task 128 (the intake state machine must be merged; `ConfirmAndPlan` is the entry
  point where the `DecisionAllow` interposition happens).
- Tasks 124–127 must be merged.
- Task 123 merged to `main`.

## Out of scope

- Escalation over the channel (task 130).
- LLM clarifier (task 131).
- Any change to the `DecisionDeny` or `DecisionRequireApproval` paths (they are
  unaffected by `requireApproval`).
