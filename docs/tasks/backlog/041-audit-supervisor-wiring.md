# Task 041: Wire audit.Sink into the supervisor's action events

**Project:** agent-builder
**Created:** 2026-06-16
**Status:** backlog

## Goal

Project the supervisor's `command`-class lifecycle events through an `audit.Sink` (typed action layer), wire `audit.ChainWriter` into `internal/runtime/run.go` behind an optional `AGENT_BUILDER_AUDIT_RECORD` path, and prove a real run produces a valid, verifiable audit chain — all without widening the F-003 supervisor isolation boundary.

## Context

- Tech stack: Go
- Governing ADR: `docs/architecture/decisions/025-audit-trail-v0-hash-chained-reader.md` decision 1 (beside, not replace — the chained action log sits alongside the unchanged 019 RunRecord raw stream) and the F-003 note in Consequences (`internal/audit` is a leaf; a fitness check must assert it — that check is task 042).
- The action events the supervisor emits today are the `command` lines in `internal/runtime/run.go` `RunInside`: `containment=podman launcher=…`, `pick task`, `attempt`, `verify`, `publish branch … remote=…`, `escalated` evidence, `finish … outcome=…`. This task emits a typed `audit.AuditEvent` for each alongside the existing `command` write.
- Wiring mirrors the optional RunRecord path: `supervisor.WithRunRecordPath` / `AGENT_BUILDER_RUN_RECORD` (see `internal/supervisor/supervisor.go` `openRunRecord`/`closeRunRecord` and `internal/runtime/run.go`). The new env var is `AGENT_BUILDER_AUDIT_RECORD`.
- **Model tier: deep (opus)** — touches the trusted supervisor boundary; the isolation invariant and the close/seal-before-teardown ordering are load-bearing.
- Dependencies: 038, 039.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-041-01 | The supervisor projects each action-class lifecycle event (containment, pick, attempt, verify+verdict, publish, escalate, finish+outcome) through an `audit.Sink`; raw stdout/stderr stay in the unchanged 019 RunRecord, never the Sink | must have |
| REQ-041-02 | The Sink is `Seal`ed before containment teardown on both success and failure paths (mirrors the RunRecord close-before-teardown durability rule); the Sink is optional | must have |
| REQ-041-03 | `internal/runtime` wires `audit.ChainWriter` behind an optional `AGENT_BUILDER_AUDIT_RECORD` path (blank/absent disables it, mirroring `AGENT_BUILDER_RUN_RECORD`); an unwritable path fails before dispatch | must have |
| REQ-041-04 | A real run through the default wiring produces a chain file that `audit.Verify` (task 040) reports `OK == true`, with the action sequence matching the run | must have |
| REQ-041-05 | The wiring does not widen the F-003 boundary: `make fitness-supervisor-isolation` still passes; `internal/audit` adds no executor/LLM/web dep to the supervisor's transitive graph | must have |
| REQ-041-06 | `docs/spec/architecture.md`, `docs/architecture/diagrams.md`, and `docs/spec/configuration.md` (new `AGENT_BUILDER_AUDIT_RECORD` env var) are updated in the same commit | must have |

## Readiness gate

- [x] Test spec `041-audit-supervisor-wiring-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [ ] Blocking tasks 038, 039 complete

## Acceptance criteria

- [ ] [REQ-041-01] A run with a `FakeSink` records typed action events in order (containment, pick, attempt, verify[verdict], publish, finish[outcome]); an escalated run records attempt(s)+escalate+finish[failed] and no publish; no event carries raw stdout/stderr
- [ ] [REQ-041-01] With both a RunRecord and a Sink attached, stdout/stderr/command stream lines remain in the RunRecord (task 019/028 assertions still pass) and the Sink holds only typed action events
- [ ] [REQ-041-02] `Seal()` is called before containment teardown on success and failure; `FakeSink.Sealed()` is true post-run; a run with no Sink behaves exactly as before
- [ ] [REQ-041-03] `AGENT_BUILDER_AUDIT_RECORD=<path>` produces an NDJSON chain file with `prev_hash`/`hash`; blank/absent writes no file and the run still completes; an unwritable path fails with a config error before dispatch
- [ ] [REQ-041-04] `audit.Verify` reports `OK == true` over the produced chain, and the chain's action sequence matches the run (pick→attempt→verify→publish→finish)
- [ ] [REQ-041-05] `make fitness-supervisor-isolation` passes after wiring; `go list -deps ./internal/supervisor/...` shows `internal/audit` but no executor/LLM/web package
- [ ] [REQ-041-06] architecture.md, diagrams.md, and configuration.md (new env var) updated in the feat commit

## Verification plan

- **Highest level achievable:** L5 — an e2e harness drives a real `agent-builder run` through the default wiring with `AGENT_BUILDER_AUDIT_RECORD` set, then runs `audit.Verify` over the produced chain and asserts it is valid.
- **Level 5 — Validation harness command (if applicable):**
  ```
  go test -count=1 -v ./tests/e2e ./tests/supervisor -run 'TestAuditChain|TestSupervisorAuditProjection'
  ```
  Expected final assertion: the run produces a chain at `AGENT_BUILDER_AUDIT_RECORD`; `audit.Verify` reports `OK == true`; the chain's action sequence equals pick→attempt→verify→publish→finish for an accepted task. Final gate `make check` -> `All checks passed.` (includes `fitness-supervisor-isolation`).
- **Level 6 — Operator observation (if applicable):**
  - Binary path: `AGENT_BUILDER_AUDIT_RECORD=/tmp/audit.ndjson agent-builder run` (with the fake-launcher/test env), then `cat /tmp/audit.ndjson`
  - Targeted behaviour to observe: an NDJSON action chain whose `prev_hash` of each line equals the prior line's `hash`, ending in a `finish` event.
- **Cross-module state risk:** names the `audit.AuditEvent` action stream and the `AGENT_BUILDER_AUDIT_RECORD` file. Producer = supervisor/runtime wiring; consumers = `audit.ChainWriter` (039) on write and `audit.Verify` (040) on read. The executor must produce a producer→consumer trace: run emits events → ChainWriter persists → Verify confirms.
- **Runtime-visible surface:** file output (the audit chain NDJSON) + a new CLI-configurable env var. The executor must run the binary/harness and quote chain lines and the verify result.

## Out of scope

- The `fitness-audit-isolation` (F-005) check — task 042.
- Unifying the RunRecord raw stream and the audit chain into one format (Option C, rejected in ADR 025).
- A `verify` CLI subcommand surfacing `audit.Verify` to operators — can follow; this task wires the writer and verifies via harness.
- **Egress-attempt audit events** — deferred and spike-gated per ADR 025 decision 2; v0 ships only the action events the run loop already emits.

## Notes

- Keep `internal/audit` a leaf — the whole point of the seam is that the supervisor depends on the interface, not on a backend, so isolation holds.
- Seal-before-teardown is the durability invariant; reuse the exact ordering the RunRecord already follows in `closeRunRecord`.
- Two durable artifacts per run (RunRecord raw stream + audit chain) is the accepted ADR 025 trade, not drift.
- Update architecture.md, diagrams.md, and configuration.md in the same commit. Do not edit spec during backlog authoring.
