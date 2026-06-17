# Task 039: audit.BlockSink — emit to the shipped audit-trail block

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Implement the production `audit.Sink`: a `BlockSink` adapter that maps each typed `AuditEvent` onto one `audit-trail emit` call against the shipped `audit-trail` block (CLI subprocess in v0), so agent-builder's action layer lands in the block's tamper-evident chain **without reimplementing the chain**. The block owns the hash chain, canonical encoding, genesis, and verifier; this task owns only the mapping and the subprocess seam.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/026-audit-trail-consume-shipped-block.md` (Option A — consume via CLI; supersedes ADR 025). The block is `github.com/tkdtaylor/audit-trail` (`$HOME/Code/Public/audit-trail`), frozen v1 `emit`/`verify` contract (`docs/CONTRACT.md` in that repo).
- **This task replaces the former in-repo `ChainWriter`.** Per ADR 026, agent-builder does not own the on-disk format — re-implementing SHA-256 chaining + RFC 8785 canonicalization duplicates a shipped, fitness-covered block. The adapter shells out to it instead.
- Builds on task 038's `audit.AuditEvent` + `Sink` interface — `BlockSink` is the production implementation of that seam (the `FakeSink` stays the unit-test double).
- The block's `emit` schema (frozen): `event = { ts, actor, action, target, decision?, refs, context? }` → returns `{ seq, hash }`. CLI form: `audit-trail emit --logfile <path> --actor <id> --action <verb> --target <res> [--decision <d>] …`. The CLI resumes chain state from disk per invocation (`seq`/`prev_hash` reconstructed on open), so one subprocess per action event yields a single continuous chain.
- **Model tier: balanced (sonnet)** — the integrity-critical canonicalization now lives in the block; this task is a typed→CLI mapping with strict error handling, not a crypto implementation.
- Dependencies: 038.

## AuditEvent → block emit mapping (the load-bearing detail)

| `AuditEvent` field | block `emit` field | notes |
|---|---|---|
| action (enum) | `action` | the verb string (`containment`, `pick`, `attempt`, `verify`, `publish`, `escalate`, `finish`) |
| constant `"agent-builder"` (+ run identity) | `actor` | the emitting identity |
| task id / branch / remote / launcher | `target` | the resource the action touched |
| verdict (verify) / outcome (finish) | `decision` | only set when the event carries one |
| run id, task id, other typed sub-fields | `context` | integer/string values only (block convention; no floats) |
| event time (injectable clock) | `ts` | unix seconds |

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-039-01 | `BlockSink` implements `audit.Sink` (`Append`/`Seal`); construction takes the block binary path + logfile path (resolved from `AGENT_BUILDER_AUDIT_BIN`/`$PATH` and `AGENT_BUILDER_AUDIT_RECORD` by the caller, task 041) | must have |
| REQ-039-02 | `Append(event)` validates the event (reusing 038's validator), maps it to the block `emit` field set per the mapping above, and invokes `audit-trail emit` once; a non-zero block exit or unparseable `{seq,hash}` response is surfaced as a non-nil error, never swallowed | must have |
| REQ-039-03 | `BlockSink` reaches the block over a process boundary only (`os/exec`); it imports **no** `audit-trail` Go package and **no** executor/LLM/web package — `internal/audit` stays a leaf (keeps F-005 trivially green) | must have |
| REQ-039-04 | `Append` after `Seal` fails; a validation-failing event invokes no subprocess and does not advance the chain; `Seal` is idempotent and surfaces any deferred error | must have |
| REQ-039-05 | Block-unavailability is a hard, named error (binary not found / not executable / emit failed) — the adapter never degrades to silent no-op when a logfile path is configured | must have |
| REQ-039-06 | `docs/spec/data-model.md` documents the `AuditEvent`→block-`emit` mapping and points to the block's frozen contract for the on-disk chain format (agent-builder does not own that format); `docs/spec/architecture.md` records the `audit-trail` block as an external dependency reached via CLI | must have |

## Readiness gate

- [x] Test spec `039-audit-chain-writer-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking task 038 complete

