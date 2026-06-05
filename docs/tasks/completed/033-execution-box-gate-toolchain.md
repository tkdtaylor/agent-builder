# Task 033: execution-box gate toolchain

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed (code merged)

## Goal
Make the contained execution environment capable of running the production verification Gate without relying on host-only tool shims.

## Context
- Tech stack: Go, rootless Podman, execution-box profile
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 and Phase 0.3
- Related ADRs: ADR 014, ADR 015, ADR 016
- Dependencies: 003, 004, 005, 006, 014, 015, 016
- Audit finding: `make check` passes only when `/tmp/agent-builder-tools` is on `PATH`, while the execution-box image currently provides Go but not the full Gate scanner/linter toolchain.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | The execution-box runtime exposes `go`, `gofmt`, `golangci-lint`, `gods`, and `code-scanner` on `PATH` for Gate execution. | must have |
| REQ-002 | Tool installation or mounting is explicit, pinned or version-reported, and compatible with default-deny egress. | must have |
| REQ-003 | Missing Gate tools fail loudly before a task can be marked successful. | must have |
| REQ-004 | The toolchain provisioning path does not introduce Docker/devcontainer semantics outside the product containment artifact. | must have |

## Readiness gate
- [x] Test spec `033-execution-box-gate-toolchain-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks complete: 003, 004, 005, 006, 014, 015, and 016

## Acceptance criteria
- [x] [REQ-001] An execution-box probe reports every production Gate executable is present on `PATH`.
- [x] [REQ-002] Tool versions or pinned artifact sources are recorded in configuration/spec docs without requiring open egress during task execution.
- [x] [REQ-003] A missing-tool fixture causes `agent-builder verify` or the in-box Gate step to fail before success is reported.
- [x] [REQ-004] `make fitness-no-docker` remains green.

## Verification plan
- **Highest level achievable:** L6 - launched execution-box runs the production Gate against a fixture repo using only in-box tools.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/containment ./tests/cli -run 'TestExecutionBoxGateToolchain|TestVerifyMissingGateTool'
  ```
  Expected final assertion: `TC-001 execution-box Gate toolchain available`
- **Level 6 - Operator observation:**
  - Binary path: `containment/execution-box/run.sh --worktree <fixture> -- agent-builder verify /work`
  - Targeted behaviour to observe: Gate steps run with in-box `golangci-lint`, `gods`, and `code-scanner`, and the fixture passes or fails as expected.
- **Cross-module state risk:** containment image/launcher and Gate tool resolution; quote tool path/version evidence.
- **Runtime-visible surface:** probe output and Gate stdout/stderr.

## Verification evidence

- **Level 5 - validation harness:** `go test -count=1 -v ./tests/containment ./tests/cli -run 'TestExecutionBoxGateToolchain|TestVerifyMissingGateTool'` -> `ok  	github.com/tkdtaylor/agent-builder/tests/cli	0.165s`
- **Runtime-visible dry-run:** `containment/execution-box/run.sh --gate-tools /tmp/agent-builder-t033-tools.qIdv9b --print-toolchain-plan` printed `TC-001 PLAN` mounted tool paths plus `TC-002 PLAN` version lines for `golangci-lint`, `gods`, and `code-scanner`.
- **Level 6 - operator observation:** pending; local `containment/execution-box/run.sh --gate-tools <fixture> --worktree . --probe` is blocked by `execution-box: podman unavailable on PATH`.
- **Repo checks:** `make fitness-no-docker` -> `PASS fitness-no-docker: no forbidden dev-environment references found.`; `make fitness` -> `Fitness checks passed.`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`

## Out of scope
- Changing Gate semantics.
- Installing tools on the host.
- Relaxing egress allowlist to fetch tools during every run.

## Notes
- This task can choose either baked-in tools or explicit read-only tool mounts, but the choice must be documented in `docs/spec/configuration.md`.
