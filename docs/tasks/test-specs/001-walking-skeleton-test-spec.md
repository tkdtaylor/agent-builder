# Test Spec 001: Walking skeleton & project setup

**Linked task:** [`docs/tasks/completed/001-walking-skeleton.md`](../completed/001-walking-skeleton.md)
**Written:** 2026-06-04

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003 | ✅ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

---

## Test cases

### TC-001: Project compiles with idiomatic Go layout

- **Requirement:** REQ-001
- **Input:** `go build ./...`
- **Expected output:** exit 0; `cmd/agent-builder` and `internal/supervisor` build clean
- **Edge cases:** `go vet ./...` and `gofmt -l .` produce no output

### TC-002: Walking-skeleton seams exist and behave as stubs

- **Requirement:** REQ-002
- **Input:** `go test ./...` (exercises `internal/supervisor`)
- **Expected output:** `Version` is non-empty; `Supervisor.Run()` returns `ErrNotImplemented` (stub is deliberate, not silently passing). Covered by `internal/supervisor/supervisor_test.go` (`TestVersionSet`, `TestRunNotYetImplemented`).
- **Edge cases:** `Executor` and `Gate` interfaces and `Task`/`Result` types compile against the SPEC invariants

### TC-003: Entrypoint runs and reports status without crashing

- **Requirement:** REQ-003
- **Input:** `go run ./cmd/agent-builder`
- **Expected output:** prints version banner + "loop not yet implemented — see docs/plans/roadmap.md (Phase 0)"; exits 0
- **Edge cases:** does not treat the `ErrNotImplemented` stub as a fatal error

---

## Post-implementation verification

- [x] All test cases above pass
- [x] No regressions in existing tests
- [x] Coverage threshold met (skeleton behaviour, not an arbitrary %)
