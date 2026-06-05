# Test Spec 016: Tiered OCI runtime selection seam

**Linked task:** [`docs/tasks/completed/016-tiered-runtime-seam.md`](../completed/016-tiered-runtime-seam.md)
**Written:** 2026-06-04
**Status:** complete

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001 | ✅ |
| REQ-002 | TC-002, TC-004 | ✅ |
| REQ-003 | TC-003 | ✅ |

## Test cases
### TC-001: Runtime selectable via config/flag (happy path)
- **Requirement:** REQ-001
- **Harness:** `go test ./tests/containment/... -run Runtime` for static/no-Podman assertions; `containment/execution-box/run.sh --worktree . --runtime runc --probe` and `containment/execution-box/run.sh --worktree . --runtime runsc --probe` for L6 runtime observation where runtimes are installed.
- **Input:** Launch box with `--runtime runc`, then with `--runtime runsc`; inspect the created workload container runtime and print the requested runtime from inside the probe.
- **Expected output:** Launcher passes Podman `--runtime <value>` to the workload/probe container, host inspection reports the requested runtime, and the in-box probe prints a `TC-016-RUNTIME PASS` line naming the selected runtime and workload tier.
- **Edge cases:** Unknown runtime values are rejected before Podman; missing runtime support on the host fails closed with an explicit unavailable-runtime message.

### TC-002: Go toolchain compatibility under runsc (recorded finding)
- **Requirement:** REQ-002
- **Harness:** `containment/execution-box/run.sh --worktree . --runtime runsc --probe` in an environment with rootless Podman and `runsc`.
- **Input:** Under `runsc`, the in-box probe writes a trivial Go module under `/scratch` and runs `CGO_ENABLED=0 go build`.
- **Expected output:** Probe prints `TC-016-GO PASS: go build trivial module succeeded under runsc`; OR it fails with captured compiler/runtime output and the ADR records the syscall gap plus fallback.
- **Edge cases:** `runsc` unavailable locally; cgo-enabled builds or dependency builds that exercise syscalls outside gVisor's implemented set.

### TC-003: Default tier per workload (happy path)
- **Requirement:** REQ-003
- **Harness:** `containment/execution-box/run.sh --print-runtime-plan`; `containment/execution-box/run.sh --workload dev --print-runtime-plan`; `containment/execution-box/run.sh --workload agent --runtime runc --print-runtime-plan`.
- **Input:** Resolve runtime defaults for agent and dev workloads with no Podman dependency, then resolve an explicit override.
- **Expected output:** Agent workload defaults to `runsc`; dev workload defaults to `runc`; explicit `--runtime` overrides the workload default and is reported as the selected runtime source.
- **Edge cases:** Unknown workload tiers are rejected before Podman.

### TC-004 (NEGATIVE / fallback): Syscall gap forces fallback
- **Requirement:** REQ-002
- **Harness:** same L6 `--runtime runsc --probe` as TC-002; ADR evidence records the pass/fail finding.
- **Input:** A Go build under `runsc` that fails because of an unimplemented syscall or runtime restriction.
- **Expected output:** The probe exits non-zero, prints `TC-016-GO FAIL` with captured output, and the ADR records the specific gap plus the selected fallback response instead of silently treating `runsc` as compatible.

## Notes
Static tests can prove parsing, defaults, docs, and Podman argument construction, but cannot promote the task beyond 🟡 because the selected OCI runtime is a host runtime effect. The L6 command for promotion is `containment/execution-box/run.sh --worktree . --runtime runsc --probe`; quote both the `TC-016-RUNTIME PASS` and `TC-016-GO PASS` lines, or quote the failing syscall output and the fallback recorded in the ADR.
