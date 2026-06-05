# Task 024: armor on the web-ingestion / tool-call path

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Wire armor (the existing LLM-guard block) onto the web-ingestion + tool-call path so that when the executor does web research (attacker-reachable content), injection/exfil/tool-call validation runs as a blocking guard before that content reaches the executor's loop.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§2 armor necessary from the start; §3 armor lowers injection likelihood on the ingestion path — it does NOT cover the disk-read exfil path, which is scanners + allowlist)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.6)
- Related ADRs: <none yet> — record one if the armor invocation boundary is non-obvious
- Dependencies: 012 (agent loop — ingestion happens within it)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | armor sits on the ingestion path as a blocking check before ingested content reaches the executor's loop. | must have |
| REQ-002 | A flagged injection is blocked / quarantined, not silently passed through. | must have |
| REQ-003 | armor is a wired seam (invoked as an existing tool), never edited as a target. | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 012

## Acceptance criteria
- [ ] [REQ-001] Web-ingested content passes through armor as a blocking step before reaching the executor's loop.
- [ ] [REQ-002] Content flagged as injection is blocked or quarantined; it does not reach the loop silently.
- [ ] [REQ-003] armor is invoked as an external tool/service seam; no armor source is modified by this task.

## Verification plan
- **Highest level achievable:** L6 — feed a known prompt-injection fixture through the ingestion path; observe that armor flags/blocks it and the content does not reach the loop. Quote the result.
- L5 harness: ingestion path driven with (a) a benign fixture and (b) a known-injection fixture; expected final assertion — benign passes, injection is blocked/quarantined.
- **Cross-module state risk:** armor is an external invoked dependency; a misconfigured/absent armor must fail closed (block ingestion), not fail open. Verify the fail-closed behaviour.
- **Runtime-visible surface:** ingestion path block/allow decision (and its logged/quarantined result).

## Out of scope
- Building or modifying armor itself — it is an existing tool, invoked only.
- The egress allowlist (task 015) and the disk-read exfil path (scanners + allowlist), which armor does not cover.

## Notes
- Unattended operation is exactly where injection has teeth — this guard is necessary from the start, not a later hardening step.
- armor lowers injection *likelihood* on the ingestion path; it is one layer, not the whole defense.
