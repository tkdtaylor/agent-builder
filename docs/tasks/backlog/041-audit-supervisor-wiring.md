# Task 041: Wire audit.Sink into the supervisor's action events

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Project the supervisor's `command`-class lifecycle events through an `audit.Sink` (typed action layer), wire the `audit.BlockSink` adapter into `internal/runtime/run.go` behind an optional `AGENT_BUILDER_AUDIT_RECORD` path, and prove a real run produces a chain the shipped block's own `verify` reports valid — all without widening the F-003 supervisor isolation boundary.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/026-audit-trail-consume-shipped-block.md` (consume the shipped block via CLI; supersedes ADR 025). Decision 1 (beside, not replace — the action log sits alongside the unchanged 019 RunRecord raw stream) and the F-003 leaf-package note both carry forward; the chain is produced by the block via the `BlockSink` adapter (task 039), not an in-repo writer.
- The action events the supervisor emits today are the `command` lines in `internal/runtime/run.go` `RunInside`: `containment=podman launcher=…`, `pick task`, `attempt`, `verify`, `publish branch … remote=…`, `escalated` evidence, `finish … outcome=…`. This task emits a typed `audit.AuditEvent` for each alongside the existing `command` write.
- Wiring mirrors the optional RunRecord path: `supervisor.WithRunRecordPath` / `AGENT_BUILDER_RUN_RECORD` (see `internal/supervisor/supervisor.go` `openRunRecord`/`closeRunRecord` and `internal/runtime/run.go`). The new env vars are `AGENT_BUILDER_AUDIT_RECORD` (the chain logfile passed to the block) and `AGENT_BUILDER_AUDIT_BIN` (the `audit-trail` binary; falls back to `$PATH`).
- **Model tier: deep (opus)** — touches the trusted supervisor boundary; the isolation invariant and the close/seal-before-teardown ordering are load-bearing.
- Dependencies: 038, 039.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-041-01 | The supervisor projects each action-class lifecycle event (containment, pick, attempt, verify+verdict, publish, escalate, finish+outcome) through an `audit.Sink`; raw stdout/stderr stay in the unchanged 019 RunRecord, never the Sink | must have |
| REQ-041-02 | The Sink is `Seal`ed before containment teardown on both success and failure paths (mirrors the RunRecord close-before-teardown durability rule); the Sink is optional | must have |
| REQ-041-03 | `internal/runtime` wires `audit.BlockSink` behind an optional `AGENT_BUILDER_AUDIT_RECORD` path (blank/absent disables it, mirroring `AGENT_BUILDER_RUN_RECORD`); when set, the `audit-trail` binary must resolve (`AGENT_BUILDER_AUDIT_BIN` or `$PATH`) and an unresolvable binary or unwritable path fails with a config error before dispatch — auditing is never silently skipped | must have |
| REQ-041-04 | A real run through the default wiring produces a chain the block's `verify` (task 040's `VerifyChain`) reports `Valid == true`, with the action sequence matching the run | must have |
| REQ-041-05 | The wiring does not widen the F-003 boundary: `make fitness-supervisor-isolation` still passes; `internal/audit` adds no executor/LLM/web dep to the supervisor's transitive graph | must have |
| REQ-041-06 | `docs/spec/architecture.md`, `docs/architecture/diagrams.md`, and `docs/spec/configuration.md` (new `AGENT_BUILDER_AUDIT_RECORD` + `AGENT_BUILDER_AUDIT_BIN` env vars, and the `audit-trail` block as an external runtime dependency) are updated in the same commit | must have |

## Readiness gate

- [x] Test spec `041-audit-supervisor-wiring-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking tasks 038, 039 complete

## Acceptance criteria

