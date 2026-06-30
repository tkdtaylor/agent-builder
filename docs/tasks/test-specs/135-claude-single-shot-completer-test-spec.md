# Test spec — Task 135: Claude single-shot completer (`claude -p`)

**Task:** `docs/tasks/backlog/135-claude-single-shot-completer.md`
**Relates to:** ADR 059 (general single-shot path), ADR 053 (Completer seam), `claude_cli.go` (`claude -p`, `claudeEnv`, `NewClaudeCLIFromEntry`).

## Context

`CompleterForEntry` (ADR 053) currently returns `ErrSingleShotUnsupported` for `HarnessClaudeCLI`.
This task makes it return a `claudeCompleter` that runs `claude -p <prompt>` and returns stdout — no
worktree/branch/gate. Auth mirrors the `ClaudeCLI` executor: cloud entries resolve `ProviderToken()`;
local (proxy) entries set `ANTHROPIC_BASE_URL` + the `ANTHROPIC_AUTH_TOKEN` placeholder. The
subprocess env is built with `claudeEnv` and a temp HOME.

## Requirements

- **REQ-135-01** — `CompleterForEntry(entry)` with `entry.Harness == HarnessClaudeCLI` returns a
  non-nil `Completer` and `nil` error (no longer `ErrSingleShotUnsupported`).
- **REQ-135-02** — `claudeCompleter.Complete(ctx, entry, prompt)` invokes the Claude CLI as
  `claude -p <prompt>` (exact argv `[cliPath, "-p", prompt]`) and returns the subprocess stdout,
  trimmed of surrounding whitespace.
- **REQ-135-03** — Auth env mirrors `NewClaudeCLIFromEntry`: for a **cloud** entry
  (`SecretRef != ""`) the resolved `ANTHROPIC_API_KEY` / `CLAUDE_CODE_OAUTH_TOKEN` is present in the
  subprocess env; for a **local** entry (`SecretRef == ""`) `ANTHROPIC_BASE_URL == entry.Endpoint`
  and `ANTHROPIC_AUTH_TOKEN == LocalProxyAuthPlaceholder` with no real key. A fresh temp HOME is set.
- **REQ-135-04** — A non-zero CLI exit returns `("", err)` where `err` contains `"claude"` and the
  sanitized combined output; any auth token is redacted from the error (not present verbatim).
- **REQ-135-05** — The caller's `context.Context` is threaded into the subprocess (cancellation
  before/at start aborts the call and propagates the error). The returned string is empty on any
  error path.
- **REQ-135-06** — Boundary invariants unchanged: `Completer` lives in `internal/executor`;
  `make fitness` (F-010, F-014, supervisor isolation) stays green.

## Test cases

- **TC-135-01** (`TestCompleterForEntryClaudeReturnsCompleter`) — REQ-135-01: build a
  `HarnessClaudeCLI` entry; assert `CompleterForEntry` returns non-nil completer, nil error; assert
  `!errors.Is(err, ErrSingleShotUnsupported)`.
- **TC-135-02** (`TestClaudeCompleterRunsPrintModeAndReturnsStdout`) — REQ-135-02: inject a stub
  `commandCreator` capturing `name`+`args`; assert argv `== [<cliPath>, "-p", <prompt>]`; stub writes
  `"  Paris\n"` to stdout, exit 0; assert `Complete` returns exactly `"Paris"`.
- **TC-135-03** (`TestClaudeCompleterAuthEnvCloudVsLocal`) — REQ-135-03: cloud entry (SecretRef set,
  fake SecretSource returning a token) → captured env contains the token var; local entry
  (SecretRef=="", Endpoint set) → env contains `ANTHROPIC_BASE_URL=<endpoint>` and
  `ANTHROPIC_AUTH_TOKEN=<placeholder>`, and no real `ANTHROPIC_API_KEY` value.
- **TC-135-04** (`TestClaudeCompleterNonZeroExitSanitizedError`) — REQ-135-04: stub exits non-zero
  writing a line that includes the token; assert returned string is `""`, error non-nil, contains
  `"claude"`, and does NOT contain the raw token.
- **TC-135-05** (`TestClaudeCompleterContextCancellation`) — REQ-135-05: pass an already-cancelled
  ctx; assert error propagates and result is `""`.

## Non-vacuous / negative controls

- TC-135-02 asserts the exact stdout value (`Some("Paris")`-style), not merely "no error".
- TC-135-04 asserts the token is absent from the error (a real redaction check, not a smoke test).
- TC-135-03 asserts the local path injects the placeholder AND omits a real key (both directions).
