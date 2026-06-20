# Task 074: F-006 fitness — `fitness-policy-isolation`

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** completed

## Goal

Add a `make fitness-policy-isolation` target (fitness rule F-006) asserting:

1. **`internal/policy` is a leaf** — `go list -deps ./internal/policy/...` contains no
   `github.com/tkdtaylor/agent-builder/internal/` paths other than `internal/policy`
   itself. This prevents the package from importing `internal/runtime`, `internal/sandbox`,
   etc., which would allow in-process decision paths that bypass the out-of-process rule.

2. **`internal/runtime` reaches policy-engine only over IPC** — `go list -deps ./internal/runtime/...`
   must NOT contain `github.com/tkdtaylor/policy-engine` (the block's Go module path).
   If this import appears, the agent can call the block's `Decide()` function in-process,
   defeating the security model entirely.

Wire the target into `make fitness` and document it as F-006 in
`docs/spec/fitness-functions.md` with a source-of-truth link to ADR 038.

Mirror F-005 (`fitness-audit-isolation`, task 042) exactly — same shell/`go list`
Makefile mechanism, same PASS/FAIL format, same negative-evidence convention, same
fitness-umbrella wiring.

## Context

The policy-engine block's load-bearing security invariant (from `CLAUDE.md`):

> "Out-of-process only. policy-engine runs as its own process; the agent reaches it
> only over IPC (Unix socket). Never expose an in-process `decide` the agent could
> call to flip its own decision."

Agent-builder enforces this by calling `decide` over a Unix socket (`internal/policy`
client). The fitness check makes this invariant machine-verifiable and blocking —
a regression (accidentally importing the policy-engine Go module, or adding a reverse
import from `internal/policy` into `internal/runtime`) fails `make check` immediately.

**Pattern to mirror:** `Makefile` `fitness-audit-isolation` (task 042, lines ~133–160)
— `go list -deps` piped through `grep` for forbidden path segments, PASS/FAIL line,
wired into the `fitness:` umbrella and `.PHONY`. The negative case follows the
established convention: demonstrated against a synthetic import list, not committed.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                                     | Priority  |
|------------|---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-074-01 | `make fitness-policy-isolation` asserts `go list -deps ./internal/policy/...` contains no `agent-builder/internal/` paths other than `internal/policy` itself; prints a PASS line on the clean tree. | must have |
| REQ-074-02 | The same target asserts `go list -deps ./internal/runtime/...` contains no `github.com/tkdtaylor/policy-engine` path; PASS line names both checks. | must have |
| REQ-074-03 | A forbidden import (synthetic) makes the target exit non-zero with a FAIL line naming the offending package. Demonstrated per the F-001..F-005 negative-evidence convention. | must have |
| REQ-074-04 | The target is a prerequisite of `make fitness` (and `.PHONY`); `docs/spec/fitness-functions.md` gains an F-006 row (rule, asserts, threshold `0 violations`, check command, severity `block`, one-line why) with a source-of-truth link to ADR 038; `make check` exits 0. | must have |

## Readiness gate

- [x] Test spec `074-fitness-policy-isolation-test-spec.md` exists (written first)
- [ ] Task 073 (require_approval + audit_emit) merged and verified — the `internal/policy`
  package and its runtime wiring must exist for the import-graph assertions to be
  meaningful
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-074-01] TC-074-01: `make fitness-policy-isolation` exits 0 and prints `PASS fitness-policy-isolation: …` on the clean tree; checks `internal/policy` leaf
- [ ] [REQ-074-02] TC-074-02: same target also asserts `internal/runtime` does not import the policy-engine Go module; PASS line states both confirmations
- [ ] [REQ-074-03] TC-074-03: a synthetic forbidden import produces non-zero exit and a FAIL line naming the offending package
- [ ] [REQ-074-04] TC-074-04: `fitness-policy-isolation` in `fitness:` prerequisite + `.PHONY`; F-006 row in `fitness-functions.md` with ADR 038 link; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L5 — the fitness check's observable surface is its own
  stdout. Running `make fitness-policy-isolation` and seeing PASS (plus demonstrated FAIL
  on a synthetic violation) is the verification. Mirrors the F-005 pattern exactly.
- **Harness command:**
  ```
  make fitness-policy-isolation
  make fitness
  make check
  ```
  Expected:
  - `make fitness-policy-isolation` → `PASS fitness-policy-isolation: …` (exit 0).
  - `make fitness` → `All fitness checks passed.` (includes F-006).
  - `make check` → `All checks passed.`
- **Negative evidence (TC-074-03):**
  Demonstrate against a synthetic import list that the script produces the correct FAIL
  output and non-zero exit. Document the output in the `Verified by` column per the
  F-001..F-005 convention.

## Out of scope

- Re-implementing or modifying F-005 (`fitness-audit-isolation`) — F-006 is independent.
- Runtime tracing or dynamic inspection of IPC calls (static import-graph is sufficient).
- Changing any `internal/policy`, `internal/runtime`, or audit code — this task only
  adds the Makefile target and `fitness-functions.md` row.
- OPA/Cedar evaluator import check (the block's v1 adds OPA inside its own module;
  agent-builder never imports it).

## Dependencies

- Task 073 (require_approval + audit_emit) — both the `internal/policy` package and its
  wiring into `internal/runtime` must exist for the import-graph checks to be meaningful.
  The leaf check is trivially true on an empty package; the runtime-block-import check
  is only interesting once the real wiring is in place.
- ADR 038 (task 070) — must be merged so the F-006 `fitness-functions.md` row can
  reference it as the source of truth.
