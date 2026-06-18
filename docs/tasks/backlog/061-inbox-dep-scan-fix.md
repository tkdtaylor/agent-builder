# Task 061: fix in-box dep-scan gate step

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix the production gate's `DepScanStep`, which crashes in-box. It ran `gods` (a `go` drop-in wrapper)
with no arguments → bare `exec go` → exit non-zero. Replace it with a direct `dep-scan` invocation,
and pass cleanly when the module has no `go.sum` (no third-party deps = nothing to scan). This
unblocks the live capstone (022/028/032) past the dep-scan step. Governing decision: ADR 034.

## Context

- `internal/gate/go_steps.go`: `DepScanStep.Run` → `runCommandStep(repoPath, "gods")` (no args).
- `gods` (gate-tools wrapper) with no pkgs skips the scan and `exec go` (no args) → exit 2.
- Latent: host `make check` (`lint test fitness`) never runs dep-scan; the step's tests used a fake
  `gods` that always exited 0. Surfaced live in-box: `FAIL gods` after build/vet/test/gofmt/lint PASS.
- agent-builder has no `go.sum` (no `require`); `dep-scan` was not a mounted gate tool.
- `dep-scan check --registry go --lockfile go.sum --lockfile-type go` is the correct scan invocation.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-061-01 | `DepScanStep.Run` passes (`OK==true`) without invoking the scanner when `repoPath/go.sum` is absent. | must |
| REQ-061-02 | When `go.sum` exists, `DepScanStep.Run` runs `dep-scan check --registry go --lockfile go.sum --lockfile-type go` in repoPath. | must |
| REQ-061-03 | A non-zero `dep-scan` exit fails the step with captured output; a missing `dep-scan` (with go.sum present) is a hard failure naming the tool. | must |
| REQ-061-04 | `run.sh` `required_mounted_gate_tools` + `--print-toolchain-plan` require `dep-scan` (replacing `gods`). | must |
| REQ-061-05 | `internal/gate/dep_scan_step_test.go` rewritten to the new contract (TC-061-01..04). | must |
| REQ-061-06 | Affected `docs/spec/` (gate behavior / toolchain) updated in the same commit. | must |
| REQ-061-07 | `go test ./...` + `make check` green. | must |

## Readiness gate

- [x] Test spec `061-inbox-dep-scan-fix-test-spec.md` exists
- [x] ADR 034 written

## Acceptance criteria

- [ ] [REQ-061-01] TC-061-01: no go.sum → PASS, scanner not invoked
- [ ] [REQ-061-02] TC-061-02: go.sum → dep-scan invoked with exact args in repoPath
- [ ] [REQ-061-03] TC-061-03/04: finding fails with output; missing tool hard-fails
- [ ] [REQ-061-04] TC-061-05: run.sh toolchain requires dep-scan
- [ ] [REQ-061-06] spec updated same commit
- [ ] [REQ-061-07] TC-061-06: `make check` exit 0

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green.
- **L6 (observed):** live capstone clears the dep-scan gate step (TC-061-07) — pending live run.

## Out of scope

- Egress allowlist for dep-scan's CVE backend when deps exist (deferred per ADR 034).
- The `code-scanner` gate step (next link in the chain — its own task if broken in-box).
- Removing the `gods` wrapper (stays for interactive use).
