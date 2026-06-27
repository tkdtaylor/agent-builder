# Test spec — Task 078: Runtime gate-existence assertion for generated recipes

**Linked task:** `docs/tasks/backlog/078-runtime-gate-existence-assertion.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 041 enforces gate-existence at compile time for human-authored Go recipes
(a recipe with no `GateFactory` won't compile). ADR 042 amends this: when a
code-authoring worker produces a recipe, the compile-time guarantee no longer
applies, so the assembler must apply a runtime assembly-time assertion that rejects
any recipe that binds no real, non-empty, blocking gate.

This task adds that assertion to `runtime.Run` (or the recipe assembler): before
constructing the supervisor, the assembler verifies that the recipe's `GateFactory`
produces a non-nil, non-pass-through gate. "A generated agent with no gate" must
remain unrepresentable at runtime.

## Requirements coverage

| Req ID     | Test cases           | Covered? |
|------------|----------------------|----------|
| REQ-078-01 | TC-078-01, TC-078-02 | yes      |
| REQ-078-02 | TC-078-03            | yes      |
| REQ-078-03 | TC-078-04            | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-078-01 — A recipe with a nil GateFactory is rejected before supervisor construction

- **Requirement:** REQ-078-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_gate_assert_test.go`

**Input:** Construct a `Recipe` by force (bypassing any compile-time guard) with
`GateFactory = nil`. Pass it to the runtime assembler.

**Expected output:**
- The assembler returns a non-nil error before any `sandbox.Create` or
  supervisor construction call.
- The error message contains `"gate"` and `"nil"` (or equivalent — exact wording
  is implementation-defined, but "no gate" must be named).
- No audit events are emitted (the run never starts).

---

### TC-078-02 — A recipe whose GateFactory returns a pass-through gate is rejected

- **Requirement:** REQ-078-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_gate_assert_test.go`

**Input:** Construct a `Recipe` whose `GateFactory` returns a `Gate` implementation
that always returns `gate.Verdict{OK: true}` without running any checks (a
"no-op" gate — the kind a malicious generated recipe might supply).

**Expected output:**
- The assembler detects that the gate is a known pass-through sentinel and rejects
  the recipe before dispatch.
- Error message names the gate as invalid.
- NOTE: if the implementation approach is a marker interface (`GateIsBlocking()`)
  rather than a pass-through detection heuristic, the test asserts that a gate
  missing the marker is rejected, not that every always-OK gate is rejected.

**Rationale:** This is the "soft" case — a gate that compiles but that never
actually blocks. Acceptable implementation strategies include: a required marker
method (`Blocks() bool`) that the fake gate omits; a registry of "real" gate types;
or a configuration-level flag on the recipe. The test asserts whichever mechanism
the implementation uses is actually checked before dispatch.

---

### TC-078-03 — A recipe with a real gate passes the assertion

- **Requirement:** REQ-078-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_gate_assert_test.go`

**Input:** The `coding-agent` recipe (which uses the production gate with real steps).

**Expected output:**
- The assembler accepts the recipe and proceeds to supervisor construction.
- `make check` on the clean tree passes (this is a regression guard: the existing
  coding-agent recipe must still be accepted after the assertion is added).

---

### TC-078-04 — Gate-existence assertion fires for every recipe path, not just generated ones

- **Requirement:** REQ-078-03
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/runtime_gate_assert_test.go`

**Input:** Register a test recipe via `SelectRecipe` (or inject directly) that
supplies no gate, and call `runtime.Run`.

**Expected output:**
- `Run` returns an error citing the missing gate before any dispatch, regardless of
  whether the recipe was "generated" or "human-authored".
- The assertion is not conditional on a `generated=true` flag — it applies uniformly.

**Rationale:** The compile-time guarantee already prevents nil-gate for recipes typed
at compile time. The runtime assertion is the last defense; it must not have an
escape hatch.

---

## Verification plan

- **Highest level achievable:** L2/L3 — the gate-existence assertion is a defensive
  pre-flight check inside `runtime.Run`; its observable surface is the error returned
  before dispatch. Unit tests covering the nil-gate and pass-through-gate rejection
  paths, plus the clean-tree acceptance path, are sufficient.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/runtime/... -run 'TestGateExistence'
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/runtime`
- **Regression guard:**
  ```
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPhase1EndToEndAcceptance'
  ```
  Expected: both pass (the real coding-agent gate passes the assertion).
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Changing the `Gate` interface in `internal/gate` — the assertion lives in the
  assembler, not in the gate package.
- Detecting all possible "weak" gate strategies (obfuscated always-OK implementations)
  — the assertion targets structural properties (nil, missing marker) not behavioral
  oracles.
- The second proof recipe (task 079) — this task only adds the assertion; the
  second recipe is what exercises it for a non-Go gate.
