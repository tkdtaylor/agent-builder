# Task 001: Walking skeleton & project setup

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (code merged; verification blocked by stale Task 001 assertions)

## Goal

Stand up a compiling, testable Go skeleton that encodes the design seams (Supervisor, Executor, Gate, Task) so subsequent tasks have a real shape to fill in.

## Context

- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md`
- Related ADRs: none yet
- Dependencies: none (this is the bootstrap)

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Go module + idiomatic layout (`cmd/`, `internal/`) compiles, vets, and is gofmt-clean | must have |
| REQ-002 | Walking-skeleton seams defined per SPEC invariants: `Supervisor`, `Executor` (`(harness,model)→branch`), `Gate` (definition of done), `Task`/`Result`; stubs return `ErrNotImplemented` | must have |
| REQ-003 | `make check` exists (lint + test + fitness) and `go run ./cmd/agent-builder` reports status without crashing | must have |

## Readiness gate

- [x] Test spec `001-walking-skeleton-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] No blocking tasks

## Acceptance criteria

- [x] [REQ-001] `go build ./...`, `go vet ./...`, `gofmt -l .` all clean
- [x] [REQ-002] seams compile; `Supervisor.Run()` returns `ErrNotImplemented`; covered by `internal/supervisor/supervisor_test.go`
- [x] [REQ-003] `make check` runs; entrypoint prints status and exits 0

## Verification plan

- **Highest level achievable:** L6 (operator-observed via `go run`) + L2 (unit tests) — the skeleton has a runtime surface (the status banner) and unit-tested seams.
- **Level 2 — unit tests:** `go test ./...` → `ok github.com/tkdtaylor/agent-builder/internal/supervisor`
- **Level 6 — operator-observed:** `go run ./cmd/agent-builder` prints the version banner + Phase 0 notice, exits 0.

## Notes

The seams here are intentionally stubs. Phase 0 fills them in order (roadmap §Phase 0): verification gate → agent loop + escalation → Podman containment profile → supervisor wiring → exec-sandbox adapter → single executor.
