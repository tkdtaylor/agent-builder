# Test spec — Task 074: F-006 fitness — policy-isolation (`fitness-policy-isolation`)

**Linked task:** `docs/tasks/backlog/074-fitness-policy-isolation.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task adds fitness rule F-006 (`fitness-policy-isolation`) asserting that
`internal/policy` is a leaf that reaches the policy-engine block only over IPC
(`os/exec` or net.Dial to the Unix socket) — no in-process `decide`, and no import
edge from `internal/policy` into `internal/runtime` or any other agent-builder
package that would let the agent flip its own decision.

The invariant being asserted matches the load-bearing security rule in
`policy-engine/CLAUDE.md`: "Out-of-process only. policy-engine runs as its own
process; the agent reaches it only over IPC (Unix socket). Never expose an in-process
decide the agent could call to flip its own decision."

The pattern mirrors F-005 (`fitness-audit-isolation`, task 042) exactly:
- Same Makefile mechanism: `go list -deps` piped through `grep` / `awk`.
- Same PASS/FAIL exit-code and printed line format.
- Same negative-evidence convention: demonstrate the FAIL path against a synthetic
  import list rather than committing a bad import.
- Same wiring: new target added as a prerequisite of `make fitness` (and `.PHONY`);
  documented in `docs/spec/fitness-functions.md` as F-006 with a source-of-truth link
  to ADR 038.

**Two-sided check (mirroring F-005's dual assertion):**

1. **`internal/policy` is a leaf** — `go list -deps ./internal/policy/...` must not
   contain any `agent-builder/internal/` path other than `internal/policy` itself.
   This prevents `internal/policy` from importing `internal/runtime`, `internal/sandbox`,
   `internal/vault`, `internal/audit`, etc. (which would allow the agent's own code to
   self-authorize by calling an in-memory decision path).

2. **`internal/runtime` reaches `internal/policy` only through the IPC seam** — the
   import graph of `internal/runtime` includes `internal/policy` (the client), but
   that client does NOT import `policy-engine`'s Go package (the block is a binary,
   not a Go module). Specifically: `go list -deps ./internal/runtime/...` must NOT
   contain a path matching `github.com/tkdtaylor/policy-engine` (the block's module
   path). If such an import appears, the boundary has been violated and the block's
   in-process `Decide()` is reachable.

## Requirements coverage

| Req ID     | Test cases                  | Covered? |
|------------|-----------------------------|----------|
| REQ-074-01 | TC-074-01                   | yes      |
| REQ-074-02 | TC-074-02                   | yes      |
| REQ-074-03 | TC-074-03                   | yes      |
| REQ-074-04 | TC-074-04                   | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-074-01 — `fitness-policy-isolation` passes on the clean tree (internal/policy is a leaf)

- **Requirement:** REQ-074-01
- **Level:** L5 (fitness check; `make fitness-policy-isolation` on the clean tree
  after tasks 071–073 land)
- **Test file / harness:** `Makefile` target `fitness-policy-isolation`

**Input:** `make fitness-policy-isolation` on the clean tree (tasks 071/072/073 merged).

**Expected output:**
- Target exits 0.
- Prints a PASS line, e.g.:
  ```
  PASS fitness-policy-isolation: internal/policy import graph contains no other internal packages, and internal/runtime does not import the policy-engine block as a Go module.
  ```
  (Exact wording is implementation-defined; it must include both halves of the check:
  leaf assertion + no-block-import assertion.)

**Edge cases:**
- The check covers `go list -deps ./internal/policy/...` (the leaf itself) and asserts
  no `agent-builder/internal/` paths other than `internal/policy`.
- The check also inspects `go list -deps ./internal/runtime/...` and asserts no
  `github.com/tkdtaylor/policy-engine` path (block module path).

---

### TC-074-02 — The check also guards the runtime's transitive graph (no block-module import)

- **Requirement:** REQ-074-02
- **Level:** L5 (fitness check; same `make fitness-policy-isolation` target)

**Input:** `make fitness-policy-isolation` inspecting `go list -deps ./internal/runtime/...`.

**Expected output:**
- Target asserts that wiring `internal/policy` into `internal/runtime` (task 072) did
  NOT introduce a `github.com/tkdtaylor/policy-engine` Go module import.
- PASS prints both the policy-leaf and runtime-graph confirmations.

**Why this is distinct from TC-074-01:** TC-074-01 ensures `internal/policy` stays a
leaf (no circular deps back into runtime). TC-074-02 ensures `internal/runtime` reaches
policy-engine exclusively over the `os/exec`/socket seam — preventing accidental Go
module adoption of the policy-engine block.

---

### TC-074-03 — A forbidden import in `internal/policy` makes the check fail (negative)

- **Requirement:** REQ-074-03
- **Level:** L5 (negative evidence; demonstrated without committing a bad import)
- **Pattern:** same convention as F-001..F-005 negative evidence

**Input:** A temporary synthetic import — either a documented manual demonstration
against a synthetic import list or a scripted check in the test harness — simulating
`internal/policy` importing `internal/runtime`.

**Expected output:**
- `make fitness-policy-isolation` exits non-zero.
- Prints a FAIL line naming the forbidden package path.

**Evidence format (matching F-005 convention):**
```
FAIL fitness-policy-isolation: internal/policy imports forbidden agent-builder/internal package(s): github.com/tkdtaylor/agent-builder/internal/runtime
```

**Note:** The negative is demonstrated per the established F-001..F-005 convention:
a synthetic import list is tested against the script logic, not permanently committed.
The verify commit records the FAIL output as evidence.

---

### TC-074-04 — `fitness-policy-isolation` is wired into `make fitness`; F-006 documented in spec

- **Requirement:** REQ-074-04
- **Level:** L5 (Makefile inspection + spec grep)

**Assertions:**
- `fitness-policy-isolation` appears in the `fitness:` prerequisite list in the
  `Makefile` (so it runs as part of `make check`).
- `fitness-policy-isolation` appears in the `.PHONY` list.
- `docs/spec/fitness-functions.md` has an F-006 row with:
  - Rule description naming the out-of-process invariant.
  - Asserts column: `"internal/policy is a leaf; runtime reaches policy-engine only over IPC"`.
  - Threshold: `0 violations`.
  - Check command: `make fitness-policy-isolation`.
  - Severity: `block`.
  - A one-line *why* (e.g. "policy-engine must stay out-of-process so the agent
    cannot self-grant by calling an in-process decide").
  - A source-of-truth link to ADR 038.
- `make fitness` prints the F-006 PASS line among the other fitness check lines.
- `make check` → `All checks passed.` (all existing fitness checks plus F-006 pass).

---

## Verification plan

- **Highest level achievable:** L5 — the fitness check's observable surface is its own
  stdout; running it and seeing PASS (plus a demonstrated FAIL on a synthetic violation)
  is the verification. Mirrors the F-005 pattern exactly.
- **L5 harness command:**
  ```
  make fitness-policy-isolation
  make fitness
  make check
  ```
  Expected:
  - `make fitness-policy-isolation` → `PASS fitness-policy-isolation: …` (exit 0).
  - `make fitness` → `All fitness checks passed.` (exit 0; F-006 in the list).
  - `make check` → `All checks passed.` (exit 0).
- **Negative evidence (TC-074-03):**
  Demonstrate against a synthetic import list that the script produces the correct FAIL
  output and non-zero exit. Document in the `Verified by` column.

## Out of scope

- Re-implementing F-005 (`fitness-audit-isolation`) — F-006 is the policy-specific
  complement; they are independent fitness functions.
- Dynamic runtime checks or runtime tracing of the IPC calls (static import-graph is
  sufficient for the out-of-process invariant).
- Changing any `internal/policy` or `internal/runtime` code — this task only adds the
  guard.
- OPA/Cedar evaluator import check (the policy-engine v1 path adds OPA as a Go
  dependency inside the policy-engine block's own module; the agent-builder side never
  imports it directly).
