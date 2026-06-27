# Test spec — Task 082: agent-builder worker recipe (code-authoring worker)

**Linked task:** `docs/tasks/backlog/082-agent-builder-worker-recipe.md`
**Written:** 2026-06-27
**Status:** stub — blocked by Cluster A (tasks 076–079)

## Context

The "agent-builder worker" is a first-party recipe whose job is to author a new
agent (a new recipe/Go code) and hand it back. It is a worker-tier task, not an
orchestrator capability. Code-authoring inherits the full worker safety model:
code-scanner + dep-scan + runtime gate-existence assertion + containment + policy +
human approval before the generated agent is dispatched + audit trail.

The base case (recipe #1, the coding agent) already writes code; this recipe is the
specialized path for authoring a *new agent definition*. It differs from the coding
agent recipe in its goal source (an "author agent recipe for X" goal), its executor
prompt (a code-generation system prompt producing a Go recipe), and its gate (which
adds the runtime gate-existence assertion on the generated recipe output in addition
to standard code-scanner/dep-scan).

This task is **blocked by Cluster A** (recipe seam must be stable; the gate-existence
assertion from task 078 must exist before this recipe can require it).

**Detailed task shape is deferred** pending those prerequisites.

## Requirements coverage (preliminary)

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-082-01 | Recipe registered as "agent-builder-worker"; satisfies Recipe interface        | TC-082-01  |
| REQ-082-02 | Gate runs code-scanner + dep-scan on generated code                            | TC-082-02  |
| REQ-082-03 | Gate applies runtime gate-existence assertion on generated recipe output        | TC-082-03  |
| REQ-082-04 | Human approval is required before generated agent is dispatched                 | TC-082-04  |
| REQ-082-05 | Generated agent code is recorded in audit trail with provenance                 | TC-082-05  |
| REQ-082-06 | Worker runs inside exec-sandbox (containment); no special orchestrator path     | TC-082-06  |

## Pre-implementation checklist

- [ ] Task 076–079 merged (recipe seam + gate-existence assertion stable)
- [ ] Task 078 merged (runtime gate-existence assertion implemented)
- [ ] code-scanner and dep-scan gate step integration confirmed (already in task 005–006)
- [ ] All test cases refined into full inputs/expected-outputs

---

## Test cases (stubs)

### TC-082-01 — SelectRecipe("agent-builder-worker") returns a non-nil Recipe

- **Requirement:** REQ-082-01
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** `recipe.SelectRecipe("agent-builder-worker")`

**Expected output:**
- Returns `(Recipe, nil)`.
- The recipe's `GateFactory` produces a non-nil gate (gate-existence assertion passes).
- `recipe.ListRecipes()` includes `"agent-builder-worker"`.

---

### TC-082-02 — Gate runs code-scanner + dep-scan on generated code

- **Requirement:** REQ-082-02
- **Level:** L2 (unit test with stub gate tool)
- **Status:** stub

**Input:** A fixture directory containing a Go file with a known-bad pattern (e.g.
a hardcoded credential string or a known CVE dependency).

**Expected output:**
- `Verdict.OK == false`.
- `Verdict.Failures` names the scanner finding.
- Neither code-scanner nor dep-scan is skipped.

---

### TC-082-03 — Gate applies runtime gate-existence assertion on generated recipe output

- **Requirement:** REQ-082-03
- **Level:** L2 (unit test)
- **Status:** stub

**Input:** A fixture "generated recipe" Go file with no gate binding.

**Expected output:**
- The gate's gate-existence check fires on the generated output.
- `Verdict.OK == false`, naming the missing gate.

**Input (positive):** A fixture generated recipe with a valid gate binding.
**Expected output:** Gate passes; `Verdict.OK == true` (modulo other checks).

---

### TC-082-04 — Human approval is required before generated agent is dispatched

- **Requirement:** REQ-082-04
- **Level:** L2 (unit test with stub policy-engine)
- **Status:** stub

**Input:** The orchestrator (task 081) attempts to dispatch a generated agent.
Policy-engine stub returns `require_approval`.

**Expected output:**
- The generated agent is NOT dispatched.
- Human approval is solicited via the channel.

---

### TC-082-05 — Audit trail records generated code and provenance

- **Requirement:** REQ-082-05
- **Level:** L2 (unit test with FakeSink)
- **Status:** stub

**Input:** A successful code-authoring worker run that produces a valid recipe file.

**Expected output:**
- At least one `audit.AuditEvent` is emitted with the generated file path and
  content hash in its `EventDetail`.
- The `Refs` field or equivalent records the goal that prompted the generation.

---

### TC-082-06 — Worker runs inside exec-sandbox (containment)

- **Requirement:** REQ-082-06
- **Level:** L2 (structural test — no special code path)
- **Status:** stub

**Input:** `runtime.Run` with `recipe="agent-builder-worker"`.

**Expected output:**
- The runtime assembler takes the same `exec-sandbox.Run()` path as any other recipe.
- No special "code-authoring" code path exists in `internal/runtime` or
  `internal/orchestrator` (verified by diffing those packages).

---

## Verification plan (preliminary)

- **Highest level achievable:** L2 (unit tests). An L5 end-to-end code-authoring
  run is out of scope for this task.
- **L2 harness command (to be confirmed):**
  ```
  go test -count=1 ./internal/recipe/agentbuilderworker/...
  ```
  Expected: `ok`.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Open questions

1. What is the "generated recipe" output format? A `.go` source file that registers
   a recipe? A struct literal? The implementation must decide this before the gate-
   existence check can be implemented.
2. Does the code-authoring worker produce a PR (like the coding-agent recipe) or
   deliver the recipe back to the orchestrator over agent-mesh? The ADR says "hand
   it back" but the mechanism is unspecified.

## Out of scope

- Multi-agent dispatch (task 085) — the worker runs one at a time under this task.
- The orchestrator dispatching the generated agent (task 081 → approval → dispatch).
- The agent-mesh transport for delivering the generated recipe back (task 083).
