# Test Spec 003: Native Go gate steps (build/vet/test/gofmt)

**Linked task:** [`docs/tasks/backlog/003-gate-go-checks.md`](../backlog/003-gate-go-checks.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-003 | ❌ |
| REQ-003 | TC-002 | ❌ |
| REQ-004 | TC-004 | ❌ |

## Test cases
### TC-001: Clean fixture repo passes all four steps
- **Requirement:** REQ-001
- **Input:** clean testdata Go module (builds, vets, tests green, formatted)
- **Expected output:** each of the four Steps returns ok; aggregate Verdict ok
- **Edge cases:** module with no test files (test step still passes)

### TC-002: Failing test fails the test step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** dirty testdata module with one failing `go test`
- **Expected output:** the `go test` Step fails; its StepResult output contains the test failure text
- **Edge cases:** build error vs test failure surface at the correct step

### TC-003: Unformatted file fails the gofmt step
- **Requirement:** REQ-002
- **Input:** module containing an unformatted .go file
- **Expected output:** gofmt Step fails even though `gofmt -l` exits 0, because its output lists the file
- **Edge cases:** trailing-whitespace-only diff still flagged

### TC-004: Missing tool is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH stripped of the target tool (or stubbed lookpath)
- **Expected output:** the Step fails and names the missing tool; never silently passes
- **Edge cases:** tool present but wrong version (out of scope; note only)

## Notes
Framework: Go `testing` (table-driven). Fixtures: `testdata/` clean + dirty Go modules. Subprocess invocation real; PATH manipulation via `t.Setenv` for the tool-absent case.