## Acceptance criteria

- [ ] [REQ-039-01] `var _ audit.Sink = (*audit.BlockSink)(nil)` compiles; the adapter is constructed with an explicit binary + logfile path (no global state)
- [ ] [REQ-039-02] For each `AuditAction`, `Append` builds the correct `audit-trail emit` argument set (action/actor/target, decision only when present, context as int/string only) and treats a non-zero exit or malformed `{seq,hash}` as an error
- [ ] [REQ-039-03] `go list -deps ./internal/audit/...` shows no `audit-trail`/executor/LLM/web package; the adapter uses `os/exec` only
- [ ] [REQ-039-04] Post-`Seal` `Append` errors; a validation-failing event spawns no subprocess; a second `Seal` does not panic
- [ ] [REQ-039-05] A missing/non-executable block binary, or an `emit` that fails, yields a non-nil named error from `Append`/construction — never a silent skip
- [ ] [REQ-039-06] `docs/spec/data-model.md` (mapping + pointer to block contract) and `docs/spec/architecture.md` (external block dependency) updated in the feat commit

## Verification plan

- **Highest level achievable:** L5 — a harness drives `BlockSink` against a **real `audit-trail` binary** over a temp logfile, emits the seven action events, then runs `audit-trail verify --logfile <path>` and asserts `valid == true` with the expected `seq` count. (If the block binary is not installed in CI, the harness uses a recorded-exec stub that asserts the exact argv per event, and the real-binary path is gated behind a `make`/env opt-in — the L5 evidence states which was run.)
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./internal/audit/... -run 'TestBlockSink|TestBlockSinkEmitArgs|TestBlockSinkChainVerifies'
  ```
  Expected final assertion: seven `emit` calls produce a chain the block's own `verify` reports `valid == true`; the argv for each event matches the mapping table; a failing `emit` surfaces an error.
- **Level 6 — Operator observation (if applicable):** optional — `AGENT_BUILDER_AUDIT_RECORD=/tmp/a.log` drive a few events, then `audit-trail verify --logfile /tmp/a.log` and observe `valid`. Not required for ✅ given L5.
- **Cross-module state risk:** the contract is now **cross-repo** — `BlockSink` depends on the block's frozen `emit` CLI surface. A drift in the block's CLI flags or `{seq,hash}` response shape breaks the adapter; the L5 real-binary path is what catches it. Producer = supervisor wiring (041); consumer of the chain = the block's own `verify` (wired in 040).
- **Runtime-visible surface:** subprocess invocations of `audit-trail emit` and the chain file the block writes. The executor must run the harness and quote both the emitted argv and the block's `verify` result.

## Out of scope

- Re-implementing the hash chain, canonical encoding, genesis sentinel, or verifier — **owned by the block** (this is the duplication ADR 026 removes).
- The IPC-socket transport / `audit-trail serve` sidecar — deferred upgrade per ADR 026 Option B; the adapter is shaped so CLI→socket is an internal swap.
- Importing the block as a Go module — rejected per ADR 026 Option C (coupling).
- Supervisor wiring (task 041), the integrity-gate wiring of `verify` (task 040), the fitness check (task 042).
- **Egress-attempt audit events** — deferred and spike-gated per ADR 026 decision 2.

## Notes

- The whole point of ADR 026 is that this task is *small*: a typed event → CLI argv mapping plus rigorous "block unavailable = loud error" handling. The hard crypto is the block's, already frozen and tested.
- Keep `internal/audit` a leaf: `os/exec` only, no `audit-trail` import, no executor/LLM/web import — task 042's F-005 depends on it.
- Update `docs/spec/data-model.md` (mapping + pointer to the block contract, not a re-spec of the chain format) and `docs/spec/architecture.md` (external block dependency) in the same commit. Do not edit spec during backlog authoring.
