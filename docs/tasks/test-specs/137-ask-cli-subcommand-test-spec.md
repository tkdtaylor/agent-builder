# Test spec — Task 137: `ask` CLI subcommand

**Task:** `docs/tasks/backlog/137-ask-cli-subcommand.md`
**Relates to:** ADR 059 (decision 2), tasks 135/136 (completers), `internal/cli/cli.go` dispatch, ADR 043 (registry/router).

## Context

`ask` is the first general (non-coding) entrypoint: select a brain → `Completer` → print the answer.
Selection is via optional `--entry <id>` (explicit) or the router default. To keep L2 hermetic, the
handler constructs completers through a package-level seam `completerForEntry` (default
`executor.CompleterForEntry`) that tests override with a fake returning canned text.

## Requirements

- **REQ-137-01** — `ask` is registered in the CLI dispatch and listed in the top-level usage.
  Invoking `ask` with no prompt prints usage to stderr and returns `ExitUsage`.
- **REQ-137-02** — `ask [--entry <id>] <prompt…>` joins the trailing args into the prompt, selects
  the entry, constructs a `Completer` via `completerForEntry`, calls `Complete(ctx, entry, prompt)`,
  prints the **trimmed answer** to `config.Stdout`, and returns `ExitOK`.
- **REQ-137-03** — `--entry <id>` selects that registry entry from `registry.LoadFromEnv`; an unknown
  id prints an error to stderr and returns a non-OK exit (`ExitUsage` or `ExitErr`). With no
  `--entry` and an empty registry, the synthetic default Claude entry is used.
- **REQ-137-04** — A `completerForEntry` error (unsupported harness) or a `Complete` error is printed
  to `config.Stderr` and returns a non-OK exit; nothing is printed to stdout in that case.

## Test cases

- **TC-137-01** (`TestAskSubcommandRegisteredAndUsage`) — REQ-137-01: top-level usage output contains
  `"ask"`; `Run({Args:["ask"]})` (no prompt) returns `ExitUsage` and writes usage to stderr.
- **TC-137-02** (`TestAskPrintsCompletionFromSelectedEntry`) — REQ-137-02/03: set an
  `--entry`-resolvable registry entry via env (or default Claude entry); override `completerForEntry`
  with a fake whose `Complete` returns `"  Paris\n"` and records the `(entry, prompt)` it received;
  run `ask --entry <id> "What is the capital of France?"`; assert stdout `== "Paris\n"` (trimmed +
  newline), `ExitOK`, and the fake received the joined prompt and the selected entry's id.
- **TC-137-03** (`TestAskUnknownEntryErrors`) — REQ-137-03: `ask --entry nope "x"` with `nope` absent
  from the catalog → non-OK exit, error on stderr, nothing on stdout.
- **TC-137-04** (`TestAskCompleterErrorSurfaced`) — REQ-137-04: fake `completerForEntry` returns
  `ErrSingleShotUnsupported`; assert non-OK exit, error on stderr naming the harness, stdout empty.

## Non-vacuous / negative controls

- TC-137-02 asserts the exact stdout (`"Paris\n"`) and that the fake received the exact prompt — not
  merely that the call returned `ExitOK`.
- TC-137-03 / TC-137-04 assert stdout is empty on the error paths (no partial/garbage answer).
