# Test Spec 165: add the `validate_read` IPC verb to `internal/memoryguard.Client`

**Linked task:** [`docs/tasks/backlog/165-memoryguard-validate-read-verb.md`](../backlog/165-memoryguard-validate-read-verb.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

**Deviation note.** This task ID was originally scoped by planning to "apply the
`vault_injection_floor` obligation on the vault client before token brokering."
Investigation found that gap does not exist as described: `internal/runtime/run.go`'s
`decideGate` already raises `wiring.InjectionMode` via `policy.RaiseInjectionFloor`
(`run.go:1042`), and `tests/e2e/policy_gate_e2e_test.go`'s
`TC-072-06C_allow_vault_injection_floor_raised` proves live, on purpose, that the
floor is raised even when no vault daemon is configured (`InjectionMode` becomes an
inert metadata field on the box request when there is nothing to inject, since
`SecretRefs` stays empty). Reversing that would contradict an already-shipped,
already-tested design decision, not close a gap. This task ID is repurposed for a
real, narrow, independently-confirmed gap in the SAME "obligation/governance
consumption" area of the codebase, one this repo's own forward roadmap needs: the
memory-guard adapter never implements the read-gate verb.

`internal/memoryguard.Client` (`internal/memoryguard/memoryguard.go`) implements two
of the memory-guard block's three IPC verbs: `validate_write` (`ValidateWrite`,
lines 109-138) and `verify_delete` (`VerifyDelete`, lines 140-169). The third verb,
`validate_read`, has zero occurrences anywhere in this codebase (`grep -rn
"ValidateRead\|validate_read" internal/` returns nothing). This blocks task 172
(`persistent-memory-store`) directly: a durable memory store that gates every write
AND every read through memory-guard cannot exist while the client has no read-gate
method to call. `internal/orchestrator/memoryguard.go`'s existing
`MemoryGuardStore[P].Get` (`internal/memoryguard/store.go:77-84`) is explicitly
documented as "purely in-process (no IPC on the read path in this task's scope)",
confirming this is a known, intentional omission carried forward from task 084, not
a bug in that task, just an unfinished verb this task now completes.

