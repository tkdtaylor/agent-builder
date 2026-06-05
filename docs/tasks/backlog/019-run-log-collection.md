# Task 019: Run log collection (audit-trail seam)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
The supervisor streams the box's stdout/stderr plus a command log out to a durable run-record that survives box teardown — the audit-trail seam where the audit-trail block plugs in later.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§3 — observability / audit-trail seam; §1 — "only the pushed branch + streamed logs survive" an ephemeral box)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.4)
- Related ADRs: none yet
- Dependencies: 017 (dispatch lifecycle)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A `RunRecord` wire format — plain-text / NDJSON (updates `docs/spec/data-model.md` in same commit) | must have |
| REQ-002 | stdout/stderr + a command log are captured during the in-box run | must have |
| REQ-003 | The record persists and is readable after box teardown | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 017

## Acceptance criteria
- [ ] [REQ-001] A `RunRecord` is defined as a plain-text / NDJSON wire format; `docs/spec/data-model.md` documents it in the same commit
- [ ] [REQ-002] The supervisor captures the box's stdout/stderr and a command log during the run, streaming them out (not buffered only in the ephemeral box)
- [ ] [REQ-003] The run-record file exists and is readable after teardown — it survives the ephemeral box

## Verification plan
- **Highest level achievable:** L6 — after a fixture run, the log file exists, contains the in-box output, and is readable post-teardown; quote a sample line
- **L5:** `go test ./internal/supervisor/...` — fake box emits known stdout/stderr; assert the persisted record contains them after teardown
- **L6:** operator-observed — inspect the run-record file on disk after a fixture run and confirm it holds the streamed in-box output post-teardown
- **Cross-module state risk:** names `RunRecord` (new data-model entity) — coordinate the outcome field with 018's timed-out state
- **Runtime-visible surface:** durable run-record file (plain-text / NDJSON) on disk

## Out of scope
- The audit-trail block itself (later phase) — this task only opens the seam
- Wall-clock timeout / runaway kill (task 018)

## Notes
- Streaming, not buffering: §1 says only the pushed branch + streamed logs survive the box, so logs must leave the box during the run, not be read back after teardown.
- Plain-text / NDJSON keeps the record liftable and audit-trail-friendly (Unix-philosophy plain-text interchange).
- Supervisor stays dumb by design — no executor/LLM/web imports (invariant F-003, fitness task 007).
- Updates `docs/spec/data-model.md` in the same commit (new `RunRecord` entity). Do not edit spec during backlog authoring.
