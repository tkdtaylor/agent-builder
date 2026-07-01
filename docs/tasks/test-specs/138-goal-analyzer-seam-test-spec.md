# Test spec — Task 138: GoalAnalyzer seam + HeuristicGoalAnalyzer

**Task:** `docs/tasks/backlog/138-goal-analyzer-seam.md`
**Relates to:** ADR 060 (goal analysis & routing), ADR 058 (Clarifier pattern this mirrors).

## Context

The orchestrator classifies a goal before routing it (ADR 060). This task adds the seam and its
deterministic default — no flow change yet (that is task 139). `GoalAnalyzer.Analyze(goal)` returns a
`GoalAnalysis{Kind, Complexity, Rationale}`. `Kind` picks the route (answer vs coding); `Complexity`
picks the brain-capability floor. The `HeuristicGoalAnalyzer` is a rule-based floor (no LLM, no IO),
mirroring `HeuristicClarifier`.

## Requirements

- **REQ-138-01** — Types exist: `GoalKind` with `KindAnswer` and `KindCoding`; `GoalComplexity` with
  `ComplexitySimple` and `ComplexityComplex`; `GoalAnalysis{Kind GoalKind; Complexity GoalComplexity;
  Rationale string}`; `GoalAnalyzer interface { Analyze(goal supervisor.Task) (GoalAnalysis, error) }`.
- **REQ-138-02** — `*HeuristicGoalAnalyzer` satisfies `GoalAnalyzer` (compile-time `var _`), constructed
  by `NewHeuristicGoalAnalyzer()`, non-nil.
- **REQ-138-03** — Classification rules (deterministic), in order:
  1. A goal naming a **repo/path** (`github.com`, `gitlab.com`, contains `/`, or ends `.git`) → `KindCoding`.
  2. Else a **question** (ends with `?`, or first word is an interrogative/aux: what/who/whom/whose/
     when/where/why/how/which/is/are/do/does/can/could/should/would) → `KindAnswer`.
  3. Else a goal starting with a **code-build verb** (build/implement/create/add/write/fix/refactor/
     debug/update/patch/remove/delete/make) → `KindCoding`.
  4. Else → `KindAnswer` (default; low blast radius per ADR 060).
- **REQ-138-04** — Complexity: `ComplexityComplex` when the spec is multi-line (contains `\n` with ≥2
  non-blank lines), OR word count > 30, OR contains a multi-step marker (` then `, ` and then `);
  else `ComplexitySimple`.
- **REQ-138-05** — `Rationale` is a non-empty short string naming the rule that fired.
- **REQ-138-06** — Boundary: `internal/orchestrator` imports no `internal/executor`; `make fitness`
  (F-010/F-014, supervisor isolation) stays green (the heuristic has no model seam).

## Test cases

- **TC-138-01** (`TestHeuristicAnalyzerSatisfiesSeam`) — REQ-138-02: compile-time assertion +
  constructor returns non-nil.
- **TC-138-02** (`TestHeuristicAnalyzerClassifiesKind`) — REQ-138-03, table-driven, hard-asserting
  `Kind` for each:
  - `"What is the capital of France?"` → `KindAnswer`
  - `"how does TCP work"` → `KindAnswer`
  - `"add a subtract function to github.com/x/calc"` → `KindCoding` (repo)
  - `"implement a REST endpoint"` → `KindCoding` (build verb)
  - `"refactor internal/foo/bar.go"` → `KindCoding` (path)
  - `"the capital of France"` → `KindAnswer` (default, no repo/verb)
- **TC-138-03** (`TestHeuristicAnalyzerClassifiesComplexity`) — REQ-138-04, hard-asserting `Complexity`:
  - `"What is the capital of France?"` → `ComplexitySimple`
  - a >30-word spec → `ComplexityComplex`
  - `"build the parser then wire it to the CLI"` → `ComplexityComplex` (multi-step marker)
- **TC-138-04** (`TestHeuristicAnalyzerRationaleNonEmpty`) — REQ-138-05: `Rationale != ""` for a
  representative answer goal and a representative coding goal.

## Non-vacuous / negative controls

- TC-138-02 asserts BOTH directions (a repo goal is coding; a bare interrogative is answer) so the
  rule ordering (repo before question before verb) is pinned, not just one branch.
