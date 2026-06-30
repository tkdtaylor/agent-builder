# Task 137 — `ask` CLI subcommand (general non-coding entrypoint)

**Status:** backlog
**Spec:** `docs/tasks/test-specs/137-ask-cli-subcommand-test-spec.md`
**Relates to:** ADR 059 (decision 2), tasks 135/136 (cloud completers), ADR 043 (registry/router), task 023 (CLI subcommands).

## Goal

Add `agent-builder ask [--entry <id>] <prompt…>` — the first general (non-coding) entrypoint. It
selects a registry brain, constructs its `Completer`, and prints the raw answer to stdout. Lets an
operator ask a question (e.g. "what is the capital of France?") and route it to local / Claude / agy.

## Scope

- **`internal/cli/cli.go`**: register `case "ask"` in the dispatch and list it in `printUsage`.
- **`internal/cli/ask.go`** (new): `runAsk(config, args)`:
  - Parse `--entry <id>` (optional) and the trailing prompt (joined remaining args). Empty prompt → `ExitUsage` with usage text.
  - **Entry selection:** with `--entry`, load the registry catalog (`registry.LoadFromEnv`) and pick that entry (unknown id → error, `ExitUsage`/`ExitErr`). Without `--entry`, use the router's default selection, or the synthetic default Claude entry when the registry is empty (reuse the run-path catalog/selection helpers).
  - Construct the completer via a package-level seam `completerForEntry` (defaults to `executor.CompleterForEntry`) so L2 tests stub it; call `Complete(ctx, entry, prompt)`; print the trimmed answer to `config.Stdout`; `ExitOK`.
  - `CompleterForEntry` error (e.g. unsupported harness like codex/gemini) or `Complete` error → print to `config.Stderr`, return non-OK exit.
- **Spec (same commit):** `docs/spec/interfaces.md` (CLI surface gains `ask`) + `docs/spec/configuration.md` if any new flag/env is documented.

## Out of scope

- Routing policy beyond default selection / explicit `--entry`; streaming output; multi-turn chat.

## Verification plan

- **Highest level now: L2/L3 + L6 smoke.**
- **L2:** `go test -race -count=1 ./internal/cli/...` — `ask` registered + in usage; no-prompt → `ExitUsage`; with a stubbed `completerForEntry`, prints the answer + `ExitOK`; unknown `--entry` → error exit; unsupported-harness error surfaced.
- **L3:** `make check` green.
- **L6 (smoke, this host):** `agent-builder ask --entry <id> "What is the capital of France?"` against **local ollama**, **Claude**, and **agy** entries each returns an answer containing "Paris". Record the three outputs.

## Boundaries

- `ask` does not run the verification gate (nothing to verify in a text answer) — read-only inference (ADR 059).
- CLI wiring stays in `internal/cli`; the completer construction stays behind `executor.CompleterForEntry`.
