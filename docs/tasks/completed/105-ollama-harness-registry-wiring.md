# Task 105 — Ollama native harness: registry enum + runtime wiring

**Status:** completed
**ID:** 105
**Slug:** ollama-harness-registry-wiring
**Priority:** must-have
**Dependencies:** tasks 102, 103, 104 (all three layers must exist first)
**Depends on tasks:** 102, 103, 104
**Blocks tasks:** none (this closes the seam)

**Spec:** `docs/tasks/test-specs/105-ollama-harness-registry-wiring-test-spec.md`
**ADR:** `docs/architecture/decisions/051-ollama-native-executor-harness.md`

---

## Goal

Wire the Ollama native executor into the registry and runtime:

1. Add `HarnessOllamaNative HarnessDriver = "ollama-native"` to `internal/registry/types.go`.
2. Add a `String()` case for the new constant.
3. Add a `HarnessOllamaNative` case to `buildExecutorForEntry` in `internal/runtime/run.go`.
4. Update spec docs (`interfaces.md`, `configuration.md`, `data-model.md`) in the same commit.

After this task, operators can point a registry entry at a local Ollama instance and
have the router select it and the runtime dispatch it — without LiteLLM or the Claude
Code CLI.

---

## Background

ADR 051 §4 specifies this wiring. The executor seam contract (`(harness, model) →
branch`) is unchanged. The new `HarnessDriver` value follows the identical pattern
as `HarnessCodexCLI` (task 089) and `HarnessGeminiCLI` (task 090). The router
already supports arbitrary `HarnessDriver` values; no router changes are needed.

---

## Requirements

### REQ-105-01 — HarnessOllamaNative constant is defined with value "ollama-native"

`registry.HarnessOllamaNative` MUST equal `HarnessDriver("ollama-native")`. The
`String()` switch MUST return `"ollama-native"` for this value — not the default
case.

### REQ-105-02 — LoadFromEnv parses a HARNESS=ollama-native registry entry

`registry.LoadFromEnv()` MUST parse an entry with
`AGENT_BUILDER_REGISTRY_<ID>_HARNESS=ollama-native` and return a `RegistryEntry`
with `Harness == HarnessOllamaNative`. The `SecretRef` field MUST be empty (local
entry, no cloud auth). The `Endpoint` and `ModelID` fields MUST be populated from the
env vars.

### REQ-105-03 — buildExecutorForEntry routes HarnessOllamaNative to OllamaNative

`buildExecutorForEntry` in `internal/runtime/run.go` MUST construct and return a
`*executor.OllamaNative` (not nil, not an error) when `entry.Harness ==
registry.HarnessOllamaNative`. The unknown-harness error path MUST remain intact
(regression guard).

### REQ-105-04 — F-003 supervisor isolation is preserved after wiring

`internal/supervisor` MUST NOT transitively import `internal/executor/ollamaclient`,
`internal/executor/ollamatoolset`, or `internal/registry`. Enforced by
`make fitness-supervisor-isolation`.

---

## Spec updates (same commit as code)

The following spec files MUST be updated in the same commit as the code change:

- **`docs/spec/interfaces.md`** — Executor Registry Interface section:
  - Add `HarnessOllamaNative HarnessDriver = "ollama-native"` to the constants block.
  - Add a `String()` case description.
  - Add a concrete executor entry for `executor.OllamaNative` following the pattern
    of the `CodexCLI` and `GeminiCLI` entries.
- **`docs/spec/configuration.md`** — Executor registry env-var section:
  - Document `HARNESS=ollama-native` as a valid harness value.
  - Document the required env vars for an Ollama-native entry:
    `ENDPOINT` (Ollama base URL) and `MODEL` (model ID).
  - Note the model requirement: the model must return structured `tool_calls` via
    `/api/chat` (confirmed for `qwen3:8b`; `qwen2.5-coder:7b` does NOT as of
    Ollama 0.17.7).
- **`docs/spec/data-model.md`** (if it enumerates `HarnessDriver` values) — add
  `"ollama-native"` to the list.

---

## Acceptance criteria

- [ ] **AC-105-01:** TC-105-01 passes: `registry.HarnessOllamaNative == "ollama-native"`;
  `HarnessOllamaNative.String() == "ollama-native"`; three existing harness constants
  unchanged.
- [ ] **AC-105-02:** TC-105-02 passes: `LoadFromEnv()` with `HARNESS=ollama-native`
  env vars returns one entry with `Harness==HarnessOllamaNative`, `SecretRef==""`,
  correct `Endpoint` and `ModelID`.
- [ ] **AC-105-03:** TC-105-03 passes: `buildExecutorForEntry` with a
  `HarnessOllamaNative` entry returns a `*executor.OllamaNative` and nil error.
- [ ] **AC-105-04:** TC-105-04 passes: `buildExecutorForEntry` with `"bogus-harness"`
  still returns a non-nil error (regression guard).
- [ ] **AC-105-05:** TC-105-05 passes: `make fitness-supervisor-isolation` exits 0.
- [ ] **AC-105-06:** Spec docs updated in the same commit: `interfaces.md`,
  `configuration.md`, and `data-model.md` (if applicable) reflect the new constant
  and wiring.
- [ ] **AC-105-07:** `make check` passes without any new warnings.

---

## Verification plan

- **Highest level achievable in CI:** L2/L3.
- **L2 command:**
  ```
  go test -count=1 ./internal/registry/... ./internal/runtime/... ./internal/executor/...
  ```
  Expected: all packages `ok`
- **L3 fitness:**
  ```
  make fitness-supervisor-isolation
  ```
- **Full gate:**
  ```
  make check
  ```
- **L6 (deferred, operator-run):** configure a live registry entry with
  `HARNESS=ollama-native`, run `agent-builder run`, and observe an `OllamaNative`
  executor being selected, dispatched, producing a branch, and passing the gate.
  This is the TC-094-02 follow-on; deferred per the same pattern.

---

## Out of scope

- The Ollama HTTP client (task 102).
- The agentic loop (task 103).
- The tool set (task 104).
- Router changes (none needed).
- A new fitness function (F-003 is sufficient).
