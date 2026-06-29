# Test Spec 122: Feed the policy daemon the plan-scoped allow set

**Linked task:** [`docs/tasks/backlog/122-orchestrate-policy-plan-scoped-allow.md`](../backlog/122-orchestrate-policy-plan-scoped-allow.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 1, daemon side; split from task 118)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-122-01 | TC-001            | ⏳ |
| REQ-122-02 | TC-002, TC-003    | ⏳ |
| REQ-122-03 | TC-004            | ⏳ |

## Unit under test

The orchestrate policy wiring (`internal/cli/orchestrate.go` `policyClientFromEnv`)
and an effective-allow helper. Today the daemon starts with `.Allow` unset → `serve
--allow ""` → the allowlist evaluator denies every resource, including the plan's own
(`plan.GoalID`, recipe names, `task.ID`). This task feeds the daemon serving a plan's
decisions the plan-derived allow set (task 118 `Plan.AllowedResources()`), intersected
with an optional deployment base allow (`AGENT_BUILDER_POLICY_ALLOW`).

The pure helper under test: `effectiveAllow(plan, base) []string` →
`plan.AllowedResources()` when `base` is empty, else `plan.AllowedResources() ∩ base`.

## Test cases

### TC-001: daemon configured with the plan-derived allow set (no base)

- **Requirement:** REQ-122-01
- **Setup:** `AGENT_BUILDER_POLICY_ALLOW` unset; a plan with `AllowedResources()` = `{goal-1, coding-agent, goal-1-0}`. Use a fake daemon launcher recording the `--allow` argv (the pattern in `internal/policy` tests).
- **Expected:** the daemon serving this plan's decisions is launched with `--allow` containing exactly `{goal-1, coding-agent, goal-1-0}` (set equality, order-insensitive). The previously-empty allow is gone.

### TC-002: deployment base intersects (narrows) the plan-derived set

- **Requirement:** REQ-122-02
- **Setup:** `AGENT_BUILDER_POLICY_ALLOW="coding-agent,goal-7,other"`; plan `AllowedResources()` = `{goal-7, coding-agent, docs-fix, goal-7-0}`.
- **Expected:** `effectiveAllow` = the **intersection** = `{goal-7, coding-agent}` (deployment can only narrow; `other` is dropped because it is not in the plan, `docs-fix`/`goal-7-0` dropped because not in the base). Assert set equality.

### TC-003: empty/unset base means full plan-derived set (no narrowing)

- **Requirement:** REQ-122-02
- **Setup:** base = `""` (and separately, base = whitespace-only).
- **Expected:** `effectiveAllow` returns the full `plan.AllowedResources()` unchanged.

### TC-004: empty effective set is fail-closed (deny)

- **Requirement:** REQ-122-03
- **Setup:** a deployment base that shares **no** element with the plan-derived set (intersection empty), e.g. base = `"unrelated"`.
- **Expected:** `effectiveAllow` = `[]` (empty); the daemon is launched with an empty allow → denies all this plan's spawns (fail-closed). The orchestrator plan-gate (task 118) and the self-repo bright line remain in force regardless.

## Post-implementation verification

- [ ] `go test ./internal/cli/... ./internal/policy/...` passes
- [ ] `make check` passes
- [ ] `docs/spec/configuration.md` documents `AGENT_BUILDER_POLICY_ALLOW` (deployment base, intersects the plan-derived set) and the plan-derived allow behavior on orchestrate — updated in the same commit
- [ ] Cross-module trace: producer = `Plan.AllowedResources()` ∩ base; consumer = policy daemon `--allow`. Assert the daemon receives the derived set (TC-001), not just that the helper returns it.

## Test framework notes

- Go `testing`. Use the fake `policy-engine` launcher recording `--allow` (the
  `internal/policy` daemon-lifecycle test pattern). `effectiveAllow` is a pure function
  — table-driven set tests. Depends on task 118 (`Plan.AllowedResources()`).
- L5/L6: the end-to-end orchestrate run shows an in-plan goal flip from `Plan denied`
  to dispatched once the daemon is fed the plan-derived allow (with tasks 118/119/120).
