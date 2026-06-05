# Task 004: golangci-lint gate step

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Add a blocking gate Step that runs `golangci-lint run` in the target worktree and fails on any finding, capturing the linter output into its StepResult.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (thin gate: adopt `golangci-lint`, don't build a framework)
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 — **Verification gate** (`golangci-lint` as a blocking step)
- Related ADRs: none yet
- Dependencies: 002 (Step interface + Verdict model)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A blocking `Step` runs `golangci-lint run` in the supplied repoPath | must have |
| REQ-002 | Any lint finding (non-zero exit) fails the step; a clean run passes | must have |
| REQ-003 | The linter's output is captured into the StepResult on failure | must have |
| REQ-004 | A missing `golangci-lint` binary on PATH is a hard step failure (fail loud), never a silent pass | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 002

## Acceptance criteria
- [ ] [REQ-001] The Step invokes `golangci-lint run` against repoPath
- [ ] [REQ-002] A known lint violation fails the step; a clean repo passes
- [ ] [REQ-003] Failing StepResult output contains the linter findings
- [ ] [REQ-004] Tool-absent produces a failed StepResult naming the missing binary

## Verification plan
- **Highest level achievable:** L5 — fixture repo with a known lint violation makes the Step fail; a clean fixture passes; observed via the harness.
- **Harness command:** `go test ./internal/gate/... -run TestGolangciLint`
- **Operator path:** run the gate against a worktree with a deliberate lint issue and observe the failing Verdict + captured findings.
- **Cross-module state risk:** none (consumes 002 types).
- **Runtime-visible surface:** captured linter output in StepResult.

## Out of scope
- Native go checks (003)
- dep-scan / code-scanner steps (005/006)

## Notes
- The repo already ships a golangci-lint v2 config; the Step runs the tool as the target worktree configures it.
- Updates `docs/spec/behaviors.md` in the same commit if the step behaviour becomes externally visible.
