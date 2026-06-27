# Test spec — Task 087: Executor registry type + entry config

**Linked task:** `docs/tasks/backlog/087-executor-registry-type.md`
**Written:** 2026-06-27
**Status:** ready

## Context

This task introduces the executor registry: the `RegistryEntry` struct, the
`HarnessDriver` discriminator, the `QuotaBudget` and `Availability` types, and the
in-process catalog (`RegisterEntry`, `LookupEntry`, `ListEntries`). All of this lives
in a new package `internal/registry`.

The registry is a Go-typed, in-process catalog: entries are first-class Go values.
Per-deployment tuning (which entries are enabled, their endpoints, their secret refs)
is driven by env vars, matching the block-wiring convention.

**Leaf constraint:** `internal/registry` must not import `internal/supervisor`,
`internal/executor`, `internal/router`, `internal/runtime`, or any LLM/harness
concrete. It is a plain data + catalog package.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-087-01 | TC-087-01, TC-087-02           | yes      |
| REQ-087-02 | TC-087-03                      | yes      |
| REQ-087-03 | TC-087-04, TC-087-05           | yes      |
| REQ-087-04 | TC-087-06                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-087-01 — RegistryEntry struct compiles with all fields; zero value is valid

- **Requirement:** REQ-087-01
- **Level:** L2 (compile-time + unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Construct a `RegistryEntry` value with all fields populated:
```go
entry := registry.RegistryEntry{
    ID:             "claude-oauth",
    Harness:        registry.HarnessClaudeCLI,
    CapabilityTier: 3,
    CostWeight:     10,
    ModelID:        "claude-opus-4-5",
    Endpoint:       "https://api.anthropic.com",
    SecretRef:      "claude-oauth-token",
    Budget:         registry.QuotaBudget{Limit: 100, Window: 5 * time.Hour},
    Usage:          0,
    Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
}
```

**Expected output:**
- The package compiles without error.
- `entry.ID == "claude-oauth"`, `entry.Harness == HarnessClaudeCLI`,
  `entry.CapabilityTier == 3`, `entry.CostWeight == 10`.
- A zero-value `RegistryEntry{}` is also valid (no constructor required for the type
  itself; the catalog validates before inserting — see REQ-087-03).

**Type assertions:**
- `HarnessDriver` is a discriminated type (string constant or int iota) with at least
  three values: `HarnessClaudeCLI`, `HarnessCodexCLI`, `HarnessGeminiCLI`.
- `QuotaBudget` has fields `Limit int` and `Window time.Duration`. A zero `QuotaBudget`
  (`Limit == 0`) means unlimited (no budget cap).
- `Availability` has fields `Status AvailStatus` and `ResetAt time.Time`.
- `AvailStatus` has at least two values: `AvailStatusAvailable` and `AvailStatusExhausted`.

---

### TC-087-02 — HarnessDriver discriminator covers all ADR-043 harnesses

- **Requirement:** REQ-087-01
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Enumerate the three concrete harness constants in a table test.

**Expected output:**
- `HarnessClaudeCLI`, `HarnessCodexCLI`, `HarnessGeminiCLI` all have distinct values.
- No two constants are equal.
- A `String()` method (or equivalent) returns a human-readable name (e.g. `"claude-cli"`).

---

### TC-087-03 — Env-driven entry construction via LoadFromEnv

- **Requirement:** REQ-087-02
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Set env vars for the `claude-oauth` entry:
```
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED=true
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT=https://api.anthropic.com
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF=claude-oauth-token
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL=claude-opus-4-5
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER=3
AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT=10
```
Call `registry.LoadFromEnv()`.

**Expected output:**
- Returns a slice that includes an entry with `ID == "claude-oauth"`,
  `Harness == HarnessClaudeCLI`, `CapabilityTier == 3`, `CostWeight == 10`,
  `SecretRef == "claude-oauth-token"`.
- Setting `ENABLED=false` (or omitting all vars for a known entry) returns a slice
  that does NOT include that entry.
- `LoadFromEnv` returns only Go values, never the secret itself (`SecretRef` is a
  name, not the credential).

**Edge cases:**
- Missing required field (e.g. `ENDPOINT` missing for an enabled entry) → descriptive
  error naming the entry and the missing field.
- `CAPABILITY_TIER` set to a non-integer string → descriptive error.

---

### TC-087-04 — RegisterEntry + LookupEntry round-trip

- **Requirement:** REQ-087-03
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Call `catalog.RegisterEntry(entry)` where `entry.ID == "local-qwen"`.
Then call `catalog.LookupEntry("local-qwen")`.

**Expected output:**
- `LookupEntry("local-qwen")` returns `(RegistryEntry, true)`.
- The returned entry has the same field values as the registered entry.
- `LookupEntry("unknown")` returns `(RegistryEntry{}, false)`.
- `LookupEntry("")` returns `(RegistryEntry{}, false)`.

---

### TC-087-05 — Duplicate RegisterEntry panics or errors loudly

- **Requirement:** REQ-087-03
- **Level:** L2 (unit test)
- **Test file:** `internal/registry/registry_test.go`

**Input:** Call `catalog.RegisterEntry(entry)` twice with the same `ID`.

**Expected output:**
- The second call panics (or returns a non-nil error if the API returns errors)
  naming the duplicate `ID`.
- The behavior is deterministic and loud, not last-writer-wins.

---

### TC-087-06 — internal/registry is a leaf: no supervisor, executor, router, runtime imports

- **Requirement:** REQ-087-04
- **Level:** L3 (import-graph)
- **Test file / harness:** `go list -deps ./internal/registry/...`

**Input:** `go list -deps ./internal/registry/...`

**Expected output:**
- The output contains `github.com/tkdtaylor/agent-builder/internal/registry`.
- The output does NOT contain any of:
  - `github.com/tkdtaylor/agent-builder/internal/supervisor`
  - `github.com/tkdtaylor/agent-builder/internal/executor`
  - `github.com/tkdtaylor/agent-builder/internal/router`
  - `github.com/tkdtaylor/agent-builder/internal/runtime`
  - `github.com/tkdtaylor/agent-builder/internal/vault`
  - `github.com/tkdtaylor/agent-builder/internal/policy`
- `go list` exits 0.

**Rationale:** The registry is a data + catalog package. It stores config (IDs,
endpoints, tiers, secret refs) and mutable state (Usage, Availability), but it must
not depend on the components that use it. Keeping it leaf-clean ensures the router
and the runtime can import it without cycles.

---

## Verification plan

- **Highest level achievable:** L3 — the registry package has no runtime-observable
  surface of its own. Compile + unit tests + import-graph check are the verification.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/registry/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/registry`
- **L3 import-graph check:**
  ```
  go list -deps ./internal/registry/...
  ```
  Expected: no `internal/supervisor`, `internal/executor`, `internal/router`,
  `internal/runtime`, `internal/vault`, `internal/policy` in output.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- The router that reads from the registry (task 092).
- Any harness adapter (tasks 089, 090, 091).
- Vault secret resolution (task 088).
- Mutable quota/usage state updates — those are owned by the router (task 092).
  The registry type has the `Usage` and `Availability` fields for the router to
  update; the registry itself does not enforce quota logic.
- Persistence of quota state (covered in task 093).
