# Test spec — Task 081: Orchestrator core

**Linked task:** `docs/tasks/backlog/081-orchestrator-core.md`
**Written:** 2026-06-27
**Status:** stub — blocked by Cluster A (tasks 076–079) and task 080

## Context

The orchestrator is a new layer ABOVE the existing `supervisor`/`runtime` stack.
It accepts a goal (from the channel adapter, task 080), decomposes it into a plan,
presents the plan for human approval (via policy-engine `require_approval`), and —
only on approval — dispatches purpose-built worker agents by selecting and
parameterizing recipes. It aggregates results and reports back through the channel.

**The orchestrator itself authors no code.** It is a consumer of the recipe seam.
It coordinates workers; it does not become a worker.

This task is **blocked by Cluster A** (recipe seam must be stable) and **task 080**
(channel adapter must exist before the orchestrator can receive goals).

**Detailed task shape is deferred** pending those prerequisites. Shape parameters
that are known now:
- New package `internal/orchestrator` — goal intake, plan decomposition, approval
  gate, dispatch, aggregation, reporting.
- No new executor, no new gate, no new publisher — the orchestrator reuses existing
  recipe seams for its workers.
- Human-approval gate is wired as `policy-engine` `require_approval` obligation
  (the mechanism already exists from task 073).
- The orchestrator is NOT a `Recipe` itself; it is the layer that selects and
  dispatches recipes.

## Requirements coverage (preliminary)

| Req ID     | Description                                                           | Test cases |
|------------|-----------------------------------------------------------------------|------------|
| REQ-081-01 | Goal intake from GoalSource; plan decomposed into sub-goals           | TC-081-01  |
| REQ-081-02 | Plan requires human approval before any worker is dispatched          | TC-081-02  |
| REQ-081-03 | Approval obtained → worker dispatched via recipe seam                 | TC-081-03  |
| REQ-081-04 | Worker results aggregated and reported back through channel           | TC-081-04  |
| REQ-081-05 | Orchestrator itself authors no code (invariant)                       | TC-081-05  |
| REQ-081-06 | Orchestrator is a new layer above supervisor; supervisor not modified  | TC-081-06  |

## Pre-implementation checklist

- [ ] Task 076–079 merged (recipe seam stable)
- [ ] Task 080 merged (channel adapter + GoalSource interface)
- [ ] Decomposition strategy for goals (LLM-driven or rule-based?) decided
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-081-01 — Goal arrives, plan is decomposed into sub-goals

- **Requirement:** REQ-081-01
- **Level:** L2 (unit test with stub GoalSource)
- **Status:** stub

**Input:** A goal string "Fix the 3 broken links in docs/spec/".

**Expected output:**
- The orchestrator produces a `Plan` with at least one sub-goal.
- The plan is surfaced to the approval gate before dispatch.

---

### TC-081-02 — Plan is not dispatched without human approval

- **Requirement:** REQ-081-02
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Stub policy-engine returns `require_approval` for any spawn action.

**Expected output:**
- No worker is dispatched.
- The orchestrator reports the plan is pending approval via the channel.

---

### TC-081-03 — Approved plan dispatches workers via recipe seam

- **Requirement:** REQ-081-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Policy-engine stub returns `allow` for the spawn action.

**Expected output:**
- `recipe.SelectRecipe(name)` is called for each sub-goal.
- A worker supervisor is constructed and started for each.
- No inline code generation occurs in the orchestrator.

---

### TC-081-04 — Worker results are aggregated and reported

- **Requirement:** REQ-081-04
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** Two workers complete — one success, one failure.

**Expected output:**
- The orchestrator aggregates both outcomes.
- Reports a summary through the channel adapter's output path.

---

### TC-081-05 — Orchestrator contains no executor/code-authoring logic (invariant)

- **Requirement:** REQ-081-05
- **Level:** L3 (import-graph)
- **Status:** stub

**Input:** `go list -deps ./internal/orchestrator/...`

**Expected output:**
- No import of `internal/executor` (the Claude CLI adapter).
- No import of `internal/gate` concrete steps.
- Orchestrator reaches recipe seam via `internal/recipe` only.

---

### TC-081-06 — supervisor package is unchanged by this task

- **Requirement:** REQ-081-06
- **Level:** L2 (structural diff)
- **Status:** stub

**Input:** `git diff HEAD~1 -- internal/supervisor/`

**Expected output:** Empty diff. The orchestrator is purely additive.

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 (unit tests with stubbed channel and policy).
  An L5 end-to-end orchestrator run requires task 082 (agent-mesh) and task 083
  (memory-guard); those are blocked by this task.
- **L2 harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/orchestrator/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/orchestrator`

## Open questions

1. Goal decomposition strategy: rule-based (pattern-match on goal string) or
   LLM-assisted (a small LLM call inside the orchestrator)? The ADR does not
   specify; this affects whether the orchestrator imports an executor interface.
2. What is the reporting format back through the channel adapter? Plain text,
   structured JSON, or a typed `Result` type?
3. Does the orchestrator persist plan state between restarts (pre-memory-guard:
   in-memory only; post-memory-guard: durable)? The initial version may be
   in-memory only, with memory-guard wired in task 083.

## Out of scope

- agent-mesh transport for worker dispatch (task 082).
- memory-guard on orchestrator state (task 083).
- Orchestrator self-containment + policy gating (task 084).
- Multi-worker concurrent dispatch (task 085).
