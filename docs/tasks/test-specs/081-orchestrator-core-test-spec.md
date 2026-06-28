# Test spec — Task 081: Orchestrator core

**Linked task:** `docs/tasks/backlog/081-orchestrator-core.md`
**Governing ADR:** `docs/architecture/decisions/046-orchestrator-core-decomposition-and-dispatch.md`
(extends ADR 042)
**Written:** 2026-06-27 (stub) — **expanded:** 2026-06-28
**Status:** active — prerequisites 076–080, 092/093/095, 096, 098 merged

## Context

The orchestrator is a new package `internal/orchestrator` — a Tier-1 layer ABOVE the
existing `supervisor`/`runtime` stack (ADR 042). It accepts a goal (a
`supervisor.Task` carried in by the inbound channel, task 080), decomposes it into a
plan via a `Planner` seam, gates the plan on human approval (`policy.Decide` with the
`spawn-plan` action), and — only on allow / approval — dispatches one worker per
sub-goal by reusing `runtime.Run`. It aggregates per-sub-goal outcomes into a typed
`PlanResult` and reports them over the outbound `supervisor.Reporter` seam (task 098).

**The orchestrator authors no code.** It is a consumer of the recipe seam; it
coordinates workers, it does not become one. It must never DIRECTLY import
`internal/executor`.

### Decisions taken in this implementation (ADR 046, autonomous defaults noted)

- **Planner seam + `StructuredPlanner` v1 (ADR 046 §1 Option A).** `Planner.Plan(goal
  supervisor.Task) (Plan, error)`. `StructuredPlanner` parses a structured plan from
  the goal text if present, else collapses the whole goal into a single sub-goal on
  the default recipe. **No LLM, no `internal/executor` import.** The `LLMPlanner` is an
  explicit follow-on (ADR 046 §6) and is NOT built here.
- **Structured-plan text format (autonomous default):** a line-list where each
  non-blank, non-comment line is one sub-goal of the form `<recipe-name>: <spec text>`.
  A line with no `<recipe>:` prefix uses the default recipe. Lines beginning with `#`
  are comments. A goal whose text contains no parseable sub-goal lines collapses to a
  single sub-goal (whole goal text → default recipe). This keeps decomposition fully
  deterministic (every TC is a hard assertion).
- **Default recipe name (autonomous default):** `"coding-agent"` (the recipe runtime
  registers at init).
- **Policy action name (ADR 046 §4, autonomous default):** `"spawn-plan"`, distinct
  from the worker's `"run-task"`.
- **Typed `PlanResult` rendered to text only at the Reporter boundary (ADR 046 §2).**
- **In-memory plan state behind a small `PlanStore` interface (ADR 046 §3)** so task
  084 can swap a durable/memory-guarded backend.
- **Dispatch reuses `runtime.Run` via a `dispatchFunc` seam (ADR 046 §5).** The field
  defaults to the real `runtime.Run` on the live path; tests override it with a spy so
  dispatch is asserted WITHOUT launching real sandboxes.
- **Approval = pause-and-resume (ADR 046 §4).** `require_approval` → report plan, hold
  in memory, dispatch nothing; resume only when an explicit approval message returns
  over the inbound channel. The approval message is envelope-verified + armor-guarded
  (same as a goal); after `VerifyAndOpen` the orchestrator asserts the envelope
  `From`/`To` expected roles (operator→orchestrator) — carry-forward from task 098
  SEC-001 (MEDIUM).

## Requirements coverage

| Req ID     | Description                                                           | Test cases |
|------------|-----------------------------------------------------------------------|------------|
| REQ-081-01 | Goal intake; plan decomposed into ≥1 sub-goal before any dispatch     | TC-081-01  |
| REQ-081-02 | require_approval → no dispatch; plan reported; approval → resume      | TC-081-02  |
| REQ-081-03 | allow → SelectRecipe per sub-goal; worker assembly invoked per sub-goal | TC-081-03 |
| REQ-081-04 | Worker outcomes aggregated into typed PlanResult, reported           | TC-081-04  |
| REQ-081-05 | `internal/orchestrator` has no DIRECT import of `internal/executor`  | TC-081-05  |
| REQ-081-06 | `internal/supervisor` unchanged (empty diff)                         | TC-081-06  |

