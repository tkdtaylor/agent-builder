# Test spec — Task 059: executor accepts subscription OAuth token

## Context

The Claude Code CLI executor ([internal/executor/claude_cli.go](../../../internal/executor/claude_cli.go))
authenticated only with `ANTHROPIC_API_KEY`. The operator's primary use case is a Claude
Pro/Max **subscription**, whose headless credential is `CLAUDE_CODE_OAUTH_TOKEN` (minted by
`claude setup-token`). Both are env-injectable, scoped, independently revocable values — so both
satisfy the egress invariant ("executor tokens are independently revocable + fast to rotate")
without weakening the `HOME`/XDG isolation. This task adds the OAuth token as an accepted,
**preferred** alternative. See ADR 033.

The invariant the tests must lock down: the executor injects **exactly one** auth variable into
the subprocess, never both — passing both lets `ANTHROPIC_API_KEY` silently win and bill the
wrong account.

## Test cases

### TC-059-01 — OAuth token alone authenticates
- **Assertion:** `NewClaudeCLI` with only an OAuth token (no API key) passes `validate()` — the
  executor no longer hard-requires `ANTHROPIC_API_KEY`. File: `internal/executor/claude_cli_test.go`.

### TC-059-02 — claudeEnv injects exactly the chosen credential
- **Assertion:** when the OAuth token is in use, the constructed subprocess env contains
  `CLAUDE_CODE_OAUTH_TOKEN=<token>` and contains **no** `ANTHROPIC_API_KEY` entry (the base
  env's API key, if any, is stripped). When the API key is in use, the env contains
  `ANTHROPIC_API_KEY=<token>` and **no** `CLAUDE_CODE_OAUTH_TOKEN`. The `HOME`, `XDG_CONFIG_HOME`,
  `XDG_CACHE_HOME` temp-dir wipe is unchanged in both cases.

### TC-059-03 — OAuth token preferred over API key
- **Assertion:** when both credentials are present, the OAuth token is selected and the API key is
  not injected (one-directional precedence per ADR 033).

### TC-059-04 — missing both credentials fails loudly
- **Assertion:** `validate()` (and `ConfigFromEnv`) returns an error naming the credential(s) when
  neither `ANTHROPIC_API_KEY` nor `CLAUDE_CODE_OAUTH_TOKEN` is set. The error is descriptive, not a
  silent fallthrough.

### TC-059-05 — ConfigFromEnv reads either credential
- **Assertion:** `runtime.ConfigFromEnv` accepts a config where only `CLAUDE_CODE_OAUTH_TOKEN` is
  set (API key absent) and wires it into the executor; accepts only `ANTHROPIC_API_KEY`; rejects
  neither-set. File: `internal/runtime/run_test.go`.

### TC-059-06 — output sanitization redacts the active token
- **Assertion:** `sanitizeCLIOutput` redacts whichever token value is in use (OAuth or API key) from
  combined stdout/stderr.

### TC-059-07 — full gate green
- **Assertion:** `go test ./...` + `make check` pass.

### TC-059-08 — live subscription run (L6, operator/observed)
- **Assertion:** with `CLAUDE_CODE_OAUTH_TOKEN` set from `.env` (no `ANTHROPIC_API_KEY`), a real
  executor run authenticates against the subscription and completes without `Not logged in` /
  `Credit balance is too low`. Recorded as operator observation; **stays pending until run** —
  never marked from code alone.

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green (TC-059-01..07).
- **L6 (observed, deferred to live run):** subscription-authenticated executor run (TC-059-08),
  which also unblocks live probes 022/028/032 without API credit.

## Out of scope

- Interactive `claude /login` host-home session credentials (rejected by ADR 033 — not env-injectable,
  breaks isolation).
- A general multi-provider credential abstraction (deferred; only two named env credentials today).
