# Test Spec 016: Tiered OCI runtime selection seam

**Linked task:** [`docs/tasks/backlog/016-tiered-runtime-seam.md`](../backlog/016-tiered-runtime-seam.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ❌ |
| REQ-002 | TC-002, TC-004 | ❌ |
| REQ-003 | TC-003 | ❌ |

## Test cases
### TC-001: Runtime selectable via config/flag (happy path)
- **Requirement:** REQ-001
- **Input:** Launch box with `--runtime runc`, then with `--runtime runsc` (if available); observe the active runtime
- **Expected output:** Box runs under the selected runtime; active runtime is observable and matches the request
- **Edge cases:** unknown runtime value rejected loudly; runtime binary missing on host

### TC-002: Go toolchain compatibility under runsc (recorded finding)
- **Requirement:** REQ-002
- **Input:** Under `runsc`, run `go build` of a trivial module
- **Expected output:** Build succeeds; OR build fails on an unimplemented syscall — the specific gap and chosen fallback are recorded in the ADR
- **Edge cases:** cgo-enabled build; build that triggers a syscall outside gVisor's implemented set

### TC-003: Default tier per workload (happy path)
- **Requirement:** REQ-003
- **Input:** Launch a dev workload and an agent workload with no runtime override
- **Expected output:** Dev → `runc`; agent → `runsc`
- **Edge cases:** explicit override beats the default

### TC-004 (NEGATIVE / fallback): Syscall gap forces fallback
- **Requirement:** REQ-002
- **Input:** A build that hits an unimplemented `runsc` syscall
- **Expected output:** Failure is detected (not silently passed) and the recorded fallback (bubblewrap / Kata) is the documented response

## Notes
Framework: integration test launching boxes under each available OCI runtime + in-box `go build` probe; assertion on active runtime and build exit code. Runtime tier is observed at L6 — quote per-runtime results. gVisor toolchain-compat caveat is the central risk: if `runsc` is unavailable in the test env, record that and the runc-only result, and capture the runsc finding when the toolchain is exercised.
