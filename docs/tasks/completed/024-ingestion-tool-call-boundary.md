# Task 024: web-ingestion and tool-call boundary

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** completed

## Goal
Define the repo-owned boundary for attacker-reachable web-ingested content and
executor tool-call requests so security guards can block or quarantine candidates
before they reach the executor path.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 armor necessary from the start; §3 armor lowers injection likelihood on the ingestion path)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: ADR 024: armor ingestion and tool-call boundary
- Dependencies: 012 (agent loop), 022 (Claude CLI executor exposes the current executor limitation this seam must address)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A typed boundary represents web-ingested content before it can enter executor context. | must have |
| REQ-002 | A typed boundary represents tool-call requests before execution. | must have |
| REQ-003 | A guard seam can allow, block, or quarantine each candidate and fails closed on errors, timeout, or unavailable guard. | must have |
| REQ-004 | The boundary lives inside the in-box agent/executor side; the trusted supervisor gains no web, LLM, executor-tooling, or armor imports. | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 012 and 022

## Acceptance criteria
- [ ] [REQ-001] Web content is represented as a typed candidate carrying content, source/provenance, media type, and a stable correlation ID.
- [ ] [REQ-002] Tool-call requests are represented as typed candidates carrying tool name, arguments, target/provenance when applicable, and a stable correlation ID.
- [ ] [REQ-003] A fakeable guard/broker seam returns allow/block/quarantine decisions and treats guard error, timeout, unavailable guard, and malformed result as block/quarantine.
- [ ] [REQ-004] F-003 remains green; no supervisor import path contains web-fetch, LLM, executor-tooling, or armor packages.

## Verification plan
- **Highest level achievable:** L5 — package-level harness drives benign, flagged, guard-error, timeout, and malformed-result fixtures through the boundary with a fake guard.
- L5 harness: `go test -count=1 ./tests/ingestion/... ./internal/ingestion/...`; expected final assertion — benign candidates are released, flagged/unavailable/timeout/malformed candidates are blocked or quarantined, and TC markers cover every requirement.
- **Cross-module state risk:** new candidate and decision types consumed by the future armor adapter and executor wiring; producer-consumer trace required before implementation commit.
- **Runtime-visible surface:** none in this seam task unless it adds logs or run-record output.

## Out of scope
- Invoking the real armor tool/service.
- Fetching web content.
- Executing tool calls.
- Wiring the current Claude CLI executor's live tool loop through the boundary.
- Building audit-trail storage for quarantined content.

## Notes
- This task is the prerequisite split from the original armor-wiring task.
- If the executor can still perform direct web research or tool calls outside this boundary, the final armor wiring remains incomplete.
