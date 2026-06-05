# Task 034: branch and PR publication

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed (code merged; L6 real PR pending because no git remote is configured)

## Goal
Publish a verified executor-produced branch as a pull request artifact so Phase 0 satisfies the roadmap's branch+PR outcome.

## Context
- Tech stack: Go, git, GitHub CLI or API
- Roadmap: `docs/plans/roadmap.md` Phase 0 goal: produce a branch/PR
- Related ADRs: ADR 002, ADR 012, ADR 013
- Dependencies: 012, 013, 019, 022, 028, 031
- Audit finding: existing tasks prove branch capture, but no task yet owns PR creation or its failure modes.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A verified successful run publishes the executor branch to the configured remote and opens or records a PR. | must have |
| REQ-002 | PR publication happens only after the Gate verdict is OK and the produced branch is non-empty. | must have |
| REQ-003 | Publication failures are recorded as non-success outcomes and do not mark the task done. | must have |
| REQ-004 | Secrets used for git/PR publication are read from explicit environment/configuration and are redacted from logs and run records. | must have |

## Readiness gate
- [x] Test spec `034-branch-pr-publication-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks complete: 012, 013, 019, 022, 028, and 031

## Acceptance criteria
- [ ] [REQ-001] A fake remote/CLI harness proves a successful run pushes the branch and records a PR URL or PR identifier.
- [ ] [REQ-002] Gate failure, executor failure, and blank branch do not call the PR publisher.
- [ ] [REQ-003] Publisher errors are surfaced in CLI/run-record output and preserve the task as not done.
- [ ] [REQ-004] GitHub or git token values never appear in stdout, stderr, logs, task files, or run records.

## Verification plan
- **Highest level achievable:** L5 - fake `git`/`gh` or fake publisher seam validates publication ordering and failure handling; L6 real PR creation is optional in an approved private repo.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/publisher ./tests/e2e -run 'TestBranchPRPublication|TestPublisherFailureDoesNotMarkDone'
  ```
  Expected final assertion: `TC-001 verified branch published as PR artifact`
- **Level 6 - Operator observation:**
  - Binary path: `agent-builder run` against an approved private fixture repo with real git credentials
  - Targeted behaviour to observe: branch pushed, PR created, PR URL recorded, token redacted.
- **Cross-module state risk:** run outcome to publisher to run-record/status writer; producer-consumer trace required.
- **Runtime-visible surface:** pushed branch, PR URL/ID, run-record entry, CLI output.

## Out of scope
- Multi-provider repository hosting support.
- Public release workflow.
- Auto-merging PRs.

## Notes
- Prefer a small `Publisher` seam so tests can prove ordering without invoking real GitHub.
