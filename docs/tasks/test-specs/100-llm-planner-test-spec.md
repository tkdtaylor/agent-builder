# Test spec — Task 100: LLMPlanner

**Linked task:** `docs/tasks/backlog/100-llm-planner.md`
**Written:** 2026-06-28
**Status:** ready
**Governing ADRs:** ADR 046 §1/§6 (Planner seam, LLMPlanner as the named follow-on behind
`orchestrator.Planner`), ADR 043 (executor-registry + router; the planning seam resolves
its model through the router, never through `internal/executor` directly).

## Context

ADR 046 §1 deliberately chose `StructuredPlanner` (rule-based, no LLM) as the v1
`Planner` implementation and named the LLMPlanner a "named follow-on gated behind task 095
(the router)." Tasks 087–095 shipped the registry, router, and executor harness adapters.
The LLMPlanner is now unblocked.

The LLMPlanner implements `orchestrator.Planner` — the exact same interface `StructuredPlanner`
satisfies — so the orchestrator adopts it by swapping the concrete at construction time with no
change to `orchestrator.go`. A free-form human goal is submitted to a model (via the router),
and the model returns a structured plan that the LLMPlanner parses into `orchestrator.Plan`.

### Import-boundary question (key design decision)

The orchestrator's import invariant (REQ-081-05 / F-010) forbids `internal/orchestrator` from
directly importing `internal/executor`. The `Planner` seam was deliberately designed so that
a new `Planner` concrete can reach a model through the router/registry path **without**
the orchestrator importing `internal/executor`. The question is: where does `LLMPlanner` live,
and how does it reach the router?

