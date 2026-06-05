# Task 029: Claude executor ingestion control

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** backlog

## Goal
Close the gap between the armor-guarded executor harness and the concrete Claude CLI executor so web-ingestion and tool-call routes are either broker-reviewed before use or explicitly disabled as unsupported.

## Context
- Tech stack: Go
- Roadmap: `docs/plans/roadmap.md` Phase 0.6
- Related ADRs: ADR 024
- Dependencies: 022, 024, 025, 026, 027
- Audit finding: `executor.ClaudeCLI` invokes `claude -p` as an opaque subprocess, while task 026 requires executor web/tool events to pass through the guarded boundary before executor use.

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Claude executor configuration exposes a concrete, testable policy for web-ingestion and tool calls: reviewed through `executorharness` or disabled fail-closed. | must have |
| REQ-002 | Any repo-owned executor-facing web/tool wrapper releases content or executes tools only through broker-reviewed `ContentRelease` and `ToolCallRelease` values. | must have |
| REQ-003 | Prompt text, CLI flags, or subprocess configuration alone are not accepted as the blocking control unless a test proves the bypass route is unavailable. | must have |
| REQ-004 | Armor-backed wiring from task 026 remains available for reviewed routes and fail-closed for unavailable armor. | must have |

## Readiness gate
- [x] Test spec `029-claude-ingestion-control-test-spec.md` exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria below have a linked REQ ID
- [x] Blocking tasks complete: 022, 024, 025, 026, and 027

## Acceptance criteria
- [ ] [REQ-001] The Claude executor has an explicit web/tool policy visible in code and spec: `reviewed` or `disabled`; the default is fail-closed.
- [ ] [REQ-002] A validation harness proves reviewed content/tool events pass through `executorharness.NewArmorGuarded` before continuation or execution.
- [ ] [REQ-003] A simulated direct web/tool bypass fails the harness instead of silently reaching executor context or tool execution.
- [ ] [REQ-004] Armor unavailable, armor block/quarantine, malformed tool arguments, and allow-with-findings all prevent executor use.

## Verification plan
- **Highest level achievable:** L5 - harness drives the concrete Claude executor adapter or its configured tool/web wrapper with fake Claude and fake armor processes.
- **Level 5 - Validation harness command:**
  ```
  go test -count=1 -v ./tests/executor ./tests/executorharness -run 'TestClaude.*Ingestion|TestArmorGuardedHarnessProducerConsumerTraceCoversLiveExecutorPath'
  ```
  Expected final assertion: `TC-005 Claude executor web/tool route is reviewed or disabled fail-closed`
- **Cross-module state risk:** Claude executor to executorharness to ingestion broker to armor guard; producer-consumer trace required.
- **Runtime-visible surface:** subprocess arguments/configuration and harness trace.

## Out of scope
- Building or modifying Claude Code CLI itself.
- Treating prompt instructions as a security boundary.
- Disk-read exfiltration controls; scanners and egress allowlists own that layer.

## Notes
- If Claude Code CLI cannot expose interceptable web/tool events, the acceptable Phase 0 answer is to disable those executor capabilities and document that all executor research/tool use must go through repo-owned wrappers.
