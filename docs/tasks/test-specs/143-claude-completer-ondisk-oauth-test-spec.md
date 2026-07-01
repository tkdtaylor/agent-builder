# Test spec — Task 143: Claude completer resolves subscription OAuth from disk

**Task:** `docs/tasks/backlog/143-claude-completer-ondisk-oauth.md`
**Relates to:** ADR 059 (single-shot Completer + `ask`), ADR 033 (auth preference order), tasks 135–137
(Claude/agy completers + `ask`), task 101 (`claudeEnv` HOME isolation + local-entry auth).

## Context

The Claude single-shot Completer (`internal/executor/claude_completer.go`) runs `claude -p <prompt>`
in an **isolated temp HOME** — `claudeEnv` strips `HOME`/`XDG_CONFIG_HOME`/`XDG_CACHE_HOME` and points
HOME at a fresh temp dir (config/history hygiene). For a **cloud** Claude entry it resolves auth from
`secrets.EnvSecretSource.ProviderToken()`, which reads **only** the process env
(`ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN`).

A user logged in via **subscription OAuth** has no token in the env — the credential lives on disk at
`${HOME}/.claude/.credentials.json` (`claudeAiOauth.accessToken`). Because the completer isolates HOME,
the child CLI can't read it, and the env source never looks there. Result: `ask --entry claude-oauth`
(and the orchestrate answer route to Claude) **fails auth unless the operator manually exports a token**
— observed live 2026-06-30 (the reason the L6 run had to inject `CLAUDE_CODE_OAUTH_TOKEN` by hand).

The fix: a **disk-OAuth fallback** secret source. When env-based `ProviderToken()` yields no token, read
`${HOME}/.claude/.credentials.json` (real parent-process HOME, before isolation) and return its
`claudeAiOauth.accessToken` as the OAuth token — injected through the existing `CLAUDE_CODE_OAUTH_TOKEN`
path. HOME isolation is preserved; explicit env tokens still win.

## Requirements

- **REQ-143-01** — A `DiskOAuthSecretSource` reads `${HOME}/.claude/.credentials.json` and returns
  `claudeAiOauth.accessToken` as the OAuth token (authToken empty). It reads **only** that field.
- **REQ-143-02** — Graceful absence: a missing file, unreadable file, malformed JSON, or empty/missing
  `accessToken` yields empty tokens and **no error** (behavior unchanged when there is no on-disk login).
- **REQ-143-03** — Precedence (ADR 033 preserved): a chained source returns the **env** token when the
  env provides one; the disk token is used **only** when both env vars are empty. An explicit env token
  is never overridden by disk.
- **REQ-143-04** — The Claude cloud completer is wired to the chained (env → disk) source. With env auth
  cleared and on-disk creds present, `Complete` injects `CLAUDE_CODE_OAUTH_TOKEN=<accessToken>` into the
  child command env (verified via the stubbed `cmdFactory`), while HOME stays isolated (temp dir).
- **REQ-143-05** — The on-disk token is never logged: `sanitizeCLIOutput` redaction still covers the
  resolved OAuth token on a CLI failure. No new leak surface (token only in the child env, not argv).
- **REQ-143-06** — Boundary + fitness unchanged (F-010/F-014 green); local (translation-proxy) Claude
  entries — empty `SecretRef` — are unaffected (they never consult the disk source).

## Test cases

- **TC-143-01** (`TestDiskOAuthSourceReadsAccessToken`) — temp `HOME` with
  `.claude/.credentials.json` = `{"claudeAiOauth":{"accessToken":"tok-abc",...}}` → source returns
  `oauthToken == "tok-abc"`, `authToken == ""`.
- **TC-143-02** (`TestDiskOAuthSourceGracefulAbsence`, table) — (a) no file, (b) dir but no file,
  (c) malformed JSON, (d) `{}` / missing `claudeAiOauth`, (e) empty `accessToken` → each returns
  `("","")` and **no error**.
- **TC-143-03** (`TestChainedSourcePrecedence`, table) — env `CLAUDE_CODE_OAUTH_TOKEN=env-tok` + disk
  `disk-tok` → chain returns `env-tok`; env `ANTHROPIC_API_KEY=key` only → returns that authToken, disk
  not consulted; env both empty + disk present → returns `disk-tok`; env + disk both empty → `("","")`.
- **TC-143-04** (`TestClaudeCompleterInjectsDiskOAuth`) — cloud entry, stubbed `cmdFactory`, env auth
  cleared, disk creds present → captured child env contains `CLAUDE_CODE_OAUTH_TOKEN=tok-abc` **and**
  `HOME=<temp dir>` (isolation intact); the on-disk plaintext appears in **no** captured argv element.
- **TC-143-05** (`TestClaudeCompleterFailureRedactsDiskToken`) — stubbed CLI exits non-zero echoing the
  token → returned error string does not contain the raw `accessToken` (redacted).
- **TC-143-06 (L6, operator)** — real subscription login in `~/.claude`, **no** `CLAUDE_CODE_OAUTH_TOKEN`
  / `ANTHROPIC_API_KEY` exported: `agent-builder ask --entry claude-oauth "What is the capital of France?
  Reply with only the city name."` → `Paris`. Same via `orchestrate` (analysis on, cloud-only registry)
  answers over the channel.

## Non-vacuous / negative controls

- TC-143-03 asserts the **env token wins** over a present disk token (proves precedence, not just that
  *some* token is returned) and that disk is **not** consulted when env already supplies auth.
- TC-143-04 asserts `HOME` is still the temp dir in the child env (proves the fix does **not** regress
  HOME isolation to "just inherit real HOME") and that the token is absent from argv (env-only surface).
- TC-143-02 asserts **no error** on absence (proves the fallback is transparent when there is no on-disk
  login — it must not break the env-token and translation-proxy paths).

## Out of scope (do not test here)

- OAuth **refresh / write-back** of a rotated token — an expired on-disk token surfaces the CLI's own
  auth error (a separate concern; note it, don't solve it).
- The coding-path `ClaudeCLI` executor (`NewClaudeCLIFromEntry`) — same HOME-isolation shape, but a
  separate seam; a parallel follow-up, not this task.
- agy/Antigravity and local (translation-proxy) entries — already authenticate correctly (the agy
  completer inherits the real HOME; local entries use the proxy endpoint, not disk OAuth).
