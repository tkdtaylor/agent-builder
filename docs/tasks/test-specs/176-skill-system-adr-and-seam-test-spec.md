# Test Spec 176: ADR 066 + the `internal/skill` registry seam

**Linked task:** [`docs/tasks/backlog/176-skill-system-adr-and-seam.md`](../backlog/176-skill-system-adr-and-seam.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

`docs/plans/roadmap.md`'s Forward arc item 2, "General self-extending skill
system, coding (contribute-to-repo, start-a-project) becomes one skill among
many, the agent selects/loads skills per goal", and `AGENTS.md`'s framing
("Coding is one skill among many... The autonomous coding agent is the first
reference build") both name this gap. Today the only selection mechanism is
`internal/recipe`'s `Register`/`SelectRecipe` (a flat namespace of execution
strategies, `internal/recipe/recipe.go:131-166`) and
`orchestrator.DefaultRecipeName = "coding-agent"`
(`internal/orchestrator/orchestrator.go:65`), a single hardcoded default with no
governance layer above it: no declared required permissions, no gate checks
distinct from the recipe's own `GateFactory`, no notion of "is this skill even
allowed to run for this goal" separate from "which execution strategy does it
use."

This task is ADR + seam only. It does not migrate any behavior: the existing
`coding-agent` recipe keeps working exactly as it does today through
`orchestrator.DefaultRecipeName`/`recipe.SelectRecipe`, unmodified. Task 177
registers it as the first governed skill and routes goal->skill selection
through this seam.

**Deliverables:** (1) `docs/architecture/decisions/066-general-skill-system-seam.md`
(ADR 066, ADR 065 is taken), recording the skill/recipe relationship decision;
(2) `internal/skill`, a new leaf package (mirrors `internal/recipe`'s own
leaf-ness convention: imports only stdlib, no `internal/executor`/`internal/orchestrator`).

**Module boundary:** `internal/skill` (new leaf) plus the ADR document. No
change to `internal/recipe` or `internal/orchestrator` in this task.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-176-01 | ADR 066 exists at `docs/architecture/decisions/066-general-skill-system-seam.md`, records the decision that a `Skill` is a governed capability (name, description, contract, required permissions, gate checks) that DECLARES which `recipe.Recipe` it executes through, `skill = governed capability, recipe = execution strategy`, and states this task migrates no behavior. | TC-176-01 |
| REQ-176-02 | `internal/skill` exports `Manifest{Name, Description, RecipeName string, RequiredPermissions []string, GateChecks []string}` (or an equivalent typed shape, executor's naming discretion, but ALL five concepts from the task brief, name/description/contract/permissions/gate-checks, must be represented as typed, non-`interface{}` fields). | TC-176-02 |
| REQ-176-03 | `Register(name string, manifest Manifest) error` adds a named skill to a package-level registry; registering a duplicate name returns a descriptive error (a value-returning error, NOT a panic, deliberately DIFFERENT from `recipe.Register`'s panic-on-duplicate convention, because skill registration may happen dynamically from config/discovery in a later task, panicking there would be a crash-on-bad-config footgun, document this deliberate divergence from the recipe precedent in the package doc comment). | TC-176-04, TC-176-05 |
| REQ-176-04 | `Select(name string) (Manifest, error)` returns the named skill or a descriptive not-found error; `List() []string` returns registered names in deterministic (sorted) order. | TC-176-06, TC-176-07 |
| REQ-176-05 | `SelectForGoal(goalText string, registry map[string]Manifest) (Manifest, error)` (v1 selection semantics: a simple, documented, deterministic rule, e.g. exact/substring keyword match against each `Manifest.Name`/`Description`, falling back to a configured default skill name when no match is found; a full LLM-driven selection is explicitly out of scope, task 177 or a later task may swap this rule out, keeping the FUNCTION SIGNATURE stable) is defined, pure, and testable without any registry global state (takes the registry as an explicit parameter, not the package-level singleton, for testability, the package-level `Register`/`Select`/`List` trio is the convenience/production API, `SelectForGoal` is the pure logic underneath). | TC-176-08, TC-176-09 |
| REQ-176-06 | `internal/skill` is a strict leaf (stdlib only, no `agent-builder/internal/*` import, matching `internal/recipe`'s own leaf discipline, though NOT importing `internal/supervisor` even, since `Manifest` does not need any seam-factory types, unlike `recipe.Recipe`). A new fitness function (F-016, following F-015 from task 167) enforces it. | TC-176-10 |
| REQ-176-07 | No existing package's behavior changes: `internal/recipe`, `internal/orchestrator`'s `DefaultRecipeName`/`SelectRecipe` call sites are byte-for-byte unmodified. | TC-176-11 |

---

## Pre-implementation checklist

- [x] ADR 065 accepted (the numbering precedent, ADR 066 is the next number)
- [x] Task 095 merged (`internal/recipe`, the sibling seam this ADR relates
  `Skill` to)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-176-01, ADR 066 exists and states the required decisions

- **Requirement:** REQ-176-01
- **Level:** L1 (document review, not a Go test; verified by the executor
  reading the committed file and by `spec-verifier` reviewing it against this
  requirement)

**Step:** Read `docs/architecture/decisions/066-general-skill-system-seam.md`.

**Expected output:** the document has `Status: accepted` (or `proposed`,
executor's/reviewer's call per this project's ADR convention), a Context
section referencing the roadmap's Forward arc item 2 and `AGENTS.md`'s "coding
is one skill among many" framing, a Decision section explicitly stating
"skill = governed capability, recipe = execution strategy a skill declares and
executes through" and "this task (176) migrates no behavior, task 177 is the
first migration", and a Consequences section.

---

### TC-176-02, `Manifest` shape

- **Requirement:** REQ-176-02
- **Level:** L2 (unit test)
- **Test file:** `internal/skill/manifest_test.go` (new)

**Step:** Construct a `Manifest` with all five fields populated (including a
multi-entry `RequiredPermissions`/`GateChecks` slice).

**Expected output:** field-for-field access works as typed (compile-time
proof plus a runtime equality check after a round trip if the executor adds
JSON tags for future config-file loading, optional but recommended given
task 175's precedent of config-declared entries).

---

### TC-176-03, (reserved, intentionally skipped, TC numbering matches REQ table)

---

### TC-176-04, `Register` rejects a duplicate name with an error, not a panic

- **Requirement:** REQ-176-03
- **Level:** L2
- **Test file:** `internal/skill/registry_test.go` (new)

**Step:** `Register("coding-agent", m1)`, then `Register("coding-agent", m2)`.

**Expected output:** the second call returns a non-nil, descriptive error
(mentions the name and "already registered"); it does NOT panic (contrast
explicitly asserted via `recover()` around the call returning nil, proving no
panic occurred, distinguishing this from `recipe.Register`'s panic
convention).

---

### TC-176-05, `Register` accepts a unique name

- **Requirement:** REQ-176-03
- **Level:** L2

**Step:** `Register("docs-fix-skill", m)` (a fresh name in a clean test-local
registry instance, or the package singleton reset via a test helper, executor's
choice of test isolation strategy, document it).

**Expected output:** `err == nil`; `Select("docs-fix-skill")` subsequently
returns `m`.

---

### TC-176-06, `Select` returns a descriptive not-found error

- **Requirement:** REQ-176-04
- **Level:** L2

**Step:** `Select("nonexistent-skill")`.

**Expected output:** `(Manifest{}, err)` where `err` is non-nil and names
`"nonexistent-skill"`.

---

### TC-176-07, `List` returns sorted names

- **Requirement:** REQ-176-04
- **Level:** L2

**Step:** Register three skills in a deliberately non-alphabetical order
(`"zzz"`, `"aaa"`, `"mmm"`). `List()`.

**Expected output:** `[]string{"aaa", "mmm", "zzz"}` (sorted, deterministic,
matching `recipe.ListRecipes`'s existing documented sort-order convention).

---

### TC-176-08, `SelectForGoal` matches by keyword

- **Requirement:** REQ-176-05
- **Level:** L2 (pure function, no package-global state, an explicit `map[string]Manifest` argument)

**Step:** `registry := map[string]Manifest{"coding-agent": {Name:
"coding-agent", Description: "contribute code to a target repo"}, "docs-fix":
{Name: "docs-fix", Description: "fix documentation drift"}}`.
`SelectForGoal("fix the drift in the README", registry, "coding-agent")`
(third arg: the fallback default name).

**Expected output:** returns the `"docs-fix"` manifest (keyword `"drift"`
matched its description), NOT the fallback.

---

### TC-176-09, `SelectForGoal` falls back to the default on no match

- **Requirement:** REQ-176-05
- **Level:** L2

**Step:** `SelectForGoal("do something entirely unrelated", registry,
"coding-agent")` (same registry as TC-176-08, no keyword overlap with either
entry's name/description).

**Expected output:** returns the `"coding-agent"` manifest (the configured
fallback), `err == nil` (a fallback is a valid, non-error outcome, matching
`orchestrator.DefaultRecipeName`'s existing "free-form goal -> default recipe"
convention, ADR 046 §1, which this function's fallback semantics deliberately
mirror).

---

### TC-176-10, leaf isolation (F-016)

- **Requirement:** REQ-176-06
- **Level:** L3 (fitness)

**Step:** `make fitness-skill-isolation`

**Expected output:** `PASS fitness-skill-isolation: internal/skill import
graph contains no agent-builder/internal/* dependency.` (wording at executor's
discretion, mirroring F-012/F-015's message shape).

---

### TC-176-11, no existing package behavior changes

- **Requirement:** REQ-176-07
- **Level:** L2/L3 (regression)

**Step:**
```
go test -race -count=1 ./internal/recipe/... ./internal/orchestrator/...
make check
```

**Expected output:** all `ok`, byte-identical to pre-task; `DefaultRecipeName`,
`SelectRecipe`, and every existing recipe registration completely unaffected
(this task adds a new, unconnected package).

---

### TC-176-12, full regression

- **Requirement:** all
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/skill/...
make check
```

**Expected output:** all `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L3, this task ships an unconnected seam with no
  runtime caller (task 177 provides the first live wiring and reaches a higher
  level there); L2 unit tests plus the new F-016 fitness check are the correct
  and sufficient bar for this task, matching how task 167's `internal/runstore`
  (also an unconnected-at-first leaf) was verified.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/skill/...
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.` (including new `fitness-skill-isolation`).

## Out of scope

- Migrating `coding-agent` (or any recipe) into a registered skill (task 177).
- Wiring `SelectForGoal` into the orchestrator's actual dispatch path (task 177).
- LLM-driven skill selection (v1's `SelectForGoal` is a deterministic
  keyword/fallback rule only, matching the "defer premature decisions" design
  principle, a follow-on can swap the selection RULE without changing the
  function signature).
- The secure skill-writing loop (`AGENTS.md`'s "Self-improvement is secure
  skill-writing" framing, a materially larger, separate capability, not this
  task's scope).