**Resolution:** `LLMPlanner` lives in a NEW package `internal/orchestrator/planner` (a
sub-package of `internal/orchestrator`, NOT inside `internal/orchestrator` itself). It
imports `internal/router` and `internal/registry` (the routing path) but NOT
`internal/executor`. The `internal/orchestrator` package imports `internal/executor` only
transitively (through `internal/runtime`, for dispatch — the ADR-042-blessed path); the
LLMPlanner reaches the model through the router, which itself imports `internal/executor`
(the router lives on the executor side of the supervisor boundary, as documented in
`internal/router`'s package comment). This keeps the direct-import fitness check
(`make fitness-orchestrator-no-executor`) green: the check asserts `internal/executor`
is not a DIRECT import of `internal/orchestrator` — `internal/orchestrator/planner` is a
separate package and gets its own fitness check (F-014) asserting it also does not directly
import `internal/executor`.

The `LLMPlanner` takes a `router.Router` (or a narrow `ExecutorResolver` interface the
package defines) and a prompt template, calls the router's `Select` to get a
`registry.RegistryEntry`, invokes the chosen harness's `executor.Executor.RunDecompose`
method (or an equivalent narrow interface — see REQ-100-04), and parses the returned text
into sub-goals. If the model response is malformed or empty, the LLMPlanner fails-closed
(returns an error, never an empty-repo plan).

### Fail-closed on empty/malformed model response (REQ-100-03)

An LLM may return an empty string, garbage, or a plan with no sub-goals. The LLMPlanner
MUST return an error in these cases — it must never emit a `Plan` with zero sub-goals
(which the orchestrator would reject anyway in `Handle`) AND it must never produce a
sub-goal carrying an empty `TargetRepo` where the recipe would default to the own-repo.
The fail-closed rule is: if the parsed output does not yield at least one valid sub-goal
with a named recipe, return an error.

### Sub-goals must carry `TargetRepo` / `Sink` for the self-repo bright line

ADR 050 §2: the orchestrator's `spawn-worker` gate reads `SubGoal.TargetRepo` and
`SubGoal.Sink` to enforce the self-repo bright line. `StructuredPlanner` does not parse
these fields from goal text (they come from context); `LLMPlanner` must prompt the model
to include them in the structured output and parse them into each `SubGoal`. A sub-goal
that omits `TargetRepo` is permitted (it means "the recipe's default sink applies") but a
sub-goal with `TargetRepo == OwnRepo` must be rejected by the bright line as before.

### Selectability via config (REQ-100-06)

`AGENT_BUILDER_PLANNER` env var selects the planner: `"structured"` (default) or `"llm"`.
The `orchestrate` subcommand (task 099) reads this var when constructing the orchestrator.
This is how task 099 and task 100 compose: 099 provides the wiring, 100 provides the
`"llm"` concrete behind the same seam.

## Requirements coverage

| Req ID      | Description                                                                                                           | Test cases            |
|-------------|-----------------------------------------------------------------------------------------------------------------------|-----------------------|
| REQ-100-01  | `LLMPlanner` implements `orchestrator.Planner`; produces a valid `PlanResult` with sub-goals from a goal via a stub model | TC-100-01        |
| REQ-100-02  | Each sub-goal carries `TargetRepo`/`Sink` parsed from the model response; self-repo bright line remains intact        | TC-100-02             |
| REQ-100-03  | Fail-closed on malformed/empty model response: returns an error, never an empty-repo plan                             | TC-100-03             |
| REQ-100-04  | LLMPlanner uses an `ExecutorResolver` interface (not `internal/executor` directly); import invariant preserved        | TC-100-04             |
| REQ-100-05  | Fitness F-014: `internal/orchestrator/planner` does not directly import `internal/executor`                           | TC-100-05             |
| REQ-100-06  | Selectable via `AGENT_BUILDER_PLANNER` env var alongside `StructuredPlanner`                                          | TC-100-06             |

---

## Test cases

### TC-100-01 — LLMPlanner produces a valid Plan from a goal via a stub model (L2)

- **Requirement:** REQ-100-01
- **Level:** L2 (unit test; stub model returns a canned decomposition response)

**Input:** Construct an `LLMPlanner` with a stub `ExecutorResolver` that returns a canned
text response:
```
coding-agent: Add rate limiting to the API layer
docs-fix: Update CHANGELOG for v1.2.0
```
Feed a `supervisor.Task{ID: "goal-1", Spec: "add rate limiting and update docs"}` to
`LLMPlanner.Plan(task)`.

**Expected output (assertions):**
- Returns a `Plan` with `Goal == "add rate limiting and update docs"` and `GoalID == "goal-1"`.
- `Plan.SubGoals` has exactly 2 entries:
  - `SubGoals[0].RecipeName == "coding-agent"` and `SubGoals[0].Task.Spec` contains `"Add rate limiting"`.
  - `SubGoals[1].RecipeName == "docs-fix"` and `SubGoals[1].Task.Spec` contains `"Update CHANGELOG"`.
- Returns `nil` error.
- The stub `ExecutorResolver` was called exactly once with the goal text in the prompt.
- The returned `Plan` value satisfies the same `orchestrator.Planner` interface that
  `StructuredPlanner` satisfies — compile-time assertion:
  ```go
  var _ orchestrator.Planner = (*planner.LLMPlanner)(nil)
  ```

---

### TC-100-02 — Sub-goals carry TargetRepo/Sink; self-repo bright line preserved (L2)

- **Requirement:** REQ-100-02
- **Level:** L2 (unit test)

**Input (happy path):** Stub returns a response including `TargetRepo` and `Sink` for each
sub-goal (e.g. in a structured JSON block or a recognized line format the LLMPlanner
parser handles):
```
coding-agent: task A | target_repo=github.com/tkdtaylor/exec-sandbox | sink=github.com/tkdtaylor/exec-sandbox
```

**Expected output (happy path):**
- `SubGoals[0].TargetRepo == "github.com/tkdtaylor/exec-sandbox"`.
- `SubGoals[0].Sink == "github.com/tkdtaylor/exec-sandbox"`.
- `SubGoals[0].Task.Spec` contains the task text without the metadata fields.

**Input (self-repo sub-goal):** Stub returns a response where one sub-goal has
`target_repo=github.com/tkdtaylor/agent-builder` (the own-repo). Feed to `LLMPlanner.Plan`.

**Expected output (self-repo sub-goal):**
- Either: the LLMPlanner parser itself filters out the own-repo sub-goal and returns the
  remaining sub-goals (defensive parsing), OR
- The sub-goal is included in `Plan.SubGoals` with `TargetRepo ==
  "github.com/tkdtaylor/agent-builder"`, and the orchestrator's existing
  `decideSpawnWorker` bright line fires as documented in task 085 TC-085-05 — either path
  is acceptable as long as the own-repo worker is never dispatched.
- In either case: the test also runs the assembled orchestrator (with LLMPlanner + stub
  dispatch spy + stub policy returning `allow`) and asserts the own-repo sub-goal is NOT
  dispatched.

---

### TC-100-03 — Fail-closed on malformed/empty model response (L2)

- **Requirement:** REQ-100-03
- **Level:** L2 (unit test)

**Sub-case A — empty response:**
- Stub returns an empty string `""`.
- `LLMPlanner.Plan(task)` returns a non-nil error.
- The returned `Plan` is the zero value (not a Plan with zero sub-goals — that is, no
  partial Plan leaks out on the error path).

**Sub-case B — garbage response:**
- Stub returns `"🤔 I cannot help with that."` (no parseable sub-goal lines).
- `LLMPlanner.Plan(task)` returns a non-nil error.
- The error message describes the parse failure (not a panic or zero-value return).

**Sub-case C — model returns a plan with only an own-repo sub-goal:**
- Stub returns one sub-goal with `target_repo=github.com/tkdtaylor/agent-builder`.
- If the LLMPlanner filters own-repo sub-goals defensively: the filtered plan has zero
  sub-goals → the LLMPlanner must return an error (not a zero-sub-goal plan, which would
  silently succeed with no dispatch).
- If the LLMPlanner does NOT filter and passes to the orchestrator: the orchestrator's
  bright line fires (TC-085-05); but in this sub-case we assert the LLMPlanner itself does
  not emit an error from a structurally valid (one sub-goal) response — the bright line is
  the orchestrator's concern.

**Note:** the test spec leaves the own-repo filtering choice to the implementer. Both
approaches are acceptable at TC-100-02/03 as long as the dispatch spy is never called with
an own-repo target.

**Sub-case D — `ExecutorResolver` returns an error:**
- Stub's `Resolve` method returns an error (no eligible executor).
- `LLMPlanner.Plan(task)` returns a non-nil error.
- The error wraps the resolver error.

---

### TC-100-04 — LLMPlanner uses `ExecutorResolver` interface; does not import `internal/executor` directly (L2 + L3)

- **Requirement:** REQ-100-04
- **Level:** L2 (compile-time: the interface is the seam) + L3 (fitness check)

**L2 assertion (compile-time):** The `planner.ExecutorResolver` interface is defined in
`internal/orchestrator/planner` and is satisfied by `*router.Router` (via an adapter or
directly). The test constructs an `LLMPlanner` with a value implementing
`ExecutorResolver` that is NOT of type `*executor.Executor` — confirming the seam is the
interface, not the concrete.

Concretely: the test in `internal/orchestrator/planner/llmplanner_test.go` imports
`internal/router` and constructs a stub `ExecutorResolver`. It does NOT import
`internal/executor`. If the test compiled successfully with this constraint, the interface
is functioning as the seam.

**L3 assertion:** Run `make fitness-llm-planner-no-executor` (F-014):
```sh
go list -f '{{range .Imports}}{{.}}\n{{end}}' github.com/tkdtaylor/agent-builder/internal/orchestrator/planner | grep -qF 'internal/executor' && echo 'FAIL F-014' || echo 'PASS F-014'
```
Expected: `PASS F-014`.

---

### TC-100-05 — Fitness check F-014: `internal/orchestrator/planner` does not directly import `internal/executor` (L3)

- **Requirement:** REQ-100-05
- **Level:** L3 (fitness check)

**Input:** `make fitness-llm-planner-no-executor` (or the equivalent `go list` invocation).

**Expected output:**
- `internal/executor` does NOT appear in the direct import list of
  `internal/orchestrator/planner`.
- Any other `agent-builder/internal/` package that `internal/orchestrator/planner`
  imports but that transitively reaches `internal/executor` is acceptable — only the
  DIRECT-import check is enforced (matching the precedent of F-010 for
  `internal/orchestrator` itself).
- The fitness check is added to `.PHONY` and to the `fitness:` prerequisites in the
  Makefile; it is documented as **F-014** in `docs/spec/SPEC.md` and
  `docs/spec/fitness-functions.md`.

---

### TC-100-06 — Selectable via `AGENT_BUILDER_PLANNER` env var (L2)

- **Requirement:** REQ-100-06
- **Level:** L2 (unit test on the CLI assembler / `NewPlannerFromEnv`)

**Input A — unset / `"structured"`:**
- `AGENT_BUILDER_PLANNER` unset (or set to `"structured"`).
- Call `planner.NewPlannerFromEnv(resolverFn)` (or equivalent CLI assembler path).
- Expected: returns a value whose dynamic type is `*orchestrator.StructuredPlanner` (or a
  wrapper that delegates to it), NOT `*planner.LLMPlanner`.

**Input B — `"llm"`:**
- `AGENT_BUILDER_PLANNER` set to `"llm"`.
- Call `planner.NewPlannerFromEnv(resolverFn)` with a stub resolver.
- Expected: returns a value whose dynamic type is `*planner.LLMPlanner`.

**Input C — unknown value:**
- `AGENT_BUILDER_PLANNER` set to `"magic"`.
- Expected: returns a non-nil error (unknown planner type; the CLI prints an error and
  exits with `ExitUsage`).

---

## Verification plan

- **Highest level achievable in CI:** L2 (unit tests with stub model) + L3 (fitness
  F-014). L5 (real model drives decomposition via the live router on the dev host) and L6
  (observed live plan via `agent-builder orchestrate`) are achievable on the dev host but
  require a real model endpoint and are NOT claimed in CI.
- **L2 harness commands:**
  ```
  go test -count=1 ./internal/orchestrator/planner/... ./internal/orchestrator/...
  ```
  Expected: `ok`.
- **L3 fitness commands:**
  ```
  make fitness-llm-planner-no-executor
  make fitness-orchestrator-no-executor
  make check
  ```
  Expected: `PASS F-014`; `PASS F-010`; `All checks passed.`
- **L5 (operator-run, not CI):** set `AGENT_BUILDER_PLANNER=llm`, configure a real router
  entry (local or cloud), run `agent-builder orchestrate` with a free-form goal, observe the
  model decompose it into sub-goals. Record model, endpoint, and decomposition output in the
  verify commit.

## Out of scope

- Changing the `orchestrator.Planner` interface (it is stable; the LLMPlanner is a new
  concrete behind it).
- The `orchestrate` subcommand wiring (task 099).
- The prompt engineering and model fine-tuning for optimal decomposition quality.
- Key management / rotation for the executor's API key (existing vault/secret mechanism,
  inherited from the registry entry).
- A2A multi-model planning (not yet scoped).
- Extending the `StructuredPlanner` (separate task if needed).
