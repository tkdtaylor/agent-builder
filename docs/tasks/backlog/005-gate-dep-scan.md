# Task 005: dep-scan blocking gate step (supply-chain CVE)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Add a blocking gate Step that invokes dep-scan (`gods` for Go modules) as a supply-chain CVE gate, failing on any high-or-above severity finding, with tool-absent treated as a hard failure rather than a silent skip.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (scanners as a blocking gate), §3 (supply-chain risk on pulled dependencies)
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 — **Verification gate** (`dep-scan`/`code-scanner` as a blocking step)
- Related ADRs: none yet
- Dependencies: 002 (Step interface + Verdict model)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A blocking `Step` invokes dep-scan (`gods` subcommand for Go modules) against the target worktree | must have |
| REQ-002 | Any high-or-above severity finding fails the step; a clean scan passes | must have |
| REQ-003 | The scanner output is captured into the StepResult on failure | must have |
| REQ-004 | A missing dep-scan/`gods` binary is a HARD failure (fail loud), never a silent skip — honoring the gate-is-blocking invariant | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 002

## Acceptance criteria
- [ ] [REQ-001] The Step runs the `gods` Go scan against repoPath
- [ ] [REQ-002] A module with a known-vulnerable (high+) dependency fails the step; a clean module passes
- [ ] [REQ-003] Failing StepResult output contains the scanner findings
- [ ] [REQ-004] Tool-absent produces a failed StepResult naming the missing tool; there is no skip route

## Verification plan
- **Highest level achievable:** L5/L6 — run the Step against a Go module fixture with a known-vulnerable dependency (or a stubbed scanner output stream) → step fails; clean module → passes.
- **Harness command:** `go test ./internal/gate/... -run TestDepScan`
- **Operator path:** point the gate at a worktree pulling a flagged dependency and observe the failing Verdict + captured findings; remove the tool and observe a hard failure (not a skip).
- **Cross-module state risk:** none (consumes 002 types).
- **Runtime-visible surface:** captured scanner output in StepResult.

## Out of scope
- code-scanner step (006)
- Native go checks (003) and lint (004)

## Notes
- dep-scan is an external tool. Install via the dep-scan installer (`curl -fsSL https://raw.githubusercontent.com/tkdtaylor/dep-scan/main/install.sh | bash`); use the `gods` subcommand for Go.
- References SPEC invariant 1 (verification gate is the definition of done) and autonomous-builder.md §2 (scanners as a blocking gate).
- Tool absence being a hard failure is the load-bearing behaviour — a silent skip would quietly disable a security control.
- Updates `docs/spec/behaviors.md` in the same commit if the step behaviour becomes externally visible.
