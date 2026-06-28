# Test spec — Task 082: agent-builder worker recipe (code-authoring worker)

**Linked task:** `docs/tasks/backlog/082-agent-builder-worker-recipe.md`
**Written:** 2026-06-27
**Expanded:** 2026-06-28 (open questions resolved by ADR 047)
**Status:** complete

## Context

The "agent-builder worker" is a first-party recipe whose job is to author a new
agent (a new recipe as a `.go` source file) and hand it back via the standard
branch/PR publish path. It is a worker-tier task, not an orchestrator capability.
Code-authoring inherits the full worker safety model: code-scanner + dep-scan +
runtime gate-existence assertion on generated recipe output + containment + policy +
human approval before the generated agent is dispatched + audit trail.

ADR 047 resolves the two open questions from the stub spec:
1. Output format: a `.go` source file that registers a recipe via the seam
   (config-taking factory binding a non-skippable gate, ADR 044 shape).
2. Delivery: standard worker publish path (branch/PR), same as any other worker.
   agent-mesh hand-back is task 083 (out of scope here).

This recipe is implemented as package `internal/recipe/agentbuilderworker`
following the same pattern as `internal/recipe/docsfix`.

## Requirements coverage

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-082-01 | Recipe registered as "agent-builder-worker"; satisfies Recipe interface        | TC-082-01  |
| REQ-082-02 | Gate runs code-scanner + dep-scan on generated code; both non-skippable        | TC-082-02  |
| REQ-082-03 | Gate applies runtime gate-existence assertion on generated recipe output        | TC-082-03  |
| REQ-082-04 | Human approval is required before generated agent is dispatched                 | TC-082-04  |
| REQ-082-05 | Generated agent code is recorded in audit trail with file path + content hash  | TC-082-05  |
| REQ-082-06 | Worker runs via standard recipe→runtime→supervisor path; no special code path  | TC-082-06  |

---

## Test cases

### TC-082-01 — SelectRecipe("agent-builder-worker") returns a non-nil Recipe

- **Requirement:** REQ-082-01
- **Level:** L2 (unit test)
- **Package:** `internal/recipe/agentbuilderworker`

**Input:** `recipe.SelectRecipe("agent-builder-worker")`

**Expected output:**
- Returns `(Recipe, nil)` — no error.
- `recipe.Name == "agent-builder-worker"`.
- `recipe.GoalSourceFactory != nil`.
- `recipe.GateFactory != nil`.
- `recipe.ResultSinkFactory != nil`.
- `recipe.GateFactory()` returns a non-nil gate that implements `gate.Blocker` and
  `gate.Blocker.Blocks() == true` (i.e. the gate-existence assertion passes).
- `recipe.ListRecipes()` contains `"agent-builder-worker"`.

---

### TC-082-02 — Gate rejects known-bad code-scanner and dep-scan fixtures

- **Requirement:** REQ-082-02
- **Level:** L2 (unit test with fake tool binaries in PATH)
- **Package:** `internal/recipe/agentbuilderworker`

**Sub-test A — code-scanner detects a flagged pattern:**

Input: A fixture directory containing a `.go` file with a flagged credential
pattern (e.g. `func ReadTokenFile() {}`). A fake `code-scanner` binary is
installed on PATH that outputs `MALWARE credential pattern found` and exits 1.
A fake `dep-scan` binary exits 0 (so the test isolates the code-scanner step).

Expected output:
- `Verdict.OK == false`.
- At least one `StepResult` has `Name == "code-scanner"` and `OK == false`.
- The failing `StepResult.Output` contains the word `"MALWARE"` or `"credential"`.

**Sub-test B — dep-scan finds a CVE:**

Input: A fixture directory with a `go.sum` file and a `.go` file. A fake
`code-scanner` binary exits 0 (clean). A fake `dep-scan` binary that outputs
`CVE-2026-0001 HIGH vulnerable module` and exits 1.

Expected output:
- `Verdict.OK == false`.
- At least one `StepResult` has `Name == "dep-scan"` and `OK == false`.
- The failing `StepResult.Output` contains `"CVE-2026-0001"`.

**Assertion on non-skippability:**

Structural: the `AgentBuilderWorkerGate` type must not expose `Skip`, `Bypass`,
`skipScan`, `skipVerify`, `bypassVerify`, `noVerify`, or any variant (this is
also verified by the `make fitness-gate-blocking` fitness check on the production
code path). No `Skip` or `Bypass` method exists on the gate or its factory.

---

### TC-082-03 — Gate applies gate-existence assertion on generated recipe output

- **Requirement:** REQ-082-03
- **Level:** L2 (unit test)
- **Package:** `internal/recipe/agentbuilderworker`

The gate includes a `GeneratedGateExistenceStep` that inspects the generated
`.go` output for a `GateFactory` binding. This step runs against the generated
output directory using static text inspection (it does not compile the generated
code — it checks for a non-nil gate binding pattern in the source text).

**Sub-test A — generated recipe with no gate binding (negative case):**

