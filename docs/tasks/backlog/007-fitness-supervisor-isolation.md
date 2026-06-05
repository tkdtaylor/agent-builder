# Task 007: Fitness F-003 — supervisor has no LLM/untrusted-content dependency

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Add a fitness check (`make fitness-supervisor-isolation`) that fails if `internal/supervisor`'s transitive import set contains any executor/LLM/web-fetch package, keeping the supervisor dumb by design so a hijacked in-box agent can never reach back through it.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §3 ("supervisor is dumb by design")
- Spec: `docs/spec/fitness-functions.md` (Rules table — add F-003 row), `docs/spec/SPEC.md` (candidate fitness fn F-003: supervisor imports no executor/LLM/web-fetch code)
- Related ADRs: none yet
- Dependencies: 001 (walking skeleton — supplies `internal/supervisor`)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A `fitness-supervisor-isolation` Makefile target that uses the Go import graph (`go list -deps ./internal/supervisor/...`) and exits non-zero if the transitive import set contains any executor/LLM/web-fetch package; exits 0 (with a clear message) otherwise | must have |
| REQ-002 | The target is added to the `fitness` umbrella target's prerequisites | must have |
| REQ-003 | A row for F-003 is added to the Rules table in `docs/spec/fitness-functions.md` (structural; asserts supervisor import graph is free of executor/LLM/web packages; threshold 0 violations; severity block) | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 001

## Acceptance criteria
- [ ] [REQ-001] `make fitness-supervisor-isolation` exits 0 on the current clean tree and prints a pass message
- [ ] [REQ-001] When a forbidden import is added under `internal/supervisor`, the target exits non-zero and names the offending package
- [ ] [REQ-002] `make fitness` invokes `fitness-supervisor-isolation` as part of the umbrella run
- [ ] [REQ-003] The F-003 row exists in `docs/spec/fitness-functions.md` and points to the `make fitness-supervisor-isolation` check command

## Verification plan
- **Highest level achievable:** L3 — fitness rule run via Makefile target; not a unit-tested seam.
- Command: `make fitness-supervisor-isolation` passes on the current tree.
- Negative test: temporarily add an import of an executor/LLM/web-fetch package (or a local stub package matching the forbidden pattern) into `internal/supervisor`, re-run the target, and confirm it exits non-zero and names the package; then revert.
- **Cross-module state risk:** none — read-only inspection of the import graph; adds a Makefile target and a spec row.
- **Runtime-visible surface:** `make fitness-supervisor-isolation` output (pass/fail message + exit code), and the same rule surfaced through `make fitness`.

## Out of scope
- Refactoring the supervisor itself (it is already isolated; this only guards it)
- Defining the executor/LLM/web packages (they arrive in their own tasks) — the rule matches by package path pattern, not by a hard import

## Notes
- The forbidden set should be matched by package-path pattern (e.g. anything under an `executor`, `llm`, or `webfetch`/`web` package), so the rule keeps working as those packages are introduced without coupling this task to them.
- Per `docs/spec/fitness-functions.md` "How to run", the three sub-changes (target, umbrella prerequisite, Rules row) land together in the implementing commit.