---

## Test cases

### TC-081-01 — Goal → Plan with ≥1 sub-goal, produced before any dispatch

- **Requirement:** REQ-081-01
- **Level:** L2 (unit test; `StructuredPlanner` + spy dispatch)
- **Status:** active

**Input A (free-form goal):** `supervisor.Task{ID: "g1", Spec: "Fix the 3 broken
links in docs/spec/"}` through `StructuredPlanner.Plan`.

**Expected:**
- Returns a `Plan` with `len(plan.SubGoals) == 1`.
- `plan.SubGoals[0].RecipeName == "coding-agent"` (default recipe).
- `plan.SubGoals[0].Task.Spec == "Fix the 3 broken links in docs/spec/"`.

**Input B (structured multi-line goal):**
```
coding-agent: implement feature X
docs-fix: update the changelog
```

**Expected:**
- `len(plan.SubGoals) == 2`.
- `SubGoals[0].RecipeName == "coding-agent"`, `SubGoals[0].Task.Spec == "implement feature X"`.
- `SubGoals[1].RecipeName == "docs-fix"`, `SubGoals[1].Task.Spec == "update the changelog"`.
- Comment lines (`# …`) and blank lines are ignored.

**Ordering assertion:** with a spy `dispatchFunc` and a policy stub returning
`require_approval`, after `Orchestrator.Handle(goal)` the spy records **zero**
dispatch calls — the plan is produced and surfaced to the approval gate before any
dispatch happens.

---

### TC-081-02 — require_approval pauses dispatch; plan reported; approval resumes

- **Requirement:** REQ-081-02
- **Level:** L2 (unit test; fake policy returns `require_approval`, fake Reporter,
  spy dispatch)
- **Status:** active

**Input:** A two-sub-goal goal; policy stub returns `DecisionRequireApproval` for the
`spawn-plan` action.

**Expected (pause):**
- Spy `dispatchFunc` recorded **0** calls (no worker dispatched).
- The fake Reporter received exactly one message that contains the substring
  `"Approve?"` and names both sub-goals' recipe/spec (the rendered plan).
- The plan is held in memory: `Orchestrator` reports it has a pending plan for the
  goal ID.

**Then (resume):** an explicit approval message arrives over the inbound channel
(modelled as a verified `(from="operator", to="orchestrator", text="approve g1")`
approval token). `Orchestrator.Resume(approval)`:
- Spy `dispatchFunc` recorded **2** calls (one per sub-goal) in sub-goal order.
- A `PlanResult` is reported over the Reporter after dispatch completes.

**Security (task 098 SEC-001 carry-forward):** a resume with a mismatched envelope
role (`from="attacker"` or `to != "orchestrator"`) is REJECTED — `Resume` returns a
non-nil error and the spy records **0** additional dispatch calls.

---

### TC-081-03 — allow dispatches one worker per sub-goal via the recipe seam

- **Requirement:** REQ-081-03
- **Level:** L2 (unit test; fake policy returns `allow`, spy dispatch)
- **Status:** active

**Input:** A two-sub-goal structured goal (`coding-agent: …`, `docs-fix: …`); policy
stub returns `DecisionAllow`.

**Expected:**
- `recipe.SelectRecipe` resolves successfully for each sub-goal recipe name (both
  `coding-agent` and `docs-fix` are registered) — an unknown recipe name yields a
  failed outcome for that sub-goal, NOT a dispatch.
- Spy `dispatchFunc` recorded exactly **2** calls.
- The recipe name passed to dispatch call `i` equals `SubGoals[i].RecipeName`
  (assert exact names `"coding-agent"`, `"docs-fix"` in order).
- The `supervisor.Task` passed to dispatch call `i` equals `SubGoals[i].Task`
  (assert the Spec text).
- No inline code generation occurs in the orchestrator (covered structurally by
  TC-081-05).

