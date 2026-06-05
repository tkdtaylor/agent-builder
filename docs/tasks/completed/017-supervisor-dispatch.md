# Task 017: Supervisor dispatch-one-task lifecycle

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (code merged + green; pending formal spec-verifier pass before ✅)

## Goal
Replace the stubbed `Supervisor.Run()` with the real outside-the-box lifecycle that dispatches exactly one task, creates an ephemeral containment box, runs the agent loop inside it, collects the result, and tears the box down deterministically.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§3 — "agent runs inside the box; supervisor outside, dumb by design")
- Roadmap: `docs/plans/roadmap.md` (Phase 0.4)
- Related ADRs: none yet
- Dependencies: 012 (agent loop, runs inside the box), 014 (containment box profile)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | `Run()` dispatches exactly one task per call, enforcing create → run-inside → teardown ordering | must have |
| REQ-002 | Teardown ALWAYS runs — on success, on loop failure, and on panic in the in-box run | must have |
| REQ-003 | The supervisor adds no executor/LLM/web imports; fitness invariant F-003 (task 007) stays green | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 012, 014

## Acceptance criteria
- [x] [REQ-001] `Supervisor.Run()` no longer returns `ErrNotImplemented`; one Run creates a box, starts the agent loop inside it, collects the result, and tears the box down — observable as create → run → teardown in logs
- [x] [REQ-002] Teardown is invoked even when the in-box loop returns an error or panics (deferred / recover path)
- [x] [REQ-003] `make fitness` (F-003 import check) passes — no forbidden executor/LLM/web imports in the supervisor package

## Verification plan
- **Highest level achievable:** L6 — supervisor binary path exercised via the dispatch test harness; create→run→teardown ordering is observable in logs, and teardown is observed to run even when the loop errors
- **L5:** `go test ./internal/supervisor/...` — assert lifecycle ordering (create before run before teardown) and that teardown ran on the error path
- **L6:** dispatch a fixture task with a fake box + fake loop; observe `box created` → `loop started` → `box torn down` in logs, including the loop-error case
- **Cross-module state risk:** none new in this task (run-outcome record arrives in 018/019); box and loop are consumed via interfaces (fake box + fake loop in tests)
- **Runtime-visible surface:** structured log lines for box create / loop start / teardown

## Out of scope
- Wall-clock timeout / runaway kill (task 018)
- Run log collection / RunRecord (task 019)
- Real containment-box backend (task 021)

## Notes
- Dumb by design: the supervisor orchestrates lifecycle only and must remain free of executor/LLM/web imports (invariant F-003, enforced by fitness task 007). The agent loop (task 012) carries all the intelligence and runs inside the box.
- Box and loop are injected as interfaces so the fake box + fake loop can drive the lifecycle test without a real backend.
