# Task 006: code-scanner blocking gate step (malware/backdoor)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** active (verified L5; pending merge)

## Goal
Add a blocking gate Step that invokes code-scanner (malware / backdoor / credential-harvest scan) over the produced diff/worktree, failing the gate on findings, with tool-absent treated as a hard failure rather than a silent skip.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (scanners as a blocking gate), §3 (credential-handling — scanners catch code reading tokens off disk)
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 — **Verification gate** (`dep-scan`/`code-scanner` as a blocking step)
- Related ADRs: none yet
- Dependencies: 002 (Step interface + Verdict model)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A blocking `Step` invokes code-scanner over the produced diff/worktree | must have |
| REQ-002 | Any malware / backdoor / credential-harvest finding fails the step; a clean scan passes | must have |
| REQ-003 | The scanner output is captured into the StepResult on failure | must have |
| REQ-004 | A missing code-scanner is a HARD failure (fail loud), never a silent skip | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 002

## Acceptance criteria
- [x] [REQ-001] The Step runs code-scanner against the worktree/diff at repoPath
- [x] [REQ-002] A worktree containing a flagged pattern fails the step; a clean worktree passes
- [x] [REQ-003] Failing StepResult output contains the scanner findings
- [x] [REQ-004] Tool-absent produces a failed StepResult naming the missing tool; there is no skip route

## Verification plan
- **Highest level achievable:** L5/L6 — run the Step against a fixture containing a benign-but-flagged pattern → step fails; a clean worktree → passes.
- **Harness command:** `go test ./internal/gate/... -run TestCodeScanner`
- **Operator path:** point the gate at a worktree with a deliberately flagged pattern and observe the failing Verdict + captured findings; remove the tool and observe a hard failure (not a skip).
- **Cross-module state risk:** none (consumes 002 types).
- **Runtime-visible surface:** captured scanner output in StepResult.

## Verification evidence

- **Level 5 — validation harness:** `go test ./internal/gate/... -run TestCodeScanner -count=1` → `ok github.com/tkdtaylor/agent-builder/internal/gate`
- **Repo checks:** `go test ./...` → `ok github.com/tkdtaylor/agent-builder/internal/gate`; `go build ./...` → success; `env PATH=/tmp/agent-builder-tools:$PATH make check` → `All checks passed.`
- **Spec-verifier:** read-only worker verifier APPROVE — all TC/REQ assertions satisfied; docs/spec behavior, interface, data-model, architecture, and diagram updates aligned.

## Out of scope
- dep-scan step (005)
- Native go checks (003) and lint (004)

## Notes
- code-scanner is the existing scanning block (runs the scan in a disposable sandbox so the target never executes on the host).
- References autonomous-builder.md §3 credential-handling — this step is what catches code that reads executor tokens off disk; its tool-absent-is-hard-failure behaviour is load-bearing for that control.
- Updates `docs/spec/behaviors.md` in the same commit if the step behaviour becomes externally visible.
