# Task 023: CLI subcommand surface (run / version / verify)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed

## Goal
Replace the status-only `main` with a real CLI surface: `run` (dispatch the loop), `version`, and `verify <repo>` (run the gate against a repo standalone), with defined exit codes (0 ok, 1 generic, 2 usage).

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (verification gate = definition of done)
- Roadmap: `docs/plans/roadmap.md` (Phase 0)
- Related ADRs: <none yet> — record one if a CLI framework is adopted over stdlib `flag`
- Dependencies: 002 (Gate), 017 (supervisor run)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | `run`, `version`, `verify` subcommands with documented flags + exit codes (0 ok, 1 generic, 2 usage). Updates `docs/spec/interfaces.md` in the same commit. | must have |
| REQ-002 | `verify <repo>` runs the Gate (task 002) standalone and exits non-zero on failure, with NO skip flag (must not violate fitness F-002). | must have |
| REQ-003 | Unknown subcommand / usage error exits 2. | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 002, 017

## Acceptance criteria
- [x] [REQ-001] `agent-builder run`, `version`, and `verify <repo>` exist with documented flags; exit codes follow 0/1/2; the surface is recorded in `docs/spec/interfaces.md`.
- [x] [REQ-002] `verify <repo>` invokes the Gate against the repo and exits non-zero when the gate fails; there is no flag that skips or bypasses the gate.
- [x] [REQ-003] An unrecognized subcommand or malformed usage exits with code 2.

## Verification plan
- **Highest level achievable:** L6 — `agent-builder version` prints the version; `agent-builder verify <clean-repo>` exits 0; `agent-builder verify <dirty-repo>` exits non-zero; a bad subcommand exits 2. Quote each output and exit code.
- L5 harness: shell harness invoking the built binary against fixture repos (one gate-passing, one gate-failing); expected final assertions — exit codes 0, non-zero, and 2 respectively.
- **Cross-module state risk:** none — `verify` calls the Gate read-only against the target repo path.
- **Runtime-visible surface:** CLI (subcommands, flags, exit codes, stdout/stderr).

## Out of scope
- The gate steps themselves (tasks 003-006).
- Router / multi-provider flags (deferred).

## Notes
- No skip flag on `verify` — bypassing the gate would violate fitness F-002 (the gate is the definition of done).
- Stdlib `flag` is sufficient for three subcommands; adopt a framework only with an ADR.
- Updates `docs/spec/interfaces.md` in the same commit.
