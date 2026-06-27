# Test spec — Task 090: Gemini harness adapter

**Linked task:** `docs/tasks/backlog/090-gemini-harness-adapter.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 identifies Gemini as one of two genuinely new harness adapters (the other is
Codex, task 089). The Gemini CLI (`gemini`) has its own subprocess interface, its own
wire format, and its own auth (Gemini API key) — it does not reuse the Claude CLI
harness.

This task introduces `internal/executor/gemini_cli.go`, a `supervisor.Executor`
implementation that invokes the `gemini` CLI subprocess, captures the branch it
produces, and handles auth via the `SecretRef` resolved by `secrets.NamedProviderToken`
(task 088).

The implementation pattern mirrors `claude_cli.go` and `codex_cli.go`.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-090-01 | TC-090-01, TC-090-02           | yes      |
| REQ-090-02 | TC-090-03                      | yes      |
| REQ-090-03 | TC-090-04                      | yes      |
| REQ-090-04 | TC-090-05                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-090-01 — GeminiCLI executor satisfies supervisor.Executor interface

- **Requirement:** REQ-090-01
- **Level:** L2 (compile-time + unit test)
- **Test file:** `internal/executor/gemini_cli_test.go`

**Input:** Compile-time assertion:
```go
var _ supervisor.Executor = (*executor.GeminiCLI)(nil)
```

**Expected output:**
- Compiles without error.
- `executor.NewGeminiCLI(entry RegistryEntry, secretSource secrets.SecretSource)` returns
  a value satisfying `supervisor.Executor`.

---

### TC-090-02 — GeminiCLI invokes the gemini subprocess with correct argv and env

- **Requirement:** REQ-090-01
- **Level:** L2 (unit test with stub subprocess)
- **Test file:** `internal/executor/gemini_cli_test.go`

**Input:** Construct a `GeminiCLI` with a fake `secretSource` returning
`"test-gemini-key"` for the configured `SecretRef`. Inject a fake subprocess launcher.
Call `executor.Run(ctx, task, worktree)`.

**Expected output:**
- The stub subprocess is invoked with at least:
  - `GEMINI_API_KEY=test-gemini-key` (or `GOOGLE_API_KEY` — whichever the Gemini CLI
    uses; implementation picks the correct var name).
  - The worktree path.
  - The model ID from the config.
- `executor.Run` returns `(Result{Branch: "task/090-test-branch", OK: true}, nil)`.

---

### TC-090-03 — Auth token is resolved via NamedProviderToken, not hardwired

- **Requirement:** REQ-090-02
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/gemini_cli_test.go`

**Input:** Two fake `secretSource` variants:
- Variant A: `NamedProviderToken("gemini-api-key")` → `"test-gemini-key"`
- Variant B: `NamedProviderToken("gemini-api-key")` → `("", ErrSecretNotFound)`

**Expected output:**
- Variant A: subprocess invoked; `Run` returns OK.
- Variant B: `Run` returns error before subprocess invocation; error names the failed
  secret resolution.

---

### TC-090-04 — Subprocess non-zero exit surfaces as executor error

- **Requirement:** REQ-090-03
- **Level:** L2 (unit test)
- **Test file:** `internal/executor/gemini_cli_test.go`

**Input:** Stub subprocess exits 1 with stderr `"Gemini API error"`.

**Expected output:**
- `executor.Run` returns non-nil error containing stderr text.
- `Result.OK == false`.

---

### TC-090-05 — F-003 supervisor isolation preserved after adding GeminiCLI

- **Requirement:** REQ-090-04
- **Level:** L3 (fitness check)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after `GeminiCLI` is added.

**Expected output:**
- `PASS fitness-supervisor-isolation: …` (exit 0).
- `internal/supervisor` does not import `internal/executor`.

---

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess. Live Gemini run
  (L6) requires a Gemini API key and is operator-run.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/executor/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/executor`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
  Expected: `PASS fitness-supervisor-isolation: …`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Router selection (task 092).
- End-to-end recipe→Gemini flow (task 095).
- Gemini CLI flag names are assumed from published docs; if the CLI API changes, the
  adapter must be updated.
