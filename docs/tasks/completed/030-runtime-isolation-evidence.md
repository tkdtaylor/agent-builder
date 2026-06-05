# Task 030: runtime isolation evidence

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed

## Goal
Collect the missing runtime evidence for containment, egress, tiered runtime selection, and sandbox-runtime so tasks 014, 015, 016, and 021 can be honestly verified or explicitly left blocked.

## Context
- Tech stack: Go, rootless Podman, OCI runtimes, `@anthropic-ai/sandbox-runtime`
- Roadmap: `docs/plans/roadmap.md` Phase 0.3 and Phase 0.5
- Related ADRs: ADR 014, ADR 015, ADR 016, ADR 020
- Dependencies: 014, 015, 016, 021
- Audit finding: these tasks are in `completed/` but the tracker still records missing Podman or `srt` runtime evidence.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Run the execution-box containment probe in an environment with rootless Podman and record the observed filesystem, user, socket, capability, and quota results. | must have |
| REQ-002 | Run the egress probe and record both allowlisted success and non-allowlisted deny behavior. | must have |
| REQ-003 | Run the tiered runtime probe for the default `agent` runtime and an explicit runtime override; record selected runtime and Go-toolchain compatibility. | must have |
| REQ-004 | Run the sandbox-runtime adapter against a trivial command and a denied-egress fixture; record command output and deny behavior. | must have |
| REQ-005 | Update task files and the coverage tracker only to the evidence actually achieved; do not mark ✅ for probes that remain unavailable. | must have |

## Readiness gate
- [x] Test spec `030-runtime-isolation-evidence-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Runtime host has rootless Podman, required OCI runtime(s), and `srt` available, or the task records a blocker without promoting verification

## Acceptance criteria
- [x] [REQ-001] Task 014 has quoted L6 evidence or remains 🟡 with a concrete blocker.
- [x] [REQ-002] Task 015 has quoted allow and deny egress evidence or remains 🟡 with a concrete blocker.
- [x] [REQ-003] Task 016 has quoted runtime selection evidence or remains 🟡 with a concrete blocker.
- [x] [REQ-004] Task 021 has quoted sandbox-runtime command and denied-egress evidence or remains 🟡 with a concrete blocker.
- [x] [REQ-005] `coverage-tracker.md` status and `Verified by` cells match the evidence exactly.

## Verification plan
- **Highest level achievable:** L6 - operator-observed runtime security behavior from launched containment and sandbox-runtime commands.
- **Level 6 - Operator observation:**
  - Binary path: `containment/execution-box/run.sh --worktree . --probe`
  - Binary path: `containment/execution-box/run.sh --worktree . --egress-probe`
  - Binary path: `containment/execution-box/run.sh --worktree . --runtime runsc --probe`
  - Binary path: targeted Go harness for `internal/sandbox/sandboxruntime` using real `srt`
  - Targeted behaviour to observe: read-only rootfs, no host socket/home, non-root user, quota fields, allowlisted egress succeeds, non-allowlisted egress fails, selected runtime is recorded, sandbox-runtime deny path blocks egress.
- **Cross-module state risk:** docs and tracker only; evidence must name the task row being promoted.
- **Runtime-visible surface:** command stdout/stderr and task/tracker evidence entries.
- **Executor runtime result:** L6 not reached in this environment. Execution-box probes reached the launcher runtime check with the Task 033 Gate toolchain fixture and all failed with `execution-box: podman unavailable on PATH`; `command -v podman`, `command -v runsc`, and `command -v srt` exited `1` with no output, while `command -v bwrap` returned `/usr/bin/bwrap`. The opt-in live sandbox-runtime harness was also blocked before `srt` execution because bare `go` resolved to `/snap/bin/go` and exited with `snap-confine has elevated permissions and is not confined but should be. Refusing to continue to avoid permission escalation attacks`.

## Out of scope
- Changing containment implementation unless a probe exposes a defect.
- Installing Podman, runsc, or `srt` as part of the task.
- Promoting unrelated tasks.

## Notes
- This is deliberately an evidence task. If the host cannot run the probes, the correct outcome is a recorded blocker, not ✅.