Input: A fixture directory containing a single `.go` file that registers a
recipe with no `GateFactory` field set (or with `GateFactory: nil`). A fake
`code-scanner` exits 0; fake `dep-scan` exits 0 (so the gate-existence step
runs to completion).

Expected output:
- `Verdict.OK == false`.
- At least one `StepResult` has `Name == "generated-gate-existence"` and
  `OK == false`.
- The failing `StepResult.Output` contains `"GateFactory"` or `"gate"`.

**Sub-test B — generated recipe with a valid gate binding (positive case):**

Input: A fixture directory containing a single `.go` file that registers a
recipe with a non-nil `GateFactory` binding (e.g. assigns a function literal
or references a gate factory function). A fake `code-scanner` exits 0; fake
`dep-scan` exits 0.

Expected output:
- The `"generated-gate-existence"` step result has `OK == true`.
- `Verdict.OK == true` (all three steps pass).

---

### TC-082-04 — Human approval is required before generated agent is dispatched

- **Requirement:** REQ-082-04
- **Level:** L2 (unit test with stub policy decision)
- **Package:** `internal/recipe/agentbuilderworker`

The recipe's goal source includes a `PolicyDecision` field that a caller sets to
simulate the policy engine returning `require_approval`. When
`PolicyDecision == "require_approval"`, the goal source must NOT yield a
dispatchable task — it returns `(Task{}, false, nil)` and records the
approval-solicitation fact in the `ApprovalSolicited` field.

This test uses an ORCHESTRATOR STUB — the real orchestrator is task 081. The
stub simulates "policy engine returned require_approval → goal source consulted
→ no dispatch".

**Input:**
- Create an `AgentBuilderWorkerGoalSource` with `PolicyDecision: "require_approval"`.
- Call `Next()`.

**Expected output:**
- `ok == false` (no task yielded).
- `err == nil` (not an error — approval required is not a failure).
- `goalSource.ApprovalSolicited == true`.

---

### TC-082-05 — Audit trail records generated file path and content hash

- **Requirement:** REQ-082-05
- **Level:** L2 (unit test with FakeSink)
- **Package:** `internal/recipe/agentbuilderworker`

The recipe provides an `EmitAuditEvent` helper that emits one `audit.AuditEvent`
with the generated file path and SHA-256 content hash in its `Detail` fields.

**Input:**
- Call `EmitAuditEvent(sink, taskID, runID, generatedFilePath, fileContents)` with:
  - `sink`: a `*audit.FakeSink`
  - `taskID`: `"task-082-test"`
  - `runID`: `"run-082-test"`
  - `generatedFilePath`: `"/tmp/generated/recipe.go"` (or a real temp path)
  - `fileContents`: `[]byte("package recipe\n// generated\n")`

**Expected output:**
- `sink.Events()` contains exactly one event.
- `event.Action == audit.ActionPublish`.
- `event.TaskID == "task-082-test"`.
- `event.RunID == "run-082-test"`.
- `event.Detail.Branch == generatedFilePath` (the generated file path, stored in
  the Branch field as the "artifact path" — closest available field in the current
  AuditEvent shape; see note below).
- `event.Detail.Remote` contains the SHA-256 hex digest of `fileContents`
  (stored in the Remote field as the "content hash" — closest available field
  in the current AuditEvent shape; see note below).

Note: `audit.EventDetail` does not currently have a `GeneratedFilePath` or
`ContentHash` field. Per ADR 047 point 3, the audit event records the generated
file path + content hash. With the current `EventDetail` shape we repurpose
`Branch` for the file path and `Remote` for the content hash, documented with
comments at the call site. This is intentional — the `EventDetail` type would
need extension to add dedicated fields, which is a follow-on concern. The
`FakeSink` allows asserting these values directly.

---

### TC-082-06 — Worker runs via standard recipe→runtime→supervisor path

- **Requirement:** REQ-082-06
- **Level:** structural (git diff check)

**Input:** `git diff HEAD~1 -- internal/runtime/ internal/orchestrator/`

(After the task-082 commit is made, the diff against the prior commit should be
empty for those two directories.)

**Expected output:**
- The diff is empty — no files in `internal/runtime/` or `internal/orchestrator/`
  were modified.

This test is validated by the executor at commit time (see "REQ-082-06 empty-diff
confirmation" in the task report).

---

## Verification plan

- **Highest level achievable:** L2 (unit tests).
- **L2 harness command:**
  ```
  go test -count=1 ./internal/recipe/agentbuilderworker/...
  ```
  Expected: `ok  github.com/tkdtaylor/agent-builder/internal/recipe/agentbuilderworker`.

- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

- **Fitness sub-checks:**
  - `make fitness-gate-blocking` — verifies no skip/bypass in agentbuilderworker
  - `make fitness-supervisor-isolation` — verifies no executor/LLM imports drag in

## Out of scope

- Multi-agent dispatch (task 085/086).
- The orchestrator dispatching the generated agent (task 081).
- The agent-mesh transport for delivering the generated recipe back (task 083).
- Compiling or running the generated `.go` recipe (the gate does static text
  inspection only; real compilation would require the generated code to be in a
  valid Go module, which is out of scope for this task).
