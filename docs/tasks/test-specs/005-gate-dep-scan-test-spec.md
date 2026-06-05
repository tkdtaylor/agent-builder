# Test Spec 005: dep-scan blocking gate step (supply-chain CVE)

**Linked task:** [`docs/tasks/active/005-gate-dep-scan.md`](../active/005-gate-dep-scan.md)
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
### TC-001: Clean module passes the scan step
- **Requirement:** REQ-001, REQ-002
- **Input:** temporary Go module plus a fake `gods` executable on PATH that exits 0 and records its working directory.
- **Exercise:** run `DepScanStep.Run(cleanFixturePath)`.
- **Expected output:** StepResult `OK == true`; `Name()` is `gods`; the fake scanner observes the supplied repoPath as its working directory.
- **Edge cases:** low/medium-only findings are treated as a scanner concern; the Step passes when `gods` exits 0.

### TC-002: High+ severity finding fails the step
- **Requirement:** REQ-002, REQ-003
- **Input:** temporary Go module plus a fake `gods` executable that prints a high-severity CVE finding and exits non-zero.
- **Exercise:** run `DepScanStep.Run(vulnerableFixturePath)`.
- **Expected output:** StepResult `OK == false`; `Output` contains combined stdout/stderr from the scanner, including the CVE identifier and high severity text.
- **Edge cases:** mixed findings are represented by scanner output; any non-zero scanner exit fails the Step and preserves all emitted findings.

### TC-003: Missing dep-scan is a hard failure
- **Requirement:** REQ-004
- **Input:** PATH stripped to an empty temp directory before running the dep-scan Step.
- **Exercise:** run `DepScanStep.Run(cleanFixturePath)` with no `gods` binary discoverable.
- **Expected output:** StepResult `OK == false`; `Output` names `gods` and includes the lookup failure. There is no skip route that converts absence into a pass.
- **Edge cases:** no environment variable or Step option disables the missing-tool failure.

## Notes
Framework: Go `testing`. Tests use fake `gods` executables in temporary PATH directories rather than a live vulnerability database, making CVE assertions deterministic and offline. The production Step still shells out to `gods` in the supplied repoPath. Harness command: `go test ./internal/gate/... -run TestDepScan`.
