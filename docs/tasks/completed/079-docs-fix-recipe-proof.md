# Task 079: Docs-fix recipe (second proof recipe)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Implement the "docs-fix" recipe (`SelectRecipe("docs-fix")`) — the second
deliberately-trivial recipe required by ADR 041 to prove the agent-recipe seam is
genuine. The recipe differs from the coding agent in all four IO seams (goal source,
executor prompt, gate, result sink is reused but the others differ), and adding it
must require **zero changes to `internal/runtime` or `internal/supervisor`**. That
zero-change constraint is the seam self-test: if `runtime` must change, ADR 041 has
failed.

## Context

ADR 041: "If a docs-fix recipe cannot be expressed without touching `runtime`
internals, the seam is wrong and this ADR has failed its own test." The docs-fix
recipe's gate uses a non-Go predicate (a markdown linter and/or link-checker + the
existing code-scanner), proving that the gate seam accepts purpose-specific predicates
beyond the Go-tooling suite.

## Requirements

| Req ID     | Description                                                                                                                                                                       | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-079-01 | `SelectRecipe("docs-fix")` returns a non-nil Recipe whose `GateFactory` produces a gate of a distinct type from the coding-agent gate; `ListRecipes()` includes `"docs-fix"`. | must have |
| REQ-079-02 | The docs-fix gate runs a markdown linter (and/or link-checker) + code-scanner; it does NOT invoke `go build`, `go test`, or `golangci-lint`; PASS on a well-formed `.md` fixture, FAIL on a malformed one. | must have |
| REQ-079-03 | The docs-fix recipe shares the same block-wiring config fields as the coding-agent recipe (exec-sandbox, vault, policy, audit); it does not implement its own containment. | must have |
| REQ-079-04 | The seam self-test: `git diff HEAD~1 -- internal/runtime/ internal/supervisor/` is empty for this task's commit — zero lines changed in those directories. | must have |

## Readiness gate

- [x] Test spec `079-docs-fix-recipe-proof-test-spec.md` exists (written first)
- [ ] Task 076 merged (recipe type + selector)
- [ ] Task 077 merged (runtime assembles from recipe)
- [ ] Task 078 merged (runtime gate-existence assertion)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-079-01] TC-079-01: `SelectRecipe("docs-fix")` returns non-nil Recipe with a distinct gate type; `ListRecipes()` includes `"docs-fix"`
- [ ] [REQ-079-02] TC-079-02: Docs-fix gate returns PASS on a well-formed `.md` fixture and FAIL on a malformed `.md` fixture; no `go build`/`go test`/`golangci-lint` subprocess is spawned
- [ ] [REQ-079-03] TC-079-03: `go list -deps ./internal/recipe/docsfix/...` does not contain `internal/sandbox` directly; block-wiring fields match those of the coding-agent recipe
- [ ] [REQ-079-03] TC-079-04: Runtime gate-existence assertion (task 078) passes for the docs-fix recipe
- [ ] [REQ-079-04] TC-079-05: `git diff HEAD~1 -- internal/runtime/ internal/supervisor/` is empty (seam self-test; recorded in verify commit)

## Verification plan

- **Highest level achievable:** L3 — the docs-fix gate's runtime surface (it runs
  a linter against a fixture directory) is exercised by unit tests.
- **Harness command:**
  ```
  go test -count=1 ./internal/recipe/...
  git diff HEAD~1 -- internal/runtime/ internal/supervisor/
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/recipe`
  - Diff → empty (zero lines changed in runtime/supervisor)
  - `make check` → `All checks passed.`

## Out of scope

- A live end-to-end docs-fix run against a real target repository.
- A real doc-lint result scanner as the goal source (a hardcoded "fix this file"
  goal is acceptable for the proof).
- Changing `internal/gate` — the docs-fix gate is a new implementation behind the
  existing `Gate` interface.

## Dependencies

- Task 076 (recipe type + selector)
- Task 077 (runtime assembles from recipe)
- Task 078 (runtime gate-existence assertion — the docs-fix recipe must pass it)
- Informs: tasks 080–086 (the recipe seam is now proven with two recipes; downstream
  clusters can confidently build on it)
