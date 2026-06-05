# Test Spec 006: code-scanner blocking gate step (malware/backdoor)

**Linked task:** [`docs/tasks/completed/006-gate-code-scanner.md`](../completed/006-gate-code-scanner.md)
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
### TC-001: Clean worktree passes the scan step
- **Requirement:** REQ-001, REQ-002
- **Input:** temporary repository worktree plus a fake `code-scanner` executable on PATH that exits 0 and records its working directory.
- **Exercise:** run `CodeScannerStep.Run(cleanFixturePath)`.
- **Expected output:** StepResult `OK == true`; `Name()` is `code-scanner`; the fake scanner observes the supplied repoPath as its working directory.
- **Edge cases:** empty diff is not special-cased by the Step; the Step scans the supplied worktree and trusts `code-scanner`'s clean exit.

### TC-002: Flagged pattern fails the step with captured output
- **Requirement:** REQ-002, REQ-003
- **Input:** temporary repository worktree plus a fake `code-scanner` executable that prints malware/backdoor/credential-harvest findings to stdout and stderr and exits non-zero.
- **Exercise:** run `CodeScannerStep.Run(flaggedFixturePath)`.
- **Expected output:** StepResult `OK == false`; `Output` contains combined stdout/stderr from the scanner, including each emitted finding.
- **Edge cases:** mixed stdout/stderr findings are preserved; any non-zero scanner exit fails the Step.

### TC-003: Missing code-scanner is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH stripped to an empty temp directory before running the code-scanner Step.
- **Exercise:** run `CodeScannerStep.Run(cleanFixturePath)` with no `code-scanner` binary discoverable.
- **Expected output:** StepResult `OK == false`; `Output` names `code-scanner` and includes the lookup failure. There is no skip route that converts absence into a pass.
- **Edge cases:** no environment variable or Step option disables the missing-tool failure.

## Notes
Framework: Go `testing`. Tests use fake `code-scanner` executables in temporary PATH directories rather than a live malware signature database, making scanner assertions deterministic and offline. The production Step still shells out to `code-scanner` in the supplied repoPath. Harness command: `go test ./internal/gate/... -run TestCodeScanner`.
