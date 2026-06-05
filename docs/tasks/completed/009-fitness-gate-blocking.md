# Task 009: Fitness F-002 — verification gate is blocking (no skip route)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L6)

## Goal
Add a fitness check (`make fitness-gate-blocking`) that asserts the verification path exposes no `--no-verify`/skip flag or conditional that bypasses dep-scan/code-scanner, because the gate is the definition of done and a silent bypass defeats the security model.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (verification gate as the thin, blocking definition of done)
- Spec: `docs/spec/fitness-functions.md` (Rules table — add F-002 row), `docs/spec/SPEC.md` (candidate fitness fn F-002: the verification path has no `--no-verify`/skip route around `dep-scan`/`code-scanner`; top-level invariant 1)
- Related ADRs: none yet
- Dependencies: 002 (gate orchestrator core), 005 (dep-scan step), 006 (code-scanner step) — the gate + scanner steps must exist to assert nothing bypasses them

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A `fitness-gate-blocking` Makefile target that greps the gate package + CLI for skip/bypass affordances (`--no-verify` / `skip` flags, env-var short-circuits, conditional early-returns around the scanner steps) and exits non-zero if any bypass exists; exits 0 otherwise | must have |
| REQ-002 | The target is added to the `fitness` umbrella target's prerequisites | must have |
| REQ-003 | A row for F-002 is added to the Rules table in `docs/spec/fitness-functions.md` (security; asserts the gate path has no skip route around dep-scan/code-scanner; threshold 0 bypasses; severity block) | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 002, 005, 006

## Acceptance criteria
- [x] [REQ-001] `make fitness-gate-blocking` exits 0 on the current tree (gate steps present, no skip route) and prints a pass message
- [x] [REQ-001] Adding a `--no-verify` short-circuit (or a conditional that returns ok without running the scanner steps) causes the target to exit non-zero and report the offending location
- [x] [REQ-002] `make fitness` invokes `fitness-gate-blocking` as part of the umbrella run
- [x] [REQ-003] The F-002 row exists in `docs/spec/fitness-functions.md` and points to the `make fitness-gate-blocking` check command

## Verification plan
- **Highest level achievable:** L3 — fitness rule run via Makefile target.
- Command: `make fitness-gate-blocking` passes once the gate + scanner steps (002/005/006) exist with no skip route.
- Negative test: add a `--no-verify` short-circuit (CLI flag or an `if skip { return ok }` around the scanner steps), re-run the target, confirm it exits non-zero and names the location; then revert.
- **Cross-module state risk:** none — read-only grep over the gate package + CLI; adds a Makefile target and a spec row.
- **Runtime-visible surface:** `make fitness-gate-blocking` output (pass/fail + exit code), and the same rule via `make fitness`.

## Out of scope
- Implementing the gate, dep-scan, or code-scanner steps (tasks 002/005/006)
- Runtime enforcement of the gate at execution time — this is a static structural guard, not a runtime check

## Notes
- Match on the patterns that constitute a bypass: skip/no-verify flags surfaced by the CLI, env-var gates that disable scanners, and early-return conditionals between gate entry and the scanner steps. Tune to avoid false positives on the words appearing in comments/tests that assert the *absence* of a bypass.
- This rule is only meaningful once 005/006 land; until then the scanner steps it guards do not exist. Sequence accordingly.
- Per `docs/spec/fitness-functions.md` "How to run", the three sub-changes (target, umbrella prerequisite, Rules row) land together in the implementing commit.

## Verification evidence

- **Positive fitness check:** `make fitness-gate-blocking` -> `PASS fitness-gate-blocking: no verification gate bypass affordances found.`
- **Negative CLI flag check:** temporary `const temporaryNegativeFixtureFlag = "--no-verify"` in `cmd/agent-builder/main.go` made `make fitness-gate-blocking` fail and name `cmd/agent-builder/main.go:13`; temporary fixture removed before commit.
- **Negative env-var check:** temporary `const temporaryNegativeFixtureEnv = "SKIP_SCAN"` in `internal/gate/go_steps.go` made `make fitness-gate-blocking` fail and name `internal/gate/go_steps.go:19`; temporary fixture removed before commit.
- **Negative early-return check:** temporary `if skip := false; skip { return StepResult{OK: true} }` in `internal/gate/go_steps.go` made `make fitness-gate-blocking` fail and name `internal/gate/go_steps.go:102`; temporary fixture removed before commit.
- **Umbrella fitness:** `make fitness` includes `fitness-gate-blocking`; clean tree -> `Fitness checks passed.`
- **Repo checks:** `gofmt -w .` -> no changes; `go test ./...` -> `ok github.com/tkdtaylor/agent-builder/internal/gate`; `go build ./...` -> success; plain `make check` failed because `golangci-lint` was absent from default `PATH`; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`
- **Spec-verifier:** read-only worker verifier APPROVE — all REQ/TC assertions satisfied; `make fitness-gate-blocking`, `make fitness`, `go test ./...`, `go build ./...`, and `env PATH=/tmp/agent-builder-tools:$PATH make check` passed; residual risk limited to not rerunning negative fixtures in the read-only verifier.
