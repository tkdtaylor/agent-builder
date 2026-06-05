# Test Spec 004: golangci-lint gate step

**Linked task:** [`docs/tasks/active/004-gate-golangci-lint.md`](../active/004-gate-golangci-lint.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-001, TC-002 | ✅ |
| REQ-003 | TC-002 | ✅ |
| REQ-004 | TC-003 | ✅ |

## Test cases
### TC-001: Clean repo passes the lint step
- **Requirement:** REQ-001, REQ-002
- **Input:** clean temporary Go module with a minimal `.golangci.yml` and one lint-clean package.
- **Exercise:** run `GolangciLintStep.Run(cleanFixturePath)` with `golangci-lint` available on `PATH`.
- **Expected output:** StepResult `OK == true`; `Name()` is `golangci-lint run`; the command runs in the supplied repoPath.
- **Edge cases:** if the binary is unavailable for this positive-tool test, skip with a recorded reason rather than converting environment absence into a false implementation failure.

### TC-002: Lint violation fails the step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** temporary Go module with `.golangci.yml` enabling `errcheck` and source that ignores a returned error.
- **Exercise:** run `GolangciLintStep.Run(violatingFixturePath)` with `golangci-lint` available on `PATH`.
- **Expected output:** StepResult `OK == false`; `Output` contains combined stdout/stderr from `golangci-lint run`, including the violating filename and the `errcheck` finding text.
- **Edge cases:** multiple findings may be present; the assertion only requires the known finding to be surfaced.

### TC-003: Missing golangci-lint is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH stripped to an empty temp directory before running the lint Step.
- **Exercise:** run `GolangciLintStep.Run(cleanFixturePath)` with no `golangci-lint` binary discoverable.
- **Expected output:** StepResult `OK == false`; `Output` names `golangci-lint` and includes the lookup failure. The step never silently passes when the tool is absent.
- **Edge cases:** binary present but config absent follows normal golangci-lint behavior and is not special-cased by the Step.

## Notes
Framework: Go `testing`. Fixtures are generated in temporary directories so committed source remains lint-clean. The positive and negative lint-finding cases use the real subprocess; PATH manipulation uses `t.Setenv` for the tool-absent case. Harness command: `go test ./internal/gate/... -run TestGolangciLint` when `golangci-lint` is already on `PATH`; in this repo's local tool-cache environment, use `env PATH=/tmp/agent-builder-tools:$PATH go test ./internal/gate/... -run TestGolangciLint`.
