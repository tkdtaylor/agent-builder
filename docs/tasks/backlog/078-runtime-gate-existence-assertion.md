# Task 078: Runtime gate-existence assertion for generated recipes

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Add a runtime assembly-time assertion to the recipe assembler in `internal/runtime`:
before constructing the supervisor, assert that the selected recipe's `GateFactory`
produces a non-nil, non-pass-through gate. Reject any recipe that binds no real
blocking gate — "a generated agent with no gate" must remain unrepresentable at
runtime.

## Context

ADR 041 enforced gate-existence at compile time because recipes are human-authored
Go (a nil `GateFactory` won't compile). ADR 042 amends this: recipes may now be
authored by a code-authoring worker, forfeiting the compile-time guarantee. The
assembler must compensate with a runtime check that fires before any dispatch, for
every recipe path (not just generated ones). This is the defense-in-depth layer.

## Requirements

| Req ID     | Description                                                                                                                                              | Priority  |
|------------|----------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-078-01 | The runtime assembler rejects any recipe whose `GateFactory` is nil or produces a pass-through/no-op gate before any `sandbox.Create` or supervisor construction call; error message names `"gate"` and the defect. | must have |
| REQ-078-02 | A recipe with a real, blocking gate passes the assertion without error; the clean-tree `make check` passes (regression guard). | must have |
| REQ-078-03 | The assertion is unconditional — it applies to every recipe path, regardless of whether the recipe is "generated" or "human-authored". No escape hatch. | must have |

## Readiness gate

- [x] Test spec `078-runtime-gate-existence-assertion-test-spec.md` exists (written first)
- [ ] Task 077 merged (`runtime.Run` assembles from recipe; the assembler exists)
- [ ] `make check` green before starting

## Acceptance criteria

- [ ] [REQ-078-01] TC-078-01: A recipe with `GateFactory = nil` (forced via test injection) → assembler returns error before any `sandbox.Create`; error contains `"gate"` and `"nil"` (or equivalent)
- [ ] [REQ-078-01] TC-078-02: A recipe with a pass-through gate (always-OK, no checks) → assembler detects and rejects it; error names the gate as invalid
- [ ] [REQ-078-02] TC-078-03: The `coding-agent` recipe passes the assertion; `TestPhase0EndToEndAcceptance` and `TestPhase1EndToEndAcceptance` still pass
- [ ] [REQ-078-03] TC-078-04: A test recipe with no gate, injected via `SelectRecipe`, is rejected by `runtime.Run` regardless of how it was registered

## Verification plan

- **Highest level achievable:** L2/L3 — the gate-existence assertion is a pre-flight
  check; its surface is the error returned before dispatch.
- **Harness command:**
  ```
  go test -count=1 ./internal/runtime/... -run 'TestGateExistence'
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  make check
  ```
  Expected:
  - Gate-existence tests → `ok`
  - e2e regression → both pass
  - `make check` → `All checks passed.`

## Out of scope

- Changing the `Gate` interface in `internal/gate`.
- Detecting all possible obfuscated always-OK gate strategies.
- The second proof recipe (task 079) — this task only adds the assertion.

## Dependencies

- Task 077 (runtime assembles from recipe) — the assembler must exist before the
  assertion can be added to it.
- Informs: task 079 (the assertion must pass for the docs-fix recipe's non-Go gate);
  task 082 (the agent-builder worker recipe uses this assertion on generated output).
