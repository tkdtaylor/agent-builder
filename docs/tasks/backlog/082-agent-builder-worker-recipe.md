# Task 082: agent-builder worker recipe (code-authoring worker)

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Implement the "agent-builder-worker" recipe — a first-party code-authoring worker
recipe whose job is to author a new agent definition (a new Go recipe) and hand it
back. It runs in exec-sandbox like any other worker and inherits the full worker
safety model: code-scanner + dep-scan + runtime gate-existence assertion on generated
output + containment + policy + human approval before any generated agent is dispatched
+ audit-trail provenance.

## Context

ADR 042: "code-authoring is a worker task, not an orchestrator capability." The
agent-builder worker is the named recipe for this. The base case (the first
agent-builder worker) is this first-party Go recipe; it can then be used to generate
further recipes. Critically, the orchestrator never calls this recipe's executor
directly — it dispatches this worker the same way it dispatches any other worker.

**Blocked by Cluster A (tasks 076–079).** The recipe seam and gate-existence
assertion must be stable.

## Requirements

| Req ID     | Description                                                                                                                                              | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-082-01 | `SelectRecipe("agent-builder-worker")` returns a non-nil Recipe; gate-existence assertion passes for it. | must have |
| REQ-082-02 | Gate includes code-scanner + dep-scan steps on generated code; both are non-skippable. | must have |
| REQ-082-03 | Gate includes the runtime gate-existence assertion on the generated recipe output; a generated recipe with no gate is a gate failure. | must have |
| REQ-082-04 | Human approval via policy-engine `require_approval` is required before any generated agent is dispatched by the orchestrator. | must have |
| REQ-082-05 | At least one `audit.AuditEvent` with the generated file path and content hash is emitted during a successful run. | must have |
| REQ-082-06 | No special code path in `internal/runtime` or `internal/orchestrator` — the worker runs via the standard recipe→runtime→supervisor path. | must have |

## Readiness gate

- [x] Test spec `082-agent-builder-worker-recipe-test-spec.md` exists (written first)
- [ ] Tasks 076–078 merged (recipe seam + gate-existence assertion)
- [ ] Open questions in test spec resolved (generated output format; delivery mechanism)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-082-01] TC-082-01: `SelectRecipe("agent-builder-worker")` returns non-nil Recipe; gate-existence assertion passes; `ListRecipes()` includes it
- [ ] [REQ-082-02] TC-082-02: Gate rejects a fixture with a known-bad code-scanner pattern and a fixture with a known CVE dependency
- [ ] [REQ-082-03] TC-082-03: Gate rejects a generated fixture recipe with no gate binding; accepts a fixture with a valid gate
- [ ] [REQ-082-04] TC-082-04: Orchestrator stub dispatching a generated agent with `require_approval` → no dispatch, approval solicited
- [ ] [REQ-082-05] TC-082-05: `FakeSink` receives a `AuditEvent` containing the generated file path + content hash
- [ ] [REQ-082-06] TC-082-06: `git diff HEAD~1 -- internal/runtime/ internal/orchestrator/` is empty for this task

## Verification plan

- **Highest level achievable:** L2 (unit tests). An L5 end-to-end code-authoring
  run is deferred.
- **Harness command:**
  ```
  go test -count=1 ./internal/recipe/...
  make check
  ```
  Expected:
  - Unit tests → `ok`
  - `make check` → `All checks passed.`

## Out of scope

- Multi-agent dispatch (task 086).
- The orchestrator dispatching the generated agent (task 081 + 086).
- agent-mesh delivery of the generated recipe back (task 083).

## Dependencies

- Task 076 (recipe type + selector)
- Task 077 (runtime assembles from recipe)
- Task 078 (runtime gate-existence assertion — required for REQ-082-03)
- Task 079 (seam proven; confirms recipe approach is correct before building a third)
- Informs: tasks 083, 085 (the worker recipe is the main thing the orchestrator dispatches)
