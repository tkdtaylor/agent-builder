# Test spec — Task 105: Ollama native harness — registry enum + runtime wiring

**Linked task:** `docs/tasks/backlog/105-ollama-harness-registry-wiring.md`
**Written:** 2026-06-28
**Status:** ready

## Context

With the Ollama client (task 102), agentic loop (task 103), and tool set (task 104)
in place, this task closes the seam: it adds the `HarnessOllamaNative` constant to
`internal/registry/types.go`, adds a `String()` case, wires a new case into
`buildExecutorForEntry` in `internal/runtime/run.go`, and updates the spec docs.

After this task, an operator can configure:

```bash
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED=true
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS=ollama-native
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT=http://localhost:11434
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL=qwen3:8b
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER=1
AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT=1
```

and have the router select it and the runtime construct an `OllamaNative` executor —
no LiteLLM, no Claude Code CLI.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-105-01 | TC-105-01                      | yes      |
| REQ-105-02 | TC-105-02                      | yes      |
| REQ-105-03 | TC-105-03, TC-105-04           | yes      |
| REQ-105-04 | TC-105-05                      | yes      |

---

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-105-01 — HarnessOllamaNative constant has the correct string value and String() returns it

- **Requirement:** REQ-105-01
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/types_test.go`

**Input:**
- Assert `registry.HarnessOllamaNative == "ollama-native"` (exact string value).
- Assert `registry.HarnessOllamaNative.String() == "ollama-native"`.
- Assert `registry.HarnessDriver("ollama-native").String() == "ollama-native"` (the
  `String()` switch must handle the new constant — not fall to `default`).
- Assert that the three existing constants are still present and unchanged:
  - `registry.HarnessClaudeCLI == "claude-cli"` (regression guard)
  - `registry.HarnessCodexCLI == "codex-cli"` (regression guard)
  - `registry.HarnessGeminiCLI == "gemini-cli"` (regression guard)

**Expected output:** all assertions pass, no compilation error.

---

### TC-105-02 — LoadFromEnv parses an ollama-native registry entry correctly

- **Requirement:** REQ-105-02
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go` (or `internal/registry/load_test.go`)

**Input:** Call `registry.LoadFromEnv()` with a `getenv` (via the test-injection
pattern used by existing registry tests) that sets:
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED=true`
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS=ollama-native`
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT=http://localhost:11434`
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL=qwen3:8b`
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER=1`
- `AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT=1`

**Expected output:**
- `LoadFromEnv` returns exactly one entry, no error.
- `entry.Harness == registry.HarnessOllamaNative`.
- `entry.Endpoint == "http://localhost:11434"`.
- `entry.ModelID == "qwen3:8b"`.
- `entry.SecretRef == ""` (local entry; no secret needed).
- `entry.CapabilityTier == 1`.
- `entry.CostWeight == 1`.
- `entry.IsUnlimited() == true` (Budget.Limit == 0 for a local entry).

---

### TC-105-03 — buildExecutorForEntry routes ollama-native to OllamaNative

- **Requirement:** REQ-105-03
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go`

**Input:** Call `buildExecutorForEntry` with a `registry.RegistryEntry{Harness: registry.HarnessOllamaNative, Endpoint: "http://localhost:11434", ModelID: "qwen3:8b"}` and a valid `Config`.

**Expected output:**
- Returns a non-nil `supervisor.Executor` and nil error.
- The returned value is of type `*executor.OllamaNative` (assert with a type
  assertion: `_, ok := exec.(*executor.OllamaNative); if !ok { t.Fatal(...) }`).

---

### TC-105-04 — buildExecutorForEntry still errors on an unknown harness driver

- **Requirement:** REQ-105-03
- **Level:** L2 (unit test — regression)
- **Test file:** `internal/runtime/run_test.go`

**Input:** Call `buildExecutorForEntry` with a
`registry.RegistryEntry{Harness: registry.HarnessDriver("bogus-harness")}`.

**Expected output:**
- Returns a non-nil error containing `"unknown harness"` or `"bogus-harness"`.
- The existing error path is still intact (this is a regression guard: adding the
  new case must not silently drop the default error).

---

### TC-105-05 — F-003 supervisor isolation preserved after wiring

- **Requirement:** REQ-105-04
- **Level:** L3 (fitness check)
- **Test file / harness:** `make fitness-supervisor-isolation`

**Input:** `make fitness-supervisor-isolation` after the new constant and wiring are
in place.

**Expected output:**
- Exits 0 with `PASS fitness-supervisor-isolation: supervisor import graph contains
  no executor/LLM/web-fetch packages.`
- `internal/supervisor` does NOT import `internal/executor`,
  `internal/executor/ollamaclient`, `internal/executor/ollamatoolset`, or
  `internal/registry`.
- `make check` passes in full.

**Rationale:** The wiring lives in `internal/runtime` (which is on the executor side
of the F-003 boundary). This test ensures the new import path does not accidentally
reach `internal/supervisor`.

---

## Verification plan

- **Highest level achievable in CI:** L2/L3.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/registry/... ./internal/runtime/... ./internal/executor/...
  ```
  Expected: all packages `ok`
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
- **Spec updates required in this task** (same commit as code, per project rules):
  - `docs/spec/interfaces.md` — add `HarnessOllamaNative` to the Executor Registry
    Interface section and add `executor.OllamaNative` as a concrete executor entry.
  - `docs/spec/configuration.md` — document the new `HARNESS=ollama-native` value
    in the executor registry env-var section.
  - `docs/spec/data-model.md` (if it lists harness driver values explicitly) — add
    `ollama-native`.
- **L6 (deferred, operator-run):** configure a live registry entry with
  `HARNESS=ollama-native`, run `agent-builder run`, and observe the `OllamaNative`
  executor being selected and dispatched through the full loop (branch produced,
  gate passes). Hardware-specific; deferred in the same style as tasks 094/101.

## Out of scope

- The Ollama HTTP client (task 102).
- The agentic loop (task 103).
- The tool set (task 104).
- Router behavior changes — the router already supports any `HarnessDriver` value;
  no router logic changes are required.
- A new fitness function — F-003 is sufficient.
