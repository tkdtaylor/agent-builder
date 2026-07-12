# Task 165: add the `validate_read` IPC verb to `internal/memoryguard.Client`

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add `Client.ValidateRead` to `internal/memoryguard`, the memory-guard block's third
IPC verb, mirroring the existing `ValidateWrite`/`VerifyDelete` wire pattern exactly.
This is a pure leaf-package addition with no wiring into a caller in this task.

## Context

**Deviation note.** This task ID was originally scoped by planning to "apply
`vault_injection_floor` on the vault client before token brokering." That gap does
not exist: `internal/runtime/run.go:1042` already raises `wiring.InjectionMode` via
`policy.RaiseInjectionFloor`, and `tests/e2e/policy_gate_e2e_test.go`'s
`TC-072-06C_allow_vault_injection_floor_raised` proves, on purpose, that the floor
raises even with no vault daemon configured (harmless: `SecretRefs` stays empty, so
`InjectionMode` is inert metadata on that request). This task ID is repurposed for a
real, independently-confirmed gap: memory-guard's read-gate verb has no client
wrapper anywhere in the codebase.

`internal/memoryguard.Client` implements `validate_write`
(`ValidateWrite`, `internal/memoryguard/memoryguard.go:109-138`) and `verify_delete`
(`VerifyDelete`, lines 140-169). `validate_read` has zero occurrences anywhere
(`grep -rn "ValidateRead\|validate_read" internal/` returns nothing). This is a
direct blocker for task 172 (`persistent-memory-store`), which needs every read
gated through memory-guard and cannot do that without a client method to call.
`internal/memoryguard/store.go:77-84`'s `MemoryGuardStore[P].Get` already documents
the gap explicitly: "purely in-process (no IPC on the read path in this task's
scope)", task 084's own doc comment naming this exact follow-on.

**Reference:**
- `internal/memoryguard/memoryguard.go:83-169` (`ValidateWrite`/`VerifyDelete` and
  their wire types, the pattern to mirror)
- `internal/memoryguard/store.go:77-84` (`MemoryGuardStore[P].Get`, the documented
  gap, NOT touched by this task, task 172's job)
- `docs/architecture/decisions/049-memory-guard-adoption.md` (ADR 049, the governing
  decision for this leaf)
- Cross-repo: the `memory-guard` block's own `validate_read(query, identity) →
  {allow, content_redacted, flags}` protocol (per the block's published IPC
  contract; mirrors `validate_write`'s shape one-for-one)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-165-01 | `Client.ValidateRead(query, identity string) (contentRedacted string, flags []string, err error)` sends `{"op":"validate_read",...}` and parses `{"allow","content_redacted","flags"}`. | must have |
| REQ-165-02 | `allow=false` returns sentinel `ErrReadGateDenied`, with `flags` from the response still returned alongside the error. | must have |
| REQ-165-03 | Transport/parse errors are wrapped, mirroring `ValidateWrite`'s exact error-message convention. | must have |
| REQ-165-04 | `internal/memoryguard` remains a strict leaf (F-012 unaffected). | must have |
| REQ-165-05 | Pre-existing `internal/memoryguard`/`internal/orchestrator` suites pass unchanged. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/165-memoryguard-validate-read-verb-test-spec.md` exists (written first)
- [x] Task 084 merged (`Client`, `ExecRunner` seam, `ValidateWrite`/`VerifyDelete` exist)
- [x] `make check` green on `main` before branching

## Implementation outline

1. In `internal/memoryguard/memoryguard.go`, add wire types mirroring
   `validateWriteRequest`/`validateWriteResponse`:
   ```go
   type validateReadRequest struct {
       Op       string `json:"op"`
       Query    string `json:"query"`
       Identity string `json:"identity"`
   }

   type validateReadResponse struct {
       Allow           bool     `json:"allow"`
       ContentRedacted string   `json:"content_redacted"`
       Flags           []string `json:"flags"`
   }
   ```
2. Add the sentinel: `var ErrReadGateDenied = errors.New("memoryguard: read-gate
   denied by memory-guard block")`, placed alongside `ErrWriteGateDenied`/`ErrTamperDetected`.
3. Add the method, mirroring `ValidateWrite` line for line (marshal request, run via
   `c.runner.Run`, wrap transport error, unmarshal response, wrap parse error, branch
   on `Allow`):
   ```go
   func (c *Client) ValidateRead(query, identity string) (contentRedacted string, flags []string, err error) {
       req := validateReadRequest{Op: "validate_read", Query: query, Identity: identity}
       reqJSON, err := json.Marshal(req)
       if err != nil {
           return "", nil, fmt.Errorf("memoryguard: marshal validate_read request: %w", err)
       }
       out, err := c.runner.Run(c.binPath, reqJSON)
       if err != nil {
           return "", nil, fmt.Errorf("memoryguard: validate_read subprocess: %w", err)
       }
       var resp validateReadResponse
       if err := json.Unmarshal(out, &resp); err != nil {
           return "", nil, fmt.Errorf("memoryguard: parse validate_read response %q: %w", out, err)
       }
       if !resp.Allow {
           return "", resp.Flags, ErrReadGateDenied
       }
       return resp.ContentRedacted, resp.Flags, nil
   }
   ```
4. Update the package doc comment (`internal/memoryguard/memoryguard.go:1-16`) to
   list all three verbs instead of two.
5. Add tests per the test spec: `internal/memoryguard/memoryguard_test.go` (extend)
   or a new `memoryguard_165_test.go`.

## Acceptance criteria

- [x] [REQ-165-01] TC-165-01/02: wire request/response shapes exact, field-by-field.
- [x] [REQ-165-02] TC-165-03: `allow=false` yields `ErrReadGateDenied` with flags preserved.
- [x] [REQ-165-03] TC-165-04/05: transport and parse errors wrapped per convention.
- [x] [REQ-165-04] TC-165-06: `make fitness-memoryguard-isolation` still passes.
- [x] [REQ-165-05] TC-165-07: `go test -race -count=1 ./internal/memoryguard/... ./internal/orchestrator/...` passes; `make check` passes.

## Verification plan

- **Highest level achievable:** L2, unit-test-only, no runtime surface (this task
  adds a leaf-package method with no live wiring; task 172 wires it into a caller
  and reaches L5/L6 there).
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/memoryguard/... -run TestValidateRead
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md`: the `internal/memoryguard` adapter section (grep
  `validate_write`/`verify_delete`) gains the `validate_read` verb entry.

## Out of scope

- Wiring `ValidateRead` into `MemoryGuardStore[P].Get` or any orchestrator call site
  (task 172/173).
- Any change to `ValidateWrite`/`VerifyDelete`.
- A live memory-guard binary integration test (no existing verb in this leaf has
  one; the `ExecRunner` seam is the established boundary).

## Dependencies

- **Blocks on:** task 084 (already merged).
- **Blocks:** task 172 (`persistent-memory-store` needs `ValidateRead` to exist
  before it can gate reads through it).
