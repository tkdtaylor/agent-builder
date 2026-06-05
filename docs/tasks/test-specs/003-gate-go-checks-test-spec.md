# Test Spec 003: Native Go gate steps (build/vet/test/gofmt)

**Linked task:** [`docs/tasks/active/003-gate-go-checks.md`](../active/003-gate-go-checks.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002, TC-003 | ✅ |
| REQ-003 | TC-002, TC-003, TC-004 | ✅ |
| REQ-004 | TC-004 | ✅ |

## Test cases
### TC-001: Clean fixture repo passes all four steps
- **Requirement:** REQ-001
- **Input:** clean testdata Go module (builds, vets, tests green, formatted)
- **Exercise:** assemble a `gate.Gate` with `GoBuildStep`, `GoVetStep`, `GoTestStep`, and `GoFmtStep`, then call `Verify(cleanFixturePath)`.
- **Expected output:** Verdict `OK == true`; Results contain exactly four passing entries in this order: `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`.
- **Edge cases:** a package with no test files still passes the `go test ./...` step.

### TC-002: Failing test fails the test step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** dirty testdata module with one failing `go test` assertion and otherwise buildable/formatted code.
- **Exercise:** run only `GoTestStep.Run(failingTestFixturePath)` so the test failure is isolated from earlier gate short-circuiting.
- **Expected output:** StepResult `OK == false`; `Output` contains combined stdout/stderr from `go test`, including the failing test name and failure text.
- **Edge cases:** a build error in test files is allowed to fail the `go test` step, but this fixture intentionally reaches test execution so the captured output proves the command was run.

### TC-003: Unformatted file fails the gofmt step
- **Requirement:** REQ-002
- **Input:** module containing an unformatted .go file
- **Exercise:** run `GoFmtStep.Run(unformattedFixturePath)`.
- **Expected output:** StepResult `OK == false` even though `gofmt -l .` exits 0; `Output` contains the relative or absolute path of the unformatted file listed by gofmt.
- **Edge cases:** whitespace/indentation-only formatting drift is enough to fail the step when `gofmt -l` lists the file.

### TC-004: Missing tool is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH stripped to an empty temp directory before constructing/running a Go native step.
- **Exercise:** run `GoBuildStep.Run(cleanFixturePath)` with no `go` binary discoverable.
- **Expected output:** StepResult `OK == false`; `Output` names the missing tool (`go`) and includes the lookup failure. The step never silently passes when the tool is absent.
- **Edge cases:** tool present but wrong version is out of scope for this task.

## Notes
Framework: Go `testing` (table-driven where useful). Fixtures live under `internal/gate/testdata/` as separate clean and dirty Go modules. Subprocess invocation is real; PATH manipulation uses `t.Setenv` for the tool-absent case. Harness command: `go test ./internal/gate/... -run TestGoChecks`.
