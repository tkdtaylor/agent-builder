# Task 026: armor on the web-ingestion / tool-call path

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L5)

## Goal
Wire the armor guard adapter onto the repo-owned web-ingestion and tool-call path so executor research content and tool-call requests are blocked or quarantined before they reach executor context or execution.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 armor necessary from the start; §3 armor lowers injection likelihood on the ingestion path — it does NOT cover the disk-read exfil path, which is scanners + allowlist)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: ADR 024: armor ingestion and tool-call boundary
- Dependencies: 024, 025, 027

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | executor web-ingested content passes through the guarded boundary before entering executor context. | must have |
| REQ-002 | A flagged injection is blocked / quarantined, not silently passed through. | must have |
| REQ-003 | executor tool-call requests pass through the guarded boundary before execution. | must have |
| REQ-004 | armor remains an invoked external tool/service; no armor source is modified by this task. | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 024, 025, and 027

## Acceptance criteria
- [x] [REQ-001] Web-ingested content passes through the armor-backed boundary as a blocking step before reaching executor context.
- [x] [REQ-002] Content flagged as injection is blocked or quarantined; it does not reach the loop silently.
- [x] [REQ-003] Tool-call requests pass through the armor-backed boundary as a blocking step before execution.
- [x] [REQ-004] armor is invoked as an external tool/service seam; no armor source is modified by this task.

## Verification plan
- **Highest level achievable:** L6 — drive the live executor research/tool-call path with known benign and injection fixtures; observe that armor allows benign traffic and blocks/quarantines flagged content/tool calls before executor use. Quote the result.
- L5 harness: executor-facing ingestion/tool-call path driven with (a) benign fixtures, (b) a known prompt-injection fixture, (c) an unsafe tool-call fixture, and (d) armor-unavailable fixture; expected final assertion — benign passes, flagged/unavailable paths are blocked or quarantined.
- **Cross-module state risk:** executor/broker/armor decision handoff is cross-module; producer-consumer trace required to prove the live executor path produces candidates before the broker/guard consumes them.
- **Runtime-visible surface:** guarded allow/block/quarantine decision and any logged/quarantined result.

## Out of scope
- Building or modifying armor itself — it is an existing tool, invoked only.
- Defining the boundary itself (task 024).
- Implementing the armor guard adapter (task 025).
- The egress allowlist (task 015) and the disk-read exfil path (scanners + allowlist), which armor does not cover.

## Notes
- Unattended operation is exactly where injection has teeth — this guard is necessary from the start, not a later hardening step.
- armor lowers injection *likelihood* on the ingestion path; it is one layer, not the whole defense.
- Direct executor web/tool use that bypasses the task 024 boundary is a blocker for this task.

## Historical blocker resolved 2026-06-05
The original blocker was that `executor.ClaudeCLI` invoked `claude -p` as an
opaque subprocess, so agent-builder could not prove web-ingestion/tool-call
events passed through the task 024 broker before executor use. Task 027 added
the executor-facing harness, and Task 031 recorded spec-verifier APPROVE for
the armor-backed wiring using the L5 producer-consumer trace harness.
