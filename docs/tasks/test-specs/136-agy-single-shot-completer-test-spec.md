# Test spec — Task 136: agy single-shot completer (`agy --print --model`)

**Task:** `docs/tasks/backlog/136-agy-single-shot-completer.md`
**Relates to:** ADR 059, ADR 057, `antigravity_cli.go` (`agy --print`, subscription env, sanitizer).

## Context

`CompleterForEntry` returns `ErrSingleShotUnsupported` for `HarnessAntigravityCLI`. This task makes
it return an `antigravityCompleter` running `agy --print <prompt> --model <model>` and returning
stdout — no worktree/branch/gate. Subscription/OAuth only (empty `SecretRef`); the inherited env lets
`agy` read `~/.antigravity`. **Confirm the exact arg order live before pinning the stub assertions.**

## Requirements

- **REQ-136-01** — `CompleterForEntry(entry)` with `entry.Harness == HarnessAntigravityCLI` returns
  a non-nil `Completer` and `nil` error (no longer `ErrSingleShotUnsupported`).
- **REQ-136-02** — `antigravityCompleter.Complete(ctx, entry, prompt)` invokes `agy` in print mode
  with the prompt and `--model <entry.ModelID>`, returning stdout trimmed. The argv contains
  `--print`, the prompt, `--model`, and the model id (exact order pinned from a live `agy --help`
  check). It does NOT pass `--add-dir` or `--dangerously-skip-permissions` (no worktree, no tools).
- **REQ-136-03** — The subprocess inherits the process environment (so `HOME` → `~/.antigravity`
  keyring); no API key is resolved or injected (subscription mode).
- **REQ-136-04** — A non-zero `agy` exit returns `("", err)` where `err` contains `"antigravity"` (or
  `"agy"`) and the sanitized combined output.
- **REQ-136-05** — The caller's `context.Context` is threaded into the subprocess; result is `""` on
  any error path.

## Test cases

- **TC-136-01** (`TestCompleterForEntryAntigravityReturnsCompleter`) — REQ-136-01: build an
  `HarnessAntigravityCLI` entry (empty `SecretRef`, a `ModelID`); assert non-nil completer, nil
  error, `!errors.Is(err, ErrSingleShotUnsupported)`.
- **TC-136-02** (`TestAgyCompleterRunsPrintModeAndReturnsStdout`) — REQ-136-02: stub `commandCreator`
  captures `name`+`args`; assert `name == "agy"`, args contain `"--print"`, the prompt, `"--model"`,
  the model id, and do NOT contain `"--add-dir"` / `"--dangerously-skip-permissions"`; stub writes
  `"Paris\n"`; assert `Complete` returns `"Paris"`.
- **TC-136-03** (`TestAgyCompleterNonZeroExitSanitizedError`) — REQ-136-04: stub exits non-zero;
  assert `""` result, error contains `"antigravity"`/`"agy"`.
- **TC-136-04** (`TestAgyCompleterContextCancellation`) — REQ-136-05: already-cancelled ctx → error
  propagates, result `""`.

## Non-vacuous / negative controls

- TC-136-02 asserts the exact returned value AND the absence of the agentic-mode flags
  (`--add-dir`, `--dangerously-skip-permissions`) — proving it is the single-shot, not the executor, path.
