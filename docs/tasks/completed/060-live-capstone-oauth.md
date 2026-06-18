# Task 060: live capstone accepts subscription OAuth token

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** ✅ (verified — L5 green; L6 subscription auth proven live, full capstone gate-green blocked on l6 baseline gofmt defect)

## Goal

Thread the subscription OAuth credential through the live capstone test fixture so a
subscription-only operator can run the end-to-end capstone (and probes 022/028/032) without an API
key. Task 059 / ADR 033 made the executor accept either `ANTHROPIC_API_KEY` or
`CLAUDE_CODE_OAUTH_TOKEN`; this matches the test boundary to that contract.

## Context

- `tests/e2e/live_phase0_e2e_test.go`: skip-gate at ~line 35 checks only `ANTHROPIC_API_KEY`;
  `liveCapstoneFixture.env()` at ~line 238 forwards only `ANTHROPIC_API_KEY` to the subprocess.
- The executor (`internal/executor/claude_cli.go`) already prefers OAuth and injects exactly one
  credential — the fixture should forward both and let the executor choose; it must NOT pre-select.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-060-01 | Capstone skip-guard skips only when BOTH credentials are blank; proceeds when either is set; message names both. | must |
| REQ-060-02 | `liveCapstoneFixture.env()` forwards `CLAUDE_CODE_OAUTH_TOKEN` (from the host env) alongside `ANTHROPIC_API_KEY`. | must |
| REQ-060-03 | `go test ./...` + `make check` green; with neither credential set the capstone still SKIPs (no TC-054-02 regression). | must |

## Readiness gate

- [x] Test spec `060-live-capstone-oauth-test-spec.md` exists
- [x] Governed by ADR 033 (no new ADR needed — same decision)

## Acceptance criteria

- [ ] [REQ-060-01] TC-060-01: skip only when both blank; proceeds on either
- [ ] [REQ-060-02] TC-060-02: env() forwards CLAUDE_CODE_OAUTH_TOKEN
- [ ] [REQ-060-03] TC-060-03: `make check` exit 0; skip discipline intact
- [ ] [REQ-060-04] TC-060-04: live subscription capstone (L6) — pending until run

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green.
- **L6 (observed):** live subscription capstone run (TC-060-04) = unblocked 022/028/032 evidence.

## Out of scope

- Executor/`ConfigFromEnv` auth logic (task 059 / ADR 033).
- Publisher / box-launch legs (tasks 034, 058).
