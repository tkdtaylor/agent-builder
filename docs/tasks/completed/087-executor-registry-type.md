# Task 087: Executor registry type + entry config

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** backlog

## Goal

Introduce the executor registry: the `RegistryEntry` struct, the `HarnessDriver`
discriminator type, the `QuotaBudget` and `Availability` value types, and the
in-process catalog (`RegisterEntry`, `LookupEntry`, `ListEntries`) in a new package
`internal/registry`.

Per ADR 043, the registry separates a **harness driver** (which CLI runs the loop)
from the **(model, endpoint, auth) config** it points at — so one harness backs many
entries. The registry stores provider config and quota state; it never stores secrets
(those are named by `SecretRef` and resolved by vault at dispatch, task 088).

Also introduce env-driven entry construction via `LoadFromEnv()` so that which entries
are enabled and their endpoints can be tuned per-deployment without code changes.

## Context

ADR 043 defines the `RegistryEntry` struct. Today there is no such struct — the single
Claude CLI executor is constructed inline in `internal/runtime`. This task creates the
data model that the router (task 092) and harness adapters (tasks 089, 090, 091) depend on.

### Allowed imports for `internal/registry`

Stdlib only (including `time`, `os`, `strconv`, `sync` for the catalog). No imports
from other `agent-builder/internal` packages — this is a leaf data package.

Forbidden: `internal/supervisor`, `internal/executor`, `internal/router`,
`internal/runtime`, `internal/vault`, `internal/policy`, `internal/secrets`.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                        | Priority  |
|------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-087-01 | A `RegistryEntry` Go struct (package `internal/registry`) with all fields from ADR 043: `ID string`, `Harness HarnessDriver`, `CapabilityTier int`, `CostWeight int`, `ModelID string`, `Endpoint string`, `SecretRef string`, `Budget QuotaBudget`, `Usage int`, `Availability Availability`. `HarnessDriver` is a typed constant with values `HarnessClaudeCLI`, `HarnessCodexCLI`, `HarnessGeminiCLI`. `QuotaBudget` has `Limit int` and `Window time.Duration`; zero means unlimited. `Availability` has `Status AvailStatus` and `ResetAt time.Time`; `AvailStatus` has `AvailStatusAvailable` and `AvailStatusExhausted`. | must have |
| REQ-087-02 | `LoadFromEnv() ([]RegistryEntry, error)` — reads well-known env-var prefixes (`AGENT_BUILDER_REGISTRY_<ID>_*`) for each known entry ID, constructs and returns enabled entries. Missing required fields for an enabled entry return a descriptive error. | must have |
| REQ-087-03 | An in-process catalog type (`Catalog`) with `RegisterEntry(e RegistryEntry)` (panics or errors on duplicate ID), `LookupEntry(id string) (RegistryEntry, bool)`, and `ListEntries() []RegistryEntry` (stable, deterministic order). | must have |
| REQ-087-04 | `internal/registry` is a leaf: `go list -deps ./internal/registry/...` contains no `agent-builder/internal/` imports other than `internal/registry` itself. `make check` exits 0. | must have |

## Readiness gate

- [x] Test spec `087-executor-registry-type-test-spec.md` exists (written first)
- [ ] ADR 043 read and understood
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-087-01] TC-087-01: `RegistryEntry` constructed with all fields compiles and round-trips; `HarnessDriver` has three distinct constants; `QuotaBudget`/`Availability`/`AvailStatus` compile as described
- [ ] [REQ-087-01] TC-087-02: All three `HarnessDriver` constants are distinct; `String()` returns human-readable names
- [ ] [REQ-087-02] TC-087-03: `LoadFromEnv()` with env vars for `claude-oauth` returns a matching entry; `ENABLED=false` excludes the entry; missing required field → descriptive error; non-integer tier → descriptive error
- [ ] [REQ-087-03] TC-087-04: `RegisterEntry` + `LookupEntry` round-trip; `LookupEntry("unknown")` → `(_, false)`; `LookupEntry("")` → `(_, false)`
- [ ] [REQ-087-03] TC-087-05: Duplicate `RegisterEntry` (same ID) → panic or error naming the duplicate
- [ ] [REQ-087-04] TC-087-06: `go list -deps ./internal/registry/...` → no `internal/supervisor`, `internal/executor`, `internal/router`, `internal/runtime`, `internal/vault`, `internal/policy`; `make check` → `All checks passed.`

## Verification plan

- **Highest level achievable:** L3 — no runtime-observable surface. Compile + unit
  tests + import-graph check.
- **Harness command:**
  ```
  go test -count=1 ./internal/registry/...
  go list -deps ./internal/registry/...
  make check
  ```
  Expected:
  - Unit tests → `ok github.com/tkdtaylor/agent-builder/internal/registry`
  - `go list` → no forbidden packages
  - `make check` → `All checks passed.`

## Out of scope

- The router (task 092) that reads entries and selects among them.
- Harness adapters (tasks 089, 090, 091).
- Vault secret resolution (task 088).
- Mutable quota state updates — the fields exist on the entry struct for the router
  to update; the registry package itself does not enforce quota logic.
- Persistence of quota state (task 093).

## Dependencies

- None (this is the first task in the ADR 043 cluster; all subsequent tasks depend on it).
- Informs: tasks 088, 089, 090, 091, 092, 093, 094, 095.
