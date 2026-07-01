# Task 143 — Claude completer resolves subscription OAuth from disk

**Status:** backlog
**Spec:** `docs/tasks/test-specs/143-claude-completer-ondisk-oauth-test-spec.md`
**Relates to:** ADR 059 (single-shot Completer + `ask`), ADR 033 (auth preference order), tasks 135–137,
task 101 (`claudeEnv` HOME isolation).

## Why

The Claude single-shot Completer isolates HOME (temp dir) and resolves cloud auth **only from the process
env**. A user logged in via **subscription OAuth** has no env token — the credential is on disk at
`${HOME}/.claude/.credentials.json`. So `ask --entry claude-oauth` and the orchestrate answer route to
Claude **fail auth unless the operator manually exports `CLAUDE_CODE_OAUTH_TOKEN`**. This was hit live on
2026-06-30: every other brain answered "capital of France?" off its own on-disk login (agy inherits the
real HOME; ollama needs none), but Claude required a hand-exported token. For a general agent whose whole
premise is "hand it a goal," making the operator paste a token to use their own logged-in Claude is a
real usability gap on the general (non-coding) path.

## Scope

- **`internal/secrets`:** add `DiskOAuthSecretSource` that reads `${HOME}/.claude/.credentials.json` and
  returns `claudeAiOauth.accessToken` as the OAuth token (reads only that field; missing/malformed/empty
  → `("","")`, no error). Add a chaining source (env → disk) whose `ProviderToken` returns the env token
  when present and falls back to disk only when both env vars are empty (ADR 033 preference preserved).
- **`internal/executor/completer.go`:** wire the chained (env → disk) source into the `HarnessClaudeCLI`
  cloud completer construction (replacing the bare `secrets.NewEnvSecretSource()`), leaving the local
  translation-proxy path (empty `SecretRef`) untouched.
- **HOME isolation preserved:** the disk read happens in the parent Go process (real HOME intact) before
  `claudeEnv` rewrites the child env — the child still runs with the isolated temp HOME.
- **ADR:** extend ADR 059 (or a short new ADR) recording the decision to read the subscription OAuth
  token off disk and inject it into the child env, with the security rationale below. Flag
  `security-auditor` on the diff.
- **Spec:** `docs/spec/configuration.md` (document the on-disk OAuth fallback + precedence) and
  `docs/spec/interfaces.md` (SecretSource auth-resolution order) updated in the same commit.

## Out of scope

- OAuth **refresh / write-back** of a rotated token — under an isolated HOME the CLI can't persist a
  refreshed token; an expired on-disk token surfaces the CLI's own auth error. Refresh is a separate task.
- The coding-path `ClaudeCLI` executor (`NewClaudeCLIFromEntry`) — same HOME-isolation shape, separate
  seam; a parallel follow-up if the coding path ever needs subscription auth without an exported token.
- agy/Antigravity and local (translation-proxy) entries — already authenticate correctly.

## Security considerations (for the ADR + security-auditor)

- The token is the user's own credential on their own host; the completer already injects cloud tokens
  into the subprocess env. The new surface is *reading one field from a well-known credentials file*.
- Read **only** `claudeAiOauth.accessToken`; never the refresh token. Token stays in the child **env**,
  never in argv, and `sanitizeCLIOutput` must still redact it on a CLI failure (REQ-143-05).
- Fail transparent: no on-disk login → empty tokens, no error — the env-token and translation-proxy
  paths behave exactly as before.

## Verification plan

- **Highest level achievable here:** L6 (operator, this host has a real `~/.claude` subscription login).
- **L2:** secrets unit tests — disk source reads `accessToken`; graceful absence (missing/malformed/empty
  → no error); chain precedence (env wins; disk only when env empty). Completer wiring test (stubbed
  `cmdFactory`): env cleared + disk creds → child env has `CLAUDE_CODE_OAUTH_TOKEN` **and** isolated
  `HOME`; token absent from argv; failure path redacts the token.
- **L3:** `make check` green (F-010/F-014 intact).
- **L6 (runtime observation):** with **no** token exported, `agent-builder ask --entry claude-oauth
  "What is the capital of France? Reply with only the city name."` → `Paris`; quote the output.

## Boundaries

- Do **not** weaken HOME isolation (the temp-HOME strip in `claudeEnv` stays; only auth resolution
  changes).
- Read only the single `accessToken` field; never log the token.
- Leave agy/local/coding-executor paths unchanged (one responsibility: the Claude completer's cloud auth).
