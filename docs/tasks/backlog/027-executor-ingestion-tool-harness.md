# Task 027: executor ingestion/tool-call harness

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** backlog

## Goal
Add an executor-facing harness or CLI/tool wrapper that exposes web-ingestion
and tool-call events as task 024 ingestion candidates before executor context or
tool execution can use them.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 armor necessary from the start; §3 armor lowers injection likelihood on the ingestion path)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: ADR 024: armor ingestion and tool-call boundary
- Dependencies: 022, 024
- Unblocks: 026

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Executor-facing web-ingestion events are represented as `ingestion.ContentCandidate` values before they can enter executor context. | must have |
| REQ-002 | Executor-facing tool-call requests are represented as `ingestion.ToolCallCandidate` values before execution. | must have |
| REQ-003 | The harness routes content and tool-call candidates through `ingestion.Broker` and releases only allowed candidates. | must have |
| REQ-004 | Direct executor web/tool routes that bypass the broker are disabled or detected by tests/harness evidence as a blocking failure. | must have |
| REQ-005 | The trusted supervisor still has no web, LLM, executor-tooling, armor, or ingestion imports. | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 022 and 024

## Acceptance criteria
- [ ] [REQ-001] A web-ingestion fixture produced by the executor-facing path becomes an `ingestion.ContentCandidate` with source URI, media type, content bytes, retrieval metadata, provenance, and correlation ID before executor release.
- [ ] [REQ-002] A tool-call fixture produced by the executor-facing path becomes an `ingestion.ToolCallCandidate` with tool name, JSON arguments, target/provenance when applicable, and correlation ID before execution.
- [ ] [REQ-003] A fake guard returning `allow`, `block`, `quarantine`, error, or timeout controls whether the harness releases content/tool calls, and fail-closed decisions do not reach executor context or execution.
- [ ] [REQ-004] The validation harness can prove that direct web/tool bypass of the broker fails the task instead of silently succeeding.
- [ ] [REQ-005] `make fitness-supervisor-isolation` remains green.

## Verification plan
- **Highest level achievable:** L5 - drive the executor-facing harness with benign web content, blocked/quarantined content, a safe tool call, a blocked tool call, guard-unavailable behavior, and a bypass attempt. Expected final assertion: only broker-released candidates reach the executor-facing continuation/execution point.
- L4 gate: `make check` and `make fitness`.
- L5 harness: targeted Go test or command that exercises the live executor-facing candidate production path with fake guard decisions and prints/asserts the producer-consumer trace.
- **Cross-module state risk:** executor/harness to ingestion broker decision handoff is cross-module; producer-consumer trace required.
- **Runtime-visible surface:** harness decision output/logging if this task adds a runnable harness command; otherwise unit/harness assertions are sufficient and no CLI/runtime surface changes.

## Out of scope
- Wiring the real armor adapter onto the harness. That remains task 026.
- Building or modifying armor itself.
- Changing the trusted supervisor to observe web content or tool-call requests.
- Relying on prompt instructions as the blocking control.
- The disk-read exfil path; scanners and the egress allowlist cover that layer.

## Notes
- The current `executor.ClaudeCLI` invokes `claude -p` as an opaque subprocess.
  This task must either constrain that executor so web/tool events go through a
  repo-owned harness or add a new executor-facing harness that task 026 can wire
  to armor.
- A unit test that manually constructs candidates is not enough. The required
  evidence is a live producer-consumer path: executor-facing event producer
  creates a candidate, broker consumes it, and only an allowed review releases
  data to the continuation/execution point.
