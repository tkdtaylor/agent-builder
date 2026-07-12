# Test Spec 177: register `coding-agent` as the first governed skill; route goal->skill selection through the registry

**Linked task:** [`docs/tasks/backlog/177-coding-as-first-skill.md`](../backlog/177-coding-as-first-skill.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

Task 176 built `internal/skill` (`Manifest`, `Register`/`Select`/`List`,
`SelectForGoal`) with zero callers. This task is the first real migration ADR
066 named: register the existing `coding-agent` recipe
(`internal/runtime/run.go:310`, `recipe.Register("coding-agent",
newCodingAgentRecipe)`) as a `skill.Manifest`, and make the orchestrator's
goal-to-recipe-name resolution go THROUGH `skill.SelectForGoal` before falling
through to `recipe.SelectRecipe`, replacing the single hardcoded
`orchestrator.DefaultRecipeName` constant's role as the ONLY selection
mechanism (the constant itself may remain as the skill registry's OWN
`fallback` parameter, it does not disappear, it moves one layer down).

**This is a routing change, not a recipe change.** `internal/recipe`'s
`newCodingAgentRecipe`/`Register`/`SelectRecipe` are completely unmodified;
this task adds exactly one new `skill.Manifest` pointing at the SAME recipe
name, and changes WHERE the orchestrator's `RecipeName` decision comes from.

**Module boundary:** `internal/orchestrator` (the routing edit) and a small
registration site (either `internal/runtime` alongside the existing
`recipe.Register` call, or a new `internal/orchestrator/skills.go` `init()`,
executor's choice, document which). `internal/skill` and `internal/recipe`
gain no new exported surface.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-177-01 | A `skill.Manifest{Name: "coding-agent", Description: "contribute code changes to a target repo, the reference build's default capability", RecipeName: "coding-agent", RequiredPermissions: [...], GateChecks: [...]}` is registered via `skill.Register` at process init, alongside (not replacing) the existing `recipe.Register("coding-agent", ...)` call. | TC-177-01 |
| REQ-177-02 | The orchestrator's sub-goal recipe-name resolution (wherever a goal's text is currently mapped to `DefaultRecipeName`/an explicit recipe name, e.g. in the `GoalAnalyzer`/planner path, `internal/orchestrator/analyzer.go`, or `Plan`/`SubGoal` construction) now calls `skill.SelectForGoal(goalText, <registered skills>, "coding-agent")` FIRST, and uses the returned `Manifest.RecipeName` as the `SubGoal.RecipeName`, instead of directly assigning `DefaultRecipeName`/an unmediated recipe name. | TC-177-02, TC-177-03 |
| REQ-177-03 | With exactly one skill registered (`"coding-agent"`), EVERY goal resolves to `RecipeName == "coding-agent"` regardless of goal text (since `SelectForGoal`'s fallback IS `"coding-agent"` and no other skill exists to keyword-match against), so existing end-to-end behavior for the single-skill deployment shape is byte-for-byte unchanged, this is the load-bearing regression proof that inserting a selection layer above a single skill is a true no-op for observable behavior. | TC-177-04 |
| REQ-177-04 | `recipe.SelectRecipe("coding-agent")` still resolves and dispatches exactly as before this task (the recipe layer itself, and the eventual `runtime.Run` call using `config.RecipeName`, are completely unmodified). | TC-177-05 |
| REQ-177-05 | `docs/spec/architecture.md`/`interfaces.md` are updated to describe the new goal->skill->recipe resolution chain, replacing any stale "goal maps directly to `DefaultRecipeName`" language. | TC-177-06 |
| REQ-177-06 | Pre-existing `internal/orchestrator`, `internal/recipe`, `internal/skill` suites pass unchanged apart from the new registration/routing addition. | TC-177-07 |

---

## Pre-implementation checklist

- [x] Task 176 merged (`internal/skill` exists)
- [x] Task 095 merged (`coding-agent` recipe registered, `DefaultRecipeName`
  exists)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-177-01, `coding-agent` is registered as a skill

- **Requirement:** REQ-177-01
- **Level:** L2 (unit test, asserts against the package-level `skill` registry
  after the production `init()`/registration call runs, mirroring how
  `internal/recipe/agentbuilderworker`'s `init()` registration is tested
  today)
- **Test file:** `internal/orchestrator/skills_177_test.go` (new, or wherever
  the registration site lands)

**Step:** Import the package containing the registration side-effect (mirrors
`docsfix.DocsFixGate{}`'s existing "ensure init() is triggered by importing"
pattern, `internal/cli/cli.go:16`). `skill.Select("coding-agent")`.

**Expected output:** returns a `Manifest` with `RecipeName == "coding-agent"`,
non-empty `Description`, and non-nil (may be empty-but-non-nil, or populated,
executor's documented choice) `RequiredPermissions`/`GateChecks` slices.

---

### TC-177-02, goal resolution calls `SelectForGoal`

- **Requirement:** REQ-177-02
- **Level:** L2 (unit test on the resolution function directly, not the full
  `Handle` chain, isolating the routing logic)

**Step:** Call the orchestrator's (renamed/refactored, wherever it now lives)
recipe-name-resolution function directly with a goal text and a
TWO-entry test-local skill registry (`"coding-agent"` and a second, fake
`"docs-fix"` skill whose `Description` keyword-matches the test goal text).

**Expected output:** the resolution function returns `RecipeName ==
"docs-fix"`'s recipe name (proving it actually calls `SelectForGoal` and uses
its result, not a hardcoded constant) for THIS test's two-skill fixture. This
test is explicitly allowed to use a fixture registry distinct from
production's single-skill registry, to prove the ROUTING mechanism works
before REQ-177-03's single-skill no-op regression proof re-confirms
production behavior is unaffected today.

---

### TC-177-03, the resolved `RecipeName` flows into `SubGoal`

- **Requirement:** REQ-177-02
- **Level:** L2 (end-to-end within `internal/orchestrator`, real `Planner`
  fixture, mirrors existing `orchestrator_test.go` `Plan`/`SubGoal`
  construction assertions)

**Step:** Construct a `Plan` via the orchestrator's planning path for a goal
that (using the TC-177-02 style two-skill test fixture, injected via
whatever seam the resolution function accepts, executor's choice of
dependency-injection shape, document it) should route to `"docs-fix"`.

**Expected output:** the resulting `SubGoal.RecipeName == "docs-fix"` (not
`"coding-agent"`), proving the resolved skill's declared recipe name reaches
the actual dispatch-shaping struct field `dispatchOne` later uses to call
`recipe.SelectRecipe`.

---

### TC-177-04, single-skill production registry is a true no-op (the load-bearing regression proof)

- **Requirement:** REQ-177-03
- **Level:** L2 (uses the REAL production `skill` registry state, i.e. does
  NOT inject a test-local fixture, exercising exactly what a live
  `agent-builder orchestrate` process would resolve)

**Step:** Using the real, production-registered `skill` package state (only
`"coding-agent"` registered), resolve recipe names for at least 5 varied goal
texts (including text that would have keyword-matched a hypothetical OTHER
skill if one existed, to prove the single-entry registry cannot accidentally
mis-route).

**Expected output:** all 5 resolve to `RecipeName == "coding-agent"`,
byte-identical to what `DefaultRecipeName` alone would have produced before
this task. This is the acceptance-critical regression proof: inserting the
selection layer changes NOTHING observable while only one skill exists.

---

### TC-177-05, `recipe.SelectRecipe` unaffected

- **Requirement:** REQ-177-04
- **Level:** L2 (regression)

**Step:** Re-run the pre-existing `internal/recipe` suite and
`internal/runtime`'s `newCodingAgentRecipe`-related tests unmodified.

**Expected output:** byte-identical pass.

---

### TC-177-06, spec/doc footprint updated

- **Requirement:** REQ-177-05
- **Level:** L1 (document review)

**Step:** Read `docs/spec/architecture.md` and `docs/spec/interfaces.md`'s
orchestrator/recipe sections.

**Expected output:** they describe goal -> `skill.SelectForGoal` ->
`Manifest.RecipeName` -> `recipe.SelectRecipe`, replacing any prior language
implying a goal maps directly and solely to `DefaultRecipeName`.

---

### TC-177-07, full regression

- **Requirement:** REQ-177-06
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/orchestrator/... ./internal/recipe/... ./internal/skill/... ./internal/runtime/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2/L3, TC-177-04's real-production-registry
  no-op regression proof is the load-bearing evidence (it is the strongest
  achievable proof, short of a live `orchestrate` run, that this task changes
  no observable behavior for the current single-skill deployment shape); an
  operator running `agent-builder orchestrate` against a real goal and
  observing it still dispatches to the coding-agent recipe (L6) is available
  but not required beyond what TC-177-04 already proves at L2.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/orchestrator/... ./internal/skill/... -run TestTC177
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Registering any SECOND skill (this task's job is proving the seam works
  end-to-end with the one skill that exists today; a second skill is a future
  task once one is built).
- Any change to `internal/recipe` itself.
- LLM-driven or config-driven skill selection beyond `SelectForGoal`'s v1
  keyword/fallback rule (task 176's own out-of-scope, unchanged here).
