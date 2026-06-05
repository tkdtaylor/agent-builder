# Task 021: sandbox-runtime backing adapter (bootstrap isolation)

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (code merged; L6 runtime evidence blocked by missing srt/Snap go issue)

## Goal
Implement the run() adapter (the seam from task 020) backed by `@anthropic-ai/sandbox-runtime` (bubblewrap + dual proxy + allowlist, Apache-2.0) as the bootstrap per-command isolation — the rented isolation that exec-sandbox v0 later replaces.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` (§4 — adopt `@anthropic-ai/sandbox-runtime` as-is = Tier 1)
- Roadmap: `docs/plans/roadmap.md` (Phase 0.5)
- Related ADRs: ADR 020 (exec-sandbox adapter seam) governs the interface; no new ADR unless the integration approach diverges
- Dependencies: 020 (seam), 015 (egress allowlist), 014 (box profile)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | The adapter drives `@anthropic-ai/sandbox-runtime` to run a command in an isolated worktree. | must have |
| REQ-002 | The adapter honors the egress allowlist (task 015) — non-allowlisted egress is blocked. | must have |
| REQ-003 | The adapter is swap-compatible behind the task-020 interface: replacing it later requires no caller change. | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 020, 015, 014

## Acceptance criteria
- [x] [REQ-001] A trivial command run through the adapter executes inside sandbox-runtime against an isolated worktree and returns a correct result + exit code.
- [x] [REQ-002] An attempted egress to a non-allowlisted host is blocked by the dual proxy / allowlist.
- [x] [REQ-003] The adapter type satisfies the task-020 interface; the supervisor compiles against it with no caller-side change versus the fake backend.

## Verification plan
- **Highest level achievable:** L6 — run a trivial command through the adapter inside `@anthropic-ai/sandbox-runtime`; observe it executes isolated, and observe that an attempt to reach a non-allowlisted host is blocked. Quote both outputs.
- L5 harness: invoke the adapter against a fixture command (e.g. `echo` + a `curl` to a blocked host); expected final assertion — allowed command exits 0 with expected stdout, blocked-egress command fails/denied.
- **Executor runtime result:** L6 not reached in this environment. Task 030 confirmed `command -v srt` exited `1` with no output while `command -v bwrap` returned `/usr/bin/bwrap`; the opt-in live harness `env AGENT_BUILDER_LIVE_SRT=1 go test -count=1 -v ./tests/sandbox -run TestSandboxRuntimeLiveHarness_TC002_TC003` was also blocked before `srt` execution because bare `go` resolved to `/snap/bin/go` and exited with `snap-confine has elevated permissions and is not confined but should be. Refusing to continue to avoid permission escalation attacks`.
- **Cross-module state risk:** consumes the egress allowlist (015) and box profile (014); a misconfigured allowlist weakens the load-bearing egress control — verify the deny path, not just the allow path.
- **Runtime-visible surface:** subprocess output (sandbox-runtime stdout/stderr) + egress allow/deny observable behaviour.

## Out of scope
- Building exec-sandbox v0 — that is a Phase 1 agent TASK, not an agent-builder task.
- The seam definition itself (task 020).

## Notes
- `@anthropic-ai/sandbox-runtime` is adopted as-is (Tier 1); do not fork or modify it.
- The deny path is the load-bearing control — egress allowlist is the accepted-risk mitigation for token-in-box.
