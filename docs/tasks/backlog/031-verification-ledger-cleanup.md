# Task 031: verification ledger cleanup

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** backlog

## Goal
Reconcile task status, spec-verifier results, current-state specs, and roadmap wording so the repository no longer presents code-merged work as fully verified.

## Context
- Tech stack: Go, Markdown docs
- Related ADRs: none expected
- Dependencies: 001, 002, 026, 027, and any runtime evidence available from task 030
- Audit finding: several task files say completed while `coverage-tracker.md` still says 🟡 or pending spec-verifier.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Run spec-verifier style assertion checks for remaining 🟡 tasks that already have L5/L6 harness evidence. | must have |
| REQ-002 | Keep tasks without required runtime evidence at 🟡 and make the blocker explicit in the task file and coverage tracker. | must have |
| REQ-003 | Rewrite `docs/spec/SPEC.md` and matching `docs/spec/` files as current-state snapshots, not target-state or pre-implementation text. | must have |
| REQ-004 | Ensure task file locations, task `**Status:**` lines, and tracker status symbols agree. | must have |

## Readiness gate
- [x] Test spec `031-verification-ledger-cleanup-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks are identified before edits

## Acceptance criteria
- [ ] [REQ-001] Tasks 001, 002, 026, and 027 either receive spec-verifier APPROVE evidence or remain 🟡 with a named missing assertion.
- [ ] [REQ-002] Tasks 014, 015, 016, and 021 are not marked ✅ unless task 030 produced the required runtime evidence.
- [ ] [REQ-003] `docs/spec/SPEC.md` no longer says the project is merely "pre-implementation" if the landed code has moved beyond that state.
- [ ] [REQ-004] A consistency check reports no mismatch between `docs/tasks/{backlog,active,completed}`, task status lines, and `coverage-tracker.md`.

## Verification plan
- **Highest level achievable:** L5 - documentation consistency harness plus targeted spec-verifier reports.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 ./tests/... && env PATH=/tmp/agent-builder-tools:$PATH make check
  ```
  Expected final assertion: `All checks passed.` plus a ledger consistency report with zero mismatches.
- **Cross-module state risk:** task ledger and spec docs; consistency report required.
- **Runtime-visible surface:** docs and command output only.

## Out of scope
- Implementing missing runtime behavior.
- Promoting any task without the evidence required by `coverage-tracker.md`.
- Rewriting historical ADRs.

## Notes
- This task is mostly about removing ambiguity. It should leave future readers able to tell what is built, what is verified, and what is still blocked.
