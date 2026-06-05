# Task 018: Wall-clock timeout / runaway kill

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed

## Goal
The supervisor enforces a configurable wall-clock timeout on the in-box run; on expiry it kills the box and marks the run timed-out — the runaway-containment / escalation hook.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§3 — resource limits + wall-clock kill / escalation)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.4)
- Related ADRs: none yet
- Dependencies: 017 (dispatch lifecycle)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Configurable wall-clock timeout for the in-box run (updates `docs/spec/configuration.md` in same commit) | must have |
| REQ-002 | A run exceeding the timeout is killed and the box torn down | must have |
| REQ-003 | The run is marked timed-out — a distinct outcome from gate-fail | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 017

## Acceptance criteria
- [x] [REQ-001] Timeout is a supervisor config value (not hard-coded); `docs/spec/configuration.md` documents it in the same commit
- [x] [REQ-002] An in-box run that exceeds the timeout triggers a box kill and deterministic teardown (the 017 teardown guarantee still holds)
- [x] [REQ-003] The run outcome records `timed-out`, distinguishable from a gate failure or a normal completion

## Verification plan
- **Highest level achievable:** L6 — dispatch a fixture task whose in-box work sleeps past the timeout; observe the box killed and the run marked timed-out, quoting the log line
- **L5:** `go test -count=1 -v ./internal/supervisor ./tests/supervisor` — fake loop blocks past a short configured timeout; assert box killed + outcome == timed-out
- **L6:** operator-observed test log line showing the timeout fired and the box was torn down
- **Cross-module state risk:** timeout outcome on the run record (the run-outcome enum/field gains a `timed-out` state) — coordinate with 019's `RunRecord`
- **Runtime-visible surface:** config value + structured log line for the timeout kill

## Out of scope
- Per-resource (cpu/mem) quotas — those live in the box profile (task 014)
- Run log collection / `RunRecord` persistence (task 019)

## Notes
- Implemented via a context deadline / timer around the in-box run; on expiry the supervisor kills the box and records the distinct timed-out outcome.
- Supervisor stays dumb by design — no executor/LLM/web imports (invariant F-003, fitness task 007).
- Updates `docs/spec/configuration.md`, `docs/spec/data-model.md`, `docs/spec/behaviors.md`, and the explicit containment interface contract in `docs/spec/interfaces.md` in the same commit.
