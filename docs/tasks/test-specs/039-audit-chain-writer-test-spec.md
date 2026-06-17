# Test Spec 039: audit.BlockSink — emit to the shipped audit-trail block

**Linked task:** [`docs/tasks/backlog/039-audit-chain-writer.md`](../backlog/039-audit-chain-writer.md)
**Written:** 2026-06-16
**Status:** ready

> Repurposed under ADR 026 (supersedes ADR 025): agent-builder consumes the shipped
> `audit-trail` block instead of reimplementing a chain. This spec covers the
> `BlockSink` adapter (typed `AuditEvent` → `audit-trail emit` CLI), **not** an in-repo
> hash chain. The block owns the chain, canonicalization, genesis, and verifier.

## Requirements coverage

| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-039-01 | TC-039-01 | ⏳ |
| REQ-039-02 | TC-039-02, TC-039-03 | ⏳ |
| REQ-039-03 | TC-039-04 | ⏳ |
| REQ-039-04 | TC-039-05 | ⏳ |
| REQ-039-05 | TC-039-06 | ⏳ |
| REQ-039-06 | TC-039-07 | ⏳ |

## Pre-implementation checklist

- [x] All test cases below are defined
- [x] Expected inputs and outputs are specified for each case
- [x] Edge cases and error paths are covered
- [x] Every REQ-ID from the task has at least one test case
- [x] Success criteria are unambiguous

## Test cases

### TC-039-01: BlockSink satisfies audit.Sink at compile time

- **Requirement:** REQ-039-01
- **Input:** a `*audit.BlockSink` value assigned to a `var _ audit.Sink` blank identifier; construction takes an explicit block binary path + logfile path.
- **Expected output:** compiles without a type assertion; `BlockSink` implements `Append(AuditEvent) error` and `Seal() error`. No global/package state — the binary and logfile are passed in.
- **Edge cases:** the exec boundary is injectable (a `runner func(args []string) (stdout []byte, err error)` seam or equivalent) so tests can record argv without a real subprocess.

### TC-039-02: each AuditAction maps to the correct `audit-trail emit` argv

- **Requirement:** REQ-039-02
- **Input:** one `AuditEvent` per action (`containment`, `pick`, `attempt`, `verify`+verdict, `publish`+remote, `escalate`, `finish`+outcome) appended through a `BlockSink` whose exec seam records argv.
- **Expected output:** each `Append` invokes the block once with `emit --logfile <path> --actor agent-builder --action <verb> --target <res>`, plus `--decision <d>` only for `verify` (verdict) and `finish` (outcome), and `context` carrying run id / task id as integer/string values only. The argv for each action matches the ADR 026 mapping table exactly.
- **Edge cases:** an event with no decision (e.g. `pick`) emits no `--decision` flag; a `context` value must never be a float (block convention — no floats).

### TC-039-03: a non-zero block exit or malformed response is surfaced as an error

- **Requirement:** REQ-039-02
- **Input:** a `BlockSink` whose exec seam returns (a) a non-zero exit, then (b) a zero exit with an unparseable (non-`{seq,hash}`) stdout.
- **Expected output:** both cases make `Append` return a non-nil error that names the failing emit; the error is never swallowed and the chain is not silently advanced.
- **Edge cases:** a valid `{seq,hash}` JSON response is parsed and accepted; the returned `seq` increments across successive appends (the block resumes chain state from disk).

### TC-039-04: BlockSink reaches the block over os/exec only — internal/audit stays a leaf

- **Requirement:** REQ-039-03
- **Input:** `go list -deps ./internal/audit/...`.
- **Expected output:** the dependency list contains no `audit-trail` block package, no executor/LLM/web package; the adapter uses `os/exec` (and optionally `encoding/json` to parse the response) only. (Enforced as a blocking gate by F-005 in task 042.)
- **Edge cases:** importing the block as a Go module (ADR 026 Option C, rejected) would fail this — the boundary is a subprocess, not an import.

### TC-039-05: Append after Seal fails; validation-failing event spawns no subprocess; Seal idempotent

- **Requirement:** REQ-039-04
- **Input:** a `BlockSink`; `Append` (valid) → `Seal` → `Append`; separately, an `Append` whose `AuditEvent` fails task-038 validation.
- **Expected output:** the post-Seal `Append` returns a non-nil error (closed); a validation-failing event returns the validation error and invokes **no** subprocess (the exec seam records zero calls for it); a second `Seal` does not panic.
- **Edge cases:** `Seal` surfaces any deferred error from the last emit (e.g. a flush/wait error).

### TC-039-06: block unavailability is a hard, named error — never a silent no-op

- **Requirement:** REQ-039-05
- **Input:** construction/append with (a) a binary path that does not exist, (b) a path that exists but is not executable.
- **Expected output:** a non-nil named error (binary not found / not executable) at construction or first `Append`; the adapter never degrades to a silent no-op when a logfile path is configured.
- **Edge cases:** this is the agent-builder-side guarantee behind ADR 026's "auditing is never silently skipped"; the run-level fail-before-dispatch is asserted in task 041.

### TC-039-07 (L5): a real block run produces a chain the block's own verify accepts

- **Requirement:** REQ-039-02 (integration)
- **Input:** a `BlockSink` over a **real `audit-trail` binary** and a temp logfile; the seven action events are emitted in run order; then `audit-trail verify --logfile <path>` is invoked.
- **Expected output:** the block reports `valid == true` with seven records in `seq` order. (CI-without-binary fallback: the recorded-exec seam asserts the per-event argv per TC-039-02; the real-binary path is gated behind an env/`make` opt-in and the L5 evidence states which ran.)
- **Edge cases:** re-running emits over the same logfile continues the chain (the block reconstructs `seq`/`prev_hash` on open), so two runs append rather than fork.

## Post-implementation verification

- [ ] All test cases above pass
- [ ] No regressions in existing tests
- [ ] L5 harness (BlockSink → real `audit-trail emit` → block `verify` == valid) passes, or records the recorded-exec fallback and the opt-in real-binary command

## Test framework notes

Framework: Go `testing` with an injectable exec seam (record argv + return canned stdout/exit) for unit tests, and an opt-in real-binary path for L5. The integrity-critical crypto (SHA-256 chaining, RFC 8785 canonicalization, genesis) lives in the **block**, already frozen and fitness-tested — this task asserts only the typed-event→argv mapping, strict error handling, and the leaf-package (os/exec, no block import) discipline. The verifier that *detects tampering* is the block's, surfaced by task 040.
