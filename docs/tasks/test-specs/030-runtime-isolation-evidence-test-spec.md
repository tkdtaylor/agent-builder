# Test Spec 030: runtime isolation evidence

**Linked task:** [`docs/tasks/completed/030-runtime-isolation-evidence.md`](../completed/030-runtime-isolation-evidence.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004 | ✅ |
| REQ-005 | TC-005 | ✅ |

## Test cases
### TC-001: execution-box containment probe records runtime properties
- **Requirement:** REQ-001
- **Input:** `containment/execution-box/run.sh --worktree . --probe` on a host with rootless Podman.
- **Expected output:** probe output names read-only rootfs, writable worktree, writable scratch, non-root user, no host home/socket mount, dropped workload capabilities, and quota fields.
- **Edge cases:** missing Podman records a blocker and does not promote task 014.

### TC-002: egress probe records allow and deny behavior
- **Requirement:** REQ-002
- **Input:** `containment/execution-box/run.sh --worktree . --egress-probe` with a known allowlisted host and known deny targets.
- **Expected output:** allowlisted host connects; non-allowlisted host and direct IP probe are refused, timed out, or DNS-blocked.
- **Edge cases:** DNS failure for allowlisted host is a blocker, not a deny-path pass.

### TC-003: runtime selection probe records selected OCI runtime
- **Requirement:** REQ-003
- **Input:** `containment/execution-box/run.sh --worktree . --print-runtime-plan`, default `--probe`, and explicit `--runtime runsc --probe`.
- **Expected output:** default workload maps to the documented runtime; explicit override is honored; Go-toolchain probe result is quoted.
- **Edge cases:** unavailable `runsc` records a blocker for task 016.

### TC-004: sandbox-runtime adapter runs command and blocks denied egress
- **Requirement:** REQ-004
- **Input:** targeted Go test or harness using real `srt` with a trivial command and a denied-egress command.
- **Expected output:** trivial command returns expected stdout and exit 0; denied-egress command fails or reports network denial.
- **Edge cases:** missing `srt` records a blocker and does not promote task 021.

### TC-005: evidence ledger matches observed results
- **Requirement:** REQ-005
- **Input:** updated task files and `coverage-tracker.md`.
- **Expected output:** rows 014, 015, 016, and 021 are ✅ only when the corresponding runtime evidence is present; otherwise they remain 🟡 with a blocker.
- **Edge cases:** partial evidence promotes only the proven task rows.

## Notes
Framework: operator-observed shell commands plus Go harness for sandbox-runtime. This spec intentionally permits a blocked outcome when required runtime tools are unavailable.