- [ ] [REQ-041-01] A run with a `FakeSink` records typed action events in order (containment, pick, attempt, verify[verdict], publish, finish[outcome]); an escalated run records attempt(s)+escalate+finish[failed] and no publish; no event carries raw stdout/stderr
- [ ] [REQ-041-01] With both a RunRecord and a Sink attached, stdout/stderr/command stream lines remain in the RunRecord (task 019/028 assertions still pass) and the Sink holds only typed action events
- [ ] [REQ-041-02] `Seal()` is called before containment teardown on success and failure; `FakeSink.Sealed()` is true post-run; a run with no Sink behaves exactly as before
- [ ] [REQ-041-03] `AGENT_BUILDER_AUDIT_RECORD=<path>` produces the block's chain file at that path; blank/absent writes no file and the run still completes; an unresolvable `audit-trail` binary or unwritable path fails with a config error before dispatch
- [ ] [REQ-041-04] `VerifyChain` (the block's `verify`) reports `Valid == true` over the produced chain, and the chain's action sequence matches the run (pick→attempt→verify→publish→finish)
- [ ] [REQ-041-05] `make fitness-supervisor-isolation` passes after wiring; `go list -deps ./internal/supervisor/...` shows `internal/audit` but no executor/LLM/web package
- [ ] [REQ-041-06] architecture.md, diagrams.md, and configuration.md (new env var) updated in the feat commit

## Verification plan

- **Highest level achievable:** L5 — an e2e harness drives a real `agent-builder run` through the default wiring with `AGENT_BUILDER_AUDIT_RECORD` set (and a resolvable `audit-trail` binary), then runs `VerifyChain` (the block's `verify`) over the produced chain and asserts it is valid.
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./tests/e2e ./tests/supervisor -run 'TestAuditChain|TestSupervisorAuditProjection'
  ```
  Expected final assertion: the run produces a chain at `AGENT_BUILDER_AUDIT_RECORD`; the block's `verify` reports `Valid == true`; the chain's action sequence equals pick→attempt→verify→publish→finish for an accepted task. Final gate `make check` -> `All checks passed.` (includes `fitness-supervisor-isolation`). (CI-without-binary: the e2e gates the real-binary path behind an env/`make` opt-in and otherwise asserts the emitted `audit-trail emit` argv via a recorded-exec stub; the evidence states which ran.)
- **Level 6 — Operator observation (if applicable):**
  - Binary path: `AGENT_BUILDER_AUDIT_RECORD=/tmp/audit.log agent-builder run` (with the fake-launcher/test env + `audit-trail` on `$PATH`), then `audit-trail verify --logfile /tmp/audit.log`
  - Targeted behaviour to observe: the block reports `valid == true` over a chain ending in a `finish` event.
- **Cross-module state risk:** names the `audit.AuditEvent` action stream and the `AGENT_BUILDER_AUDIT_RECORD` file. Producer = supervisor/runtime wiring → `BlockSink` (039) → block `emit`; consumer = the block's `verify` via `VerifyChain` (040). The executor must produce a producer→consumer trace: run emits events → block persists the chain → block `verify` confirms.
- **Runtime-visible surface:** file output (the audit chain NDJSON) + a new CLI-configurable env var. The executor must run the binary/harness and quote chain lines and the verify result.

## Out of scope

- The `fitness-audit-isolation` (F-005) check — task 042.
- Unifying the RunRecord raw stream and the audit chain into one format — out of scope (the action layer goes to the block; raw stdout/stderr stays in the RunRecord).
- A standing `verify` gate over every run in `make check` — task 040 delivers the `VerifyChain` helper + block-severity semantics; this task verifies the produced chain via the e2e harness.
- **Egress-attempt audit events** — deferred and spike-gated per ADR 026 decision 2; v0 ships only the action events the run loop already emits.

## Notes

- Keep `internal/audit` a leaf — the supervisor depends on the `Sink` interface, and the `BlockSink` reaches the block over `os/exec`, so no block/executor/LLM/web package enters the supervisor's import graph (F-003 / F-005 hold).
- Seal-before-teardown is the durability invariant; reuse the exact ordering the RunRecord already follows in `closeRunRecord` (here `Seal()` flushes the last `emit` and any deferred error).
- Two durable artifacts per run (RunRecord raw stream + the block's action chain) is the accepted ADR 026 trade, not drift.
- Update architecture.md, diagrams.md, and configuration.md in the same commit. Do not edit spec during backlog authoring.
