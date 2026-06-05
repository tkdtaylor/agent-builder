# Task 025: armor guard adapter

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** backlog

## Goal
Implement the external armor guard adapter behind the web-ingestion/tool-call
boundary so agent-builder can translate armor results into allow, block, or
quarantine decisions without editing armor source.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 armor necessary from the start; §3 armor lowers injection likelihood on the ingestion path)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: ADR 024: armor ingestion and tool-call boundary
- Dependencies: 024

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | armor is invoked as an external tool/service adapter behind the boundary guard seam. | must have |
| REQ-002 | armor allow/flag/error responses map to the boundary decision model deterministically. | must have |
| REQ-003 | Missing, timed-out, malformed, or non-zero armor invocation fails closed. | must have |
| REQ-004 | No armor source is modified or vendored into agent-builder. | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 024

## Acceptance criteria
- [ ] [REQ-001] A concrete adapter invokes armor through a process/service seam and implements the task 024 guard interface.
- [ ] [REQ-002] Benign armor output maps to `allow`; injection/exfil/tool-call findings map to `block` or `quarantine` with reason metadata.
- [ ] [REQ-003] Missing armor binary/service, timeout, malformed output, and non-zero exit map to fail-closed decisions.
- [ ] [REQ-004] The diff modifies only agent-builder-owned code/docs; armor source remains external.

## Verification plan
- **Highest level achievable:** L5 — adapter harness drives scripted armor-compatible outputs through the concrete adapter and asserts boundary decisions.
- L5 harness: `go test -count=1 ./tests/armor/... ./internal/armor/...`; expected final assertion — allow, flagged, malformed, unavailable, timeout, and non-zero cases map to the documented decisions.
- **Cross-module state risk:** adapter consumes task 024 candidate/decision types; producer-consumer trace required.
- **Runtime-visible surface:** external armor subprocess/service invocation result; if a real armor binary/service is available, quote the guarded fixture result. If not, record scripted-harness evidence only.

## Out of scope
- Building or modifying armor itself.
- Wiring live executor traffic through the adapter.
- Broadening the egress allowlist.

## Notes
- The adapter should keep the boundary decision model stable even if armor's concrete output format changes later.
