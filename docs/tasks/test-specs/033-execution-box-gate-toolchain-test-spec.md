# Test Spec 033: execution-box gate toolchain

**Linked task:** [`docs/tasks/completed/033-execution-box-gate-toolchain.md`](../completed/033-execution-box-gate-toolchain.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004 | ✅ |

## Test cases
### TC-001: execution-box exposes every Gate tool
- **Requirement:** REQ-001
- **Input:** execution-box probe command that checks `go`, `gofmt`, `golangci-lint`, `gods`, and `code-scanner`.
- **Expected output:** each tool is found on `PATH`; version or executable path is printed.
- **Edge cases:** missing optional version output still passes if the executable path is present and runnable.

### TC-002: tool provisioning is pinned or version-reported
- **Requirement:** REQ-002
- **Input:** containment config/spec docs and tool install/mount configuration.
- **Expected output:** docs identify how tool versions are controlled or reported, and runs do not need broad network egress to fetch tools during task execution.
- **Edge cases:** local read-only tool mount must be documented as host dependency.

### TC-003: missing tool fails loudly
- **Requirement:** REQ-003
- **Input:** fixture environment omitting one Gate executable.
- **Expected output:** verification fails with the missing tool name and does not report success.
- **Edge cases:** failure occurs even when earlier Gate steps pass.

### TC-004: no dev-container drift
- **Requirement:** REQ-004
- **Input:** `make fitness-no-docker`.
- **Expected output:** no forbidden dev-environment references are reported outside the product containment artifact.
- **Edge cases:** containment product files remain exempt as documented.

### TC-005: in-box Gate verifies fixture repo
- **Requirement:** REQ-001
- **Input:** launched execution-box running `agent-builder verify /work` against a clean fixture repo.
- **Expected output:** all production Gate steps run with in-box tools and the clean fixture passes.
- **Edge cases:** dirty fixture fails at the expected Gate step.

## Notes
Framework: Go `testing` for static/fake harnesses plus operator-observed Podman probe for L6.