**Module boundary touched:** `internal/memoryguard` only, a strict leaf (F-012
fitness: no other `agent-builder/internal/` import). This task adds one method and
two unexported wire types to that leaf; it does not touch `internal/orchestrator`
or wire the new method into `MemoryGuardStore` (task 172 does that).

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-165-01 | `Client` gains `func (c *Client) ValidateRead(query, identity string) (contentRedacted string, flags []string, err error)`, sending `{"op":"validate_read","query":...,"identity":...}` and parsing `{"allow":bool,"content_redacted":string,"flags":[]string}`, mirroring `ValidateWrite`'s existing shape exactly. | TC-165-01, TC-165-02 |
| REQ-165-02 | A `false` `allow` in the response returns a new sentinel `ErrReadGateDenied` (distinct from `ErrWriteGateDenied`), with `flags` from the response still returned alongside the error (a denial's flags carry the reason, e.g. `["policy_violation"]`). | TC-165-03 |
| REQ-165-03 | A transport error (subprocess failure) or a parse error (malformed JSON) is returned wrapped, mirroring `ValidateWrite`'s existing error-wrapping convention (`fmt.Errorf("memoryguard: validate_read subprocess: %w", err)` / `"memoryguard: parse validate_read response %q: %w"`). | TC-165-04, TC-165-05 |
| REQ-165-04 | `internal/memoryguard` remains a strict leaf: `go list -deps ./internal/memoryguard/...` still reports no `agent-builder/internal/` path other than itself (F-012 unaffected). | TC-165-06 |
| REQ-165-05 | Pre-existing `internal/memoryguard`, `internal/orchestrator` suites continue to pass unchanged (this task adds a method, it does not modify `ValidateWrite`/`VerifyDelete`/`MemoryGuardStore`). | TC-165-07 |

---

## Pre-implementation checklist

- [x] Task 084 merged (`internal/memoryguard` leaf, `Client`, `ExecRunner` seam,
  `ValidateWrite`/`VerifyDelete` all already exist, this task extends the same
  `Client` type)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-165-01, `ValidateRead` sends the correct wire request

- **Requirement:** REQ-165-01
- **Level:** L2 (unit test, mirrors the existing `TestValidateWrite*` shape in
  `internal/memoryguard/memoryguard_test.go`)
- **Test file:** `internal/memoryguard/memoryguard_test.go` (extend) or
  `memoryguard_165_test.go` (new)

**Step:** Construct `Client` with a recording `ExecRunner` stub (mirrors the
existing test doubles in `memoryguard_test.go`) that captures the raw request bytes
and returns a canned `{"allow":true,"content_redacted":"[REDACTED]","flags":[]}`.
Call `client.ValidateRead("goal:abc123", "agent-builder/orchestrator")`.

**Expected output:** the captured request unmarshals to
`{"op":"validate_read","query":"goal:abc123","identity":"agent-builder/orchestrator"}`
exactly (field-by-field assertion, not a smoke check); the call returns
`contentRedacted == "[REDACTED]"`, `flags == nil` (or empty slice, per the response),
`err == nil`.

---

### TC-165-02, allow=true returns the redacted content and no error

- **Requirement:** REQ-165-01
- **Level:** L2

**Step:** Stub response `{"allow":true,"content_redacted":"plan text here","flags":["stale"]}`.
Call `ValidateRead`.

**Expected output:** `contentRedacted == "plan text here"`, `flags == []string{"stale"}`,
`err == nil`.

---

### TC-165-03, allow=false returns `ErrReadGateDenied` with flags preserved

- **Requirement:** REQ-165-02
- **Level:** L2

**Step:** Stub response `{"allow":false,"content_redacted":"","flags":["policy_violation"]}`.
Call `ValidateRead`.

**Expected output:** `errors.Is(err, memoryguard.ErrReadGateDenied) == true`;
`flags == []string{"policy_violation"}` (the returned flags slice is non-nil and
matches the response even though `allow` was false, so a caller can log WHY the read
was denied); `contentRedacted == ""`.

---

### TC-165-04, subprocess/transport error is wrapped

- **Requirement:** REQ-165-03
- **Level:** L2

**Step:** Stub `ExecRunner.Run` to return `(nil, errors.New("exec: binary not found"))`.
Call `ValidateRead`.

**Expected output:** `err != nil`; `strings.Contains(err.Error(), "validate_read
subprocess")`; `errors.Unwrap(err)` reaches the original `"exec: binary not found"`
error (`errors.Is` against it holds).

---

### TC-165-05, malformed JSON response is a parse error

- **Requirement:** REQ-165-03
- **Level:** L2

**Step:** Stub `ExecRunner.Run` to return `([]byte("not json"), nil)`. Call
`ValidateRead`.

**Expected output:** `err != nil`; `strings.Contains(err.Error(), "parse
validate_read response")`; the raw malformed bytes appear in the error string
(matching `ValidateWrite`'s existing `%q` convention at
`internal/memoryguard/memoryguard.go:131`).

---

### TC-165-06, leaf isolation unaffected (F-012)

- **Requirement:** REQ-165-04
- **Level:** L3 (fitness)

**Step:** `make fitness-memoryguard-isolation`

**Expected output:** `PASS fitness-memoryguard-isolation: ...` unchanged, zero
violations, the new method and wire types use only `encoding/json`, `errors`,
`fmt` (already imported).

---

### TC-165-07, full regression

- **Requirement:** REQ-165-05
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/memoryguard/... ./internal/orchestrator/...
make check
```

**Expected output:** all packages `ok`, `ValidateWrite`/`VerifyDelete`/`MemoryGuardStore`
suites pass byte-identical; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2, this is a stdlib-only leaf-package IPC method
  addition with no runtime-observable surface of its own (no live wiring into a
  caller happens in this task, task 172 does that). Unit tests against the
  `ExecRunner` seam are the correct and sufficient verification level, matching how
  `ValidateWrite`/`VerifyDelete` were originally verified in task 084.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/memoryguard/... -run TestValidateRead
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Wiring `ValidateRead` into `MemoryGuardStore[P].Get` or any orchestrator call site
  (task 172/173).
- Any change to `ValidateWrite` or `VerifyDelete`'s existing behavior.
- A real memory-guard binary integration test (`internal/memoryguard` has none
  today for `ValidateWrite`/`VerifyDelete` either; the `ExecRunner` seam is the
  established test boundary for this leaf).
