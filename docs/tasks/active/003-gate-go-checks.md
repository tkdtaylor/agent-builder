# Task 003: Native Go gate steps (build/vet/test/gofmt)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** active (native Go gate steps built + green; pending spec-verifier pass before ✅)

## Goal
Implement gate Steps that shell out to `go build ./...`, `go vet ./...`, `go test ./...`, and `gofmt -l .` against the target worktree, each blocking and failing (with captured output) on non-zero exit or non-empty `gofmt -l` output.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (verification gate is the definition of done; adopt `go test`, don't build a framework)
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 — **Verification gate** (`go test` as a blocking step)
- Related ADRs: none yet
- Dependencies: 002 (Step interface + Verdict model)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | One blocking `Step` per native tool: `go build ./...`, `go vet ./...`, `go test ./...`, `gofmt -l .`, each run in the target worktree | must have |
| REQ-002 | A step fails on non-zero exit; the `gofmt` step also fails on non-empty `gofmt -l` output (lists unformatted files) | must have |
| REQ-003 | On failure each step captures combined stdout+stderr into its StepResult output | must have |
| REQ-004 | A missing tool on PATH is a hard step failure (fail loud), never a silent pass | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 002

## Acceptance criteria
- [x] [REQ-001] Four distinct Steps exist, each invoking its tool in the supplied repoPath
- [x] [REQ-002] Non-zero exit fails the step; a non-empty `gofmt -l` listing fails the gofmt step even though the command itself exits zero
- [x] [REQ-003] Failing-step output contains the tool's captured stdout+stderr
- [x] [REQ-004] Tool-absent produces a failed StepResult identifying the missing tool

## Verification plan
- **Highest level achievable:** L5/L6 — run the gate against two fixture repos: one clean, one carrying a failing test plus an unformatted file. Observe the Verdict fails the dirty repo at the expected step and passes the clean repo.
- **Harness command:** `go test ./internal/gate/... -run TestGoChecks`
- **Operator path:** point the assembled gate at a scratch worktree, break a test / leave a file unformatted, observe the failing Verdict and captured output; revert and observe pass.
- **Cross-module state risk:** none (consumes the 002 Step/Verdict types; adds no new shared types).
- **Runtime-visible surface:** captured tool output surfaced in StepResult; future log/CLI rendering of the Verdict.

## Verification evidence

- **Level 5 — validation harness:** `go test ./internal/gate/... -run TestGoChecks` → `ok github.com/tkdtaylor/agent-builder/internal/gate`
- **Repo checks:** `go test ./...` → `ok github.com/tkdtaylor/agent-builder/internal/gate`; `go build ./...` → success; `env PATH=/tmp/agent-builder-tools:$PATH make check` → `All checks passed.`

## Out of scope
- golangci-lint step (004)
- dep-scan / code-scanner steps (005/006)

## Notes
- Fixtures live under the test package (testdata clean + dirty Go modules).
- Updates `docs/spec/behaviors.md` (gate step behaviour) in the same commit if behaviour becomes externally visible.
