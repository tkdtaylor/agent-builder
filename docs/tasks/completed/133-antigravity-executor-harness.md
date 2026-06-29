# Task 133 — Antigravity (`agy`) executor harness

**Status:** backlog
**Spec:** `docs/tasks/test-specs/133-antigravity-executor-harness-test-spec.md`
**Relates to:** task 132 / ADR 056 (Gemini subscription-mode pattern); task 090 (`GeminiCLI` shape);
ADR 033 (subscription auth); ADR 052 (gate-failure prompt injection); task 108 (cross-harness parity).

## Goal

Add **Antigravity (`agy`)** as a first-class executor harness so the multi-LLM router gains a real
third brain. The Gemini CLI backend was shut down 2026-06-18; `agy` is its successor and is installed
and working headlessly on this host. `agy` is itself multi-model (Gemini / Claude / GPT-OSS via
`--model`).

## Context (verified live on this host — `agy` v1.0.13)

- `agy --print "<prompt>"` runs a single prompt non-interactively and prints the response (returned
  `PONG` to a probe). `--model` selects the model; `agy models` lists them (e.g. "Gemini 3.5 Flash
  (High)", "Claude Opus 4.6 (Thinking)"). `--add-dir <dir>` adds a workspace dir;
  `--dangerously-skip-permissions` auto-approves tool use.
- Auth is self-managed via `~/.antigravity` (Google Sign-In keyring) — **no API key**. Signalled by an
  **empty `SecretRef`**, identical to the Gemini subscription mode (task 132 / ADR 056). The existing
  `ConfigFromEnv` "all enabled entries local → skip cloud-credential check" exemption covers it once
  the entry ID is allowlisted in the registry loader.

## Scope

- **`internal/executor/antigravity_cli.go` (new):** `AntigravityCLI` implementing
  `supervisor.Executor`, mirroring `GeminiCLI`'s structure. Subscription-only (empty `SecretRef`): no
  key resolution/injection; invoke `agy` in print mode with `--model`, `--add-dir <worktree>`,
  `--dangerously-skip-permissions`, and the task prompt; inherit env (preserve `HOME`); `cmd.Dir =
  worktree`. Build the prompt with the `BRANCH: <name>` convention (reuse the gemini prompt shape +
  ADR 052 PriorFailure section). Extract the branch from stdout. `ErrAntigravity{BlankWorktree,
  MissingBranch}` + a sanitized-output error on non-zero exit. Use a `commandCreator` factory for
  test stubbing (mirror `geminiCommandCreator`).
  - **Confirm the exact `agy` arg form live** (prompt positional vs `--print` value; exact `--model`
    token) before pinning the stub assertions — do not guess (the gemini executor's hardcoded
    interface is the cautionary tale). Do NOT pass `agy --sandbox` — isolation is provided by the
    OUTER exec-sandbox perimeter; `--dangerously-skip-permissions` is safe only because of it (note
    this rationale in a comment).
- **`internal/registry/types.go`:** add `HarnessAntigravityCLI HarnessDriver = "antigravity-cli"`
  (+ `String()` coverage). Keep the existing constants distinct.
- **`internal/runtime/run.go`:** `buildExecutorForEntry` case `HarnessAntigravityCLI` →
  `executor.NewAntigravityCLI(entry, src, config.Worktree)`.
- **`internal/registry/loader.go`:** allow the antigravity subscription entry to have empty
  `SecretRef` (extend `localHarnessEntries` for the `"antigravity"` entry ID, scoped exactly like
  task 132's `"gemini"` addition — verify it does not weaken fail-closed for cloud entries).
- **Spec (same commit):** `docs/spec/configuration.md` (antigravity subscription entry + model token
  + no key), `docs/spec/interfaces.md` (the executor + auth mode; now 5 harnesses),
  `docs/spec/data-model.md` (HarnessDriver enum gains `antigravity-cli`).
- **ADR:** short ADR (next free number — check `docs/architecture/decisions/`) — "Antigravity (`agy`)
  executor harness; multi-model agentic CLI; subscription/OAuth (self-managed keyring); successor to
  the shut-down Gemini CLI." Reference ADR 056/033/052.

## Out of scope

- Removing/deprecating the dead `GeminiCLI` executor — tracked separately (decision pending: keep as
  reference vs remove). Note it deprecated in the spec, but do not delete in this task.
- Router cost/capability tuning for Antigravity models (later).
- Per-model routing inside `agy` beyond setting `--model` from the entry (later).

## Verification plan

- **Highest level achievable now: L6** (`agy` works headlessly on this host).
- **L2/L3:** `go test -race -count=1 ./internal/executor/... ./internal/runtime/...` + `make check`;
  all TCs hard-assert exact values.
- **Producer→consumer trace:** antigravity subscription entry (`SecretRef == ""`) → `ConfigFromEnv`
  exemption → `buildExecutorForEntry` → `NewAntigravityCLI` → `run` → `agy --print …` subprocess
  (no API key; `~/.antigravity` keyring) → `BRANCH:` extraction → gate.
- **L6 (in-session):** register an antigravity subscription entry, route a scoped goal to it against a
  real target worktree, observe the agent edit the worktree, print `BRANCH:`, and the verify gate run
  green. (Main session drives this after spec-verifier APPROVE.)

## Boundaries

- Subscription-only auth (no API-key path needed — `agy` has no API-key mode here). Do not inject a
  placeholder key.
- Do not regress other executors; preserve the supervisor-isolation fitness (no executor imports leak
  into the supervisor).
- Test-spec-first; spec files updated in the same commit; commit at 🟡 on the task branch.
