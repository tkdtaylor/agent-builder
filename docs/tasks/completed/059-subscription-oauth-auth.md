# Task 059: executor accepts subscription OAuth token

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** 🟡 code merged

## Goal

Let the Claude Code CLI executor authenticate with a Claude **subscription** OAuth token
(`CLAUDE_CODE_OAUTH_TOKEN`, minted by `claude setup-token`) as an alternative to — and preferred
over — `ANTHROPIC_API_KEY`. Both are env-injectable, scoped, independently revocable credentials,
so both satisfy the egress invariant without weakening the `HOME`/XDG isolation. This is the
operator's primary real-world path and bills against the subscription rather than API credit
(unblocking the live probes that hit `Credit balance is too low`). Governing decision: ADR 033.

## Context

- `internal/executor/claude_cli.go`: `validate()` hard-requires `ANTHROPIC_API_KEY`; `claudeEnv()`
  injects it while wiping `HOME`/`XDG_CONFIG_HOME`/`XDG_CACHE_HOME`. The wipe must stay.
- `internal/runtime/run.go`: `ConfigFromEnv` reads `ClaudeToken` from `ANTHROPIC_API_KEY` and fails
  loudly when blank (line ~121); `Run` wires it into `NewClaudeCLI` (line ~179).
- Interactive `claude /login` credentials live in host `~/.claude` and are rejected (ADR 033) —
  they are not env-injectable and would break isolation.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-059-01 | `ClaudeCLIConfig` accepts an OAuth token credential; executor selects exactly one credential, preferring OAuth over API key. | must |
| REQ-059-02 | `claudeEnv()` injects only the selected credential's env var and strips the other from the subprocess env; the `HOME`/XDG temp-dir wipe is unchanged. | must |
| REQ-059-03 | `validate()` passes with either credential and fails loudly when both are absent. | must |
| REQ-059-04 | `runtime.ConfigFromEnv` reads either `ANTHROPIC_API_KEY` or `CLAUDE_CODE_OAUTH_TOKEN`, requires at least one, and wires the selection into the executor. | must |
| REQ-059-05 | `sanitizeCLIOutput` redacts whichever token is in use. | must |
| REQ-059-06 | `docs/spec/configuration.md` (and `interfaces.md` if the config type changes) reflect the two-credential contract and precedence. | must |
| REQ-059-07 | `go test ./...` + `make check` green. | must |

## Readiness gate

- [x] Test spec `059-subscription-oauth-auth-test-spec.md` exists
- [x] ADR 033 written

## Acceptance criteria

- [ ] [REQ-059-01] TC-059-01, TC-059-03: OAuth-only authenticates; OAuth preferred when both set
- [ ] [REQ-059-02] TC-059-02: env contains exactly one auth var; HOME/XDG wipe intact
- [ ] [REQ-059-03] TC-059-04: neither-set fails loudly with a descriptive error
- [ ] [REQ-059-04] TC-059-05: ConfigFromEnv accepts either, rejects neither
- [ ] [REQ-059-05] TC-059-06: sanitizer redacts the active token
- [ ] [REQ-059-06] spec updated in the same commit
- [ ] [REQ-059-07] TC-059-07: `make check` exit 0

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green.
- **L6 (observed, deferred):** live subscription-authenticated executor run with
  `CLAUDE_CODE_OAUTH_TOKEN` only (TC-059-08) — stays pending until actually run; unblocks 022/028/032.

## Out of scope

- Interactive `claude /login` host-home credentials (ADR 033 rejects them).
- A general multi-provider credential abstraction (deferred — two named env credentials only).
