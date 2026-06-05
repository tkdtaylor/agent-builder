# Task 020: exec-sandbox run() adapter seam

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L2/L3; unit-test-only, no runtime surface)

## Goal
Define the `run()` adapter interface (the isolation seam) the supervisor uses to execute work inside a box, decoupled from any concrete isolation backend, so the rented bootstrap backend can later be swapped for the produced exec-sandbox v0 with zero caller changes.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§1 adopt-to-bootstrap / build-to-ship; §4 tiered runtime)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.5)
- Related ADRs: ADR 020: exec-sandbox run adapter seam
- Dependencies: 001

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A `run()` adapter interface mapping (command + worktree + resource/egress limits) → (result + exit code). Updates `docs/spec/interfaces.md` in the same commit. | must have |
| REQ-002 | A fake/in-process backend implementing the interface for tests (no real isolation, deterministic results). | must have |
| REQ-003 | The supervisor depends only on the interface, never on a concrete backend type. | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 001

## Acceptance criteria
- [x] [REQ-001] A Go interface defines `run(command, worktree, limits) → (result, exitCode, error)`; the contract is recorded in `docs/spec/interfaces.md`.
- [x] [REQ-002] A fake backend satisfies the interface and is used by supervisor unit tests without invoking any real isolation runtime.
- [x] [REQ-003] The supervisor field/parameter is typed as the interface; no concrete backend is imported by the supervisor package.

## Verification plan
- **Highest level achievable:** L2 — this is a pure internal seam. There is no runtime binary surface to observe yet; the only behaviour is the contract itself, exercised through the fake backend in unit tests. The real backend that produces observable isolation is task 021. Claiming L5/L6 here would be claiming a runtime that does not exist.
- **Cross-module state risk:** none — the seam is in-process; the fake backend holds no external state.
- **Runtime-visible surface:** none yet (interface + fake). The real surface arrives with 021.

## Out of scope
- The sandbox-runtime backing implementation (task 021).
- The Podman execution-box profile (task 014).

## Notes
- This is THE seam that resolves the chicken-and-egg: adopt a rented backend now, ship exec-sandbox v0 behind the same interface later.
- Keep limits as a typed struct, not a map, so the swap to exec-sandbox v0 is a compile-checked contract.
- Updates `docs/spec/interfaces.md` in the same commit as the code change.
