# Test Spec 118: Plan-derived authorization on orchestrate

**Linked task:** [`docs/tasks/backlog/118-orchestrate-plan-derived-authz.md`](../backlog/118-orchestrate-plan-derived-authz.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md)

## Requirements coverage

| Req ID      | Test cases            | Covered? |
|-------------|-----------------------|----------|
| REQ-118-01  | TC-001, TC-002        | ⏳ |
| REQ-118-02  | TC-003, TC-004        | ⏳ |
| REQ-118-03  | TC-005                | ⏳ |
| REQ-118-04  | TC-006, TC-007        | ⏳ |

## Unit under test

`internal/orchestrator` — the plan-derived authorization gate that scopes the
orchestrator's spawn decisions to exactly the resources its current plan declares,
then defers to the policy engine for the independent deployment decision.

New surface:
- `func (p Plan) AllowedResources() []string` — the deduped, deterministically
  ordered set of resource IDs the plan authorizes: `{p.GoalID}` ∪ `{sub.RecipeName
  for each sub}` ∪ `{sub.Task.ID for each sub}`.
- The orchestrator's `decideSpawn` / `decideSpawnWorker` consult the plan-derived set
  **before** the policy engine: a resource not in the set is denied without consulting
  policy (least-privilege; the plan can only authorize its own resources). A resource
  in the set proceeds to `o.policy.Decide`; the effective decision is
  `plan-allows ∧ policy-allows`.

## Test cases

### TC-001: AllowedResources derives the plan's resource set

- **Requirement:** REQ-118-01
- **Input:** a `Plan{GoalID: "goal-1", SubGoals: [{RecipeName: "coding-agent", Task: {ID: "goal-1-0"}}, {RecipeName: "docs-fix", Task: {ID: "goal-1-1"}}]}`.
- **Expected:** `AllowedResources()` returns exactly `["goal-1", "coding-agent", "docs-fix", "goal-1-0", "goal-1-1"]` as a set (order deterministic; assert via set membership + length 5). No duplicates.

### TC-002: AllowedResources dedups repeated recipe names

- **Requirement:** REQ-118-01
- **Input:** a plan with two sub-goals both on `coding-agent`, task IDs `goal-2-0`, `goal-2-1`.
- **Expected:** the set contains `coding-agent` exactly once; length 4 (`goal-2`, `coding-agent`, `goal-2-0`, `goal-2-1`).

### TC-003: in-plan spawn-plan resource proceeds to policy and is allowed

- **Requirement:** REQ-118-02
- **Setup:** orchestrator with a **fake PolicyClient** that records the request and returns `allow`. Plan with GoalID `goal-3`.
- **Expected:** `decideSpawn(plan)` returns `DecisionAllow`; the fake recorded a decide call whose `Resource.ID == "goal-3"`. (The plan-derived gate let the in-plan resource through to policy.)

### TC-004: out-of-plan resource is denied without consulting policy

- **Requirement:** REQ-118-02
- **Setup:** fake PolicyClient that records calls and would return `allow`. Drive a spawn-worker decision for a `SubGoal` whose `RecipeName`/`Task.ID` are **not** in the plan's `AllowedResources()` (simulate an injected/foreign sub-goal not belonging to the plan).
- **Expected:** the decision is `DecisionDeny`; the fake PolicyClient recorded **zero** calls for that resource (the plan-derived gate short-circuited before policy). Reason names the out-of-plan resource.

### TC-005: effective decision is plan-allows ∧ policy-allows

- **Requirement:** REQ-118-03
- **Setup:** in-plan resource (passes the plan gate), fake PolicyClient returns `deny`.
- **Expected:** the effective decision is `DecisionDeny` — an in-plan resource the deployment policy denies is still denied (the policy engine remains the independent ceiling). Symmetrically, in-plan + policy `allow` → `DecisionAllow` (TC-003).

### TC-006: plan-derived allow set is what the policy daemon is configured with

- **Requirement:** REQ-118-04
- **Setup:** the CLI policy wiring (`internal/cli`) given a plan and **no** deployment base allow (env unset). Capture the allow set handed to the policy daemon for that plan's decisions (via the daemon-config seam / a fake daemon launcher recording `--allow`).
- **Expected:** the configured allow set equals `plan.AllowedResources()` (the daemon is fed the plan-derived resources — fixing the empty-allowlist denial).

### TC-007: deployment base allow intersects the plan-derived set

- **Requirement:** REQ-118-04
- **Setup:** deployment base allow (env `AGENT_BUILDER_POLICY_ALLOW`) = `"coding-agent,goal-7"`. Plan `AllowedResources()` = `{goal-7, coding-agent, docs-fix, goal-7-0, goal-7-1}`.
- **Expected:** the configured daemon allow = the **intersection** = `{goal-7, coding-agent}` (a deployment can only narrow, never widen, the plan-derived set). When the env is unset/empty, the effective allow is the full plan-derived set (no narrowing).

## Post-implementation verification

- [ ] `go test ./internal/orchestrator/... ./internal/cli/...` passes
- [ ] `make check` passes (build, vet, test, gofmt, golangci-lint, scanners)
- [ ] No regression in existing orchestrate tests (the empty-allow deny path is replaced by plan-derived allow; update any test asserting the old empty-allow behavior, rewriting in place)

## Test framework notes

- Go `testing`. Use a fake `orchestrator.PolicyClient` (records requests, returns a
  scripted `DecideResponse`) — the pattern already used in `internal/orchestrator`
  tests. For TC-006/007, use the existing fake-daemon / launcher recording pattern in
  `internal/policy` tests (a fake `policy-engine` shell script capturing `--allow`).
- No live `policy-engine` binary required for these unit tests; the live allow→dispatch
  path is exercised at L5/L6 in the end-to-end orchestrate run (task 121 / verification).
