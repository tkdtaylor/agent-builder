# Test spec — Task 091: Local entry + translation-proxy seam

**Linked task:** `docs/tasks/backlog/091-local-entry-translation-proxy.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 establishes that a local LLM is NOT a new harness — it is the same Claude CLI
harness configured with a local endpoint and no cloud auth. Claude Code CLI honors
`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`, so pointing it at a different endpoint
drives a different model.

Most local inference servers (llama.cpp, vLLM, Ollama, LM Studio) speak the OpenAI
API. Because Claude Code speaks the Anthropic Messages API, a local entry typically
fronts the model with a **translation proxy** — an Anthropic-API-compatible front
over an OpenAI-API local server (the LiteLLM / claude-code-router pattern).

This task:
1. Adds the `"local-qwen"` (or generic `"local"`) entry to `registry.LoadFromEnv()`.
2. Documents and names the **translation-proxy seam** — the `Endpoint` field in the
   `RegistryEntry` points at the proxy, not at the model directly.
3. Proves that the existing `ClaudeCLI` adapter (unchanged) drives a local model when
   its endpoint is pointed at the proxy.

No new harness code is written. This is a config-variant task, not a code-authoring task.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-091-01 | TC-091-01                      | yes      |
| REQ-091-02 | TC-091-02                      | yes      |
| REQ-091-03 | TC-091-03, TC-091-04           | yes      |
| REQ-091-04 | TC-091-05                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-091-01 — Local entry registered via LoadFromEnv with local-specific fields

- **Requirement:** REQ-091-01
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go` (extends task 087 tests)

**Input:** Set env vars:
```
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENABLED=true
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENDPOINT=http://localhost:8080
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_MODEL=qwen2.5-coder-7b-instruct
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_CAPABILITY_TIER=1
AGENT_BUILDER_REGISTRY_LOCAL_QWEN_COST_WEIGHT=1
# SecretRef is empty — no cloud auth for local
```
Call `registry.LoadFromEnv()`.

**Expected output:**
- Returns an entry with `ID == "local-qwen"`, `Harness == HarnessClaudeCLI`,
  `SecretRef == ""` (empty — local entries have no cloud auth), `Budget == QuotaBudget{}`
  (zero — unlimited).
- A local entry with `SecretRef == ""` and `Budget.Limit == 0` is valid (unlimited,
  no auth required).

---

### TC-091-02 — Translation-proxy seam: Endpoint field points at the proxy, not the model

- **Requirement:** REQ-091-02
- **Level:** L2 (documentation assertion + unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Inspect the `RegistryEntry` returned for a local entry.

**Expected output:**
- `entry.Endpoint == "http://localhost:8080"` (the proxy address, not a model path).
- The entry's `Harness == HarnessClaudeCLI` — the same harness that drives cloud
  Claude, now pointed at a local proxy that translates Anthropic API → OpenAI API → model.
- A source comment or doc comment in `internal/registry` names the translation-proxy
  pattern (LiteLLM / claude-code-router) as the named seam for local entries.

---

### TC-091-03 — ClaudeCLI adapter drives a local model via the proxy endpoint

- **Requirement:** REQ-091-03
- **Level:** L2 (unit test with stub subprocess)
- **Test file:** `internal/executor/claude_cli_test.go` (extend existing)

**Input:** Construct a `ClaudeCLI` from a local `RegistryEntry`:
- `Endpoint = "http://localhost:8080"`
- `SecretRef = ""`
- `ModelID = "qwen2.5-coder-7b-instruct"`

**Expected output:**
- The subprocess is invoked with `ANTHROPIC_BASE_URL=http://localhost:8080` (or
  equivalent env var that redirects the Claude CLI to the proxy).
- `ANTHROPIC_API_KEY` and `CLAUDE_CODE_OAUTH_TOKEN` are NOT set (empty `SecretRef`
  → no cloud auth injected).
- The stub subprocess exits 0 and returns a branch name.
- `executor.Run` returns OK.

---

### TC-091-04 — Local entry is never marked exhausted (Budget zero = unlimited)

- **Requirement:** REQ-091-03
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** A `RegistryEntry` with `Budget == QuotaBudget{}` (zero value).

**Expected output:**
- A helper function `entry.IsUnlimited()` (or equivalent) returns `true` when
  `Budget.Limit == 0`.
- The router (task 092) must never call `Availability = exhausted` on an unlimited
  entry — but this task only establishes the predicate; the router enforces it.

---

### TC-091-05 — No new harness code: ClaudeCLI accepts RegistryEntry constructor

- **Requirement:** REQ-091-04
- **Level:** L2 (compile-time)
- **Test file:** `internal/executor/claude_cli_test.go`

**Input:** Compile-time assertion that `executor.NewClaudeCLIFromEntry` (or an updated
`executor.NewClaudeCLI` that accepts a `RegistryEntry`) compiles alongside the
existing `executor.NewClaudeCLI` constructor.

**Expected output:**
- Both compile without error.
- The existing `NewClaudeCLI(cfg, secretSource)` call sites still compile (backward
  compatible). This is an additive constructor overload, not a breaking change.
- `ClaudeCLI` constructed from a `RegistryEntry` with a local endpoint uses
  `ANTHROPIC_BASE_URL` rather than the default Anthropic API URL.

---

## Verification plan

- **Highest level achievable:** L5 — unit tests with stub subprocess for the
  ClaudeCLI-via-local-endpoint case. L6 (live local inference) requires a running
  translation proxy and a local model; it is operator-run and not automated.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/registry/... ./internal/executor/...
  ```
  Expected: both packages `ok`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`
- **L6 live (operator-run):** start a local inference server (llama.cpp or Ollama)
  + translation proxy (LiteLLM or claude-code-router) on `localhost:8080`; set
  env vars for the local entry; exercise the adapter against a real worktree.

## Out of scope

- Local model selection / benchmarking (task 094 — empirical, hardware-specific).
- Router selecting the local entry as a fallback (task 092).
- End-to-end recipe→local flow (task 095).
- Building or shipping a translation proxy — the seam is named and documented, but
  the proxy itself is an external tool (LiteLLM, claude-code-router, etc.).