---

### TC-081-04 — Two workers (one success, one failure) → aggregated PlanResult reported

- **Requirement:** REQ-081-04
- **Level:** L2 (unit test; spy dispatch returns success for one, error for the other)
- **Status:** active

**Input:** A two-sub-goal goal; policy `allow`. The spy `dispatchFunc` returns `nil`
(success) for the first sub-goal and a non-nil error (e.g. `"gate failed: go test"`)
for the second.

**Expected:**
- `PlanResult.Goal` equals the original goal text.
- `len(PlanResult.Outcomes) == 2`.
- `Outcomes[0]`: `SubGoal`/`Recipe` match sub-goal 0; `Success == true`.
- `Outcomes[1]`: `SubGoal`/`Recipe` match sub-goal 1; `Success == false`; `Detail`
  contains the failure reason substring (`"gate failed"`).
- Exactly one summary message is reported over the fake Reporter; the rendered text
  contains both recipe names and a success/failure marker for each (e.g. the rendered
  summary contains both `"coding-agent"` and `"docs-fix"` and indicates one pass + one
  fail). Sequential dispatch: the second sub-goal is dispatched even though it fails
  (no early abort in v1 aggregation; both outcomes recorded).

---

### TC-081-05 — Orchestrator has no DIRECT import of internal/executor (invariant)

- **Requirement:** REQ-081-05
- **Level:** L3 (direct-import assertion — NOT the transitive graph; ADR 046 D-2)
- **Status:** active

**Input:** the DIRECT import list of the `internal/orchestrator` package, obtained
via `go list -f '{{ join .Imports "\n" }}' ./internal/orchestrator` (a Go test that
shells `go list` and parses its own package import list), OR a hermetic `go/parser`
walk of the package's `.go` source files collecting import paths.

**Expected:**
- `internal/executor` does NOT appear in the orchestrator's direct import list.
- The orchestrator DOES directly import `internal/recipe`, `internal/runtime`,
  `internal/policy`, `internal/supervisor` — the transitive reach into
  `internal/executor` via `internal/runtime` is the ADR-042-blessed dispatch path and
  is EXPECTED; the assertion is on direct imports only.
- A `make fitness-orchestrator-no-executor` check mirrors this at the build level
  (asserts `internal/executor` is not a direct import of `internal/orchestrator`) and
  is wired into `make fitness`.

---

### TC-081-06 — supervisor package unchanged by this task

- **Requirement:** REQ-081-06
- **Level:** L2 (structural diff)
- **Status:** active

**Input:** `git diff <merge-base> -- internal/supervisor/` over the task branch.

**Expected:** empty diff. The orchestrator is purely additive; `internal/supervisor`
is read (for `Task`, `Reporter`, `GoalSource`) but not modified.

---

## Verification plan

- **Highest level achievable:** L2 (unit tests with stub GoalSource, stub policy,
  fake Reporter, spy dispatch) + L3 (direct-import fitness check). An L5/L6
  end-to-end orchestrator run requires tasks 083 (transport) and 084 (memory-guard)
  and is out of scope here.
- **Harness command:**
  ```
  go test -count=1 ./internal/orchestrator/...
  make fitness-orchestrator-no-executor
  make fitness-supervisor-isolation
  make check
  ```
  Expected:
  - Unit tests → `ok  github.com/tkdtaylor/agent-builder/internal/orchestrator`
  - `make fitness-orchestrator-no-executor` → `PASS …`
  - `make check` → `All checks passed.`

## Out of scope

- Orchestrator↔worker signed-envelope transport (task 083).
- memory-guard on orchestrator state (task 084) — v1 is in-memory behind `PlanStore`.
- Orchestrator self-containment + its own policy gating + fleet audit (task 085).
- Multi-worker concurrent dispatch (task 086) — v1 is sequential.
- The `LLMPlanner` concrete (ADR 046 §6) — only the `Planner` seam is shaped here.
- The live Telegram inbound approval wiring end-to-end (the approval-role assertion is
  unit-tested against an in-memory verified token; live bot round-trip is L6).
