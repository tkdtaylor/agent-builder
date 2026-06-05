# Test Spec 028: default run wiring

**Linked task:** [`docs/tasks/backlog/028-default-run-wiring.md`](../backlog/028-default-run-wiring.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002, TC-005 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004 | ✅ |

## Test cases
### TC-001: configured run path constructs every required seam
- **Requirement:** REQ-001
- **Input:** built `agent-builder run` binary with fixture task root, fixture target worktree, fake Claude CLI, fake sandbox/runtime adapter, fake scanner tools, timeout, and run-record path.
- **Expected output:** the run command does not return nil-seam supervisor errors; logs show box creation and loop start; the executor and Gate are invoked once.
- **Edge cases:** relative paths are cleaned; missing optional run-record path disables record writing without disabling dispatch.

### TC-002: one invocation dispatches at most one ready task
- **Requirement:** REQ-002
- **Input:** fixture task directory with two ready tasks.
- **Expected output:** exactly the lowest-ID ready task is attempted; the second task is untouched; run record names only the attempted task.
- **Edge cases:** no ready task returns an idle/non-success-neutral outcome without executor or Gate invocation.

### TC-003: supervisor isolation remains intact
- **Requirement:** REQ-003
- **Input:** `make fitness-supervisor-isolation`.
- **Expected output:** supervisor import graph contains no executor, ingestion, armor, web, or LLM dependency.
- **Edge cases:** CLI/bootstrap wiring may import concrete adapters; `internal/supervisor` may not.

### TC-004: missing configuration fails before executor attempt
- **Requirement:** REQ-004
- **Input:** run command fixtures missing task root, worktree, executor token, fake runtime, or scanner tools one at a time.
- **Expected output:** command exits non-zero, names the missing setting/tool, and records no executor attempt or task status mutation.
- **Edge cases:** missing external scanner is reported by Gate only after executor success, not before task selection.

### TC-005: runtime run record proves pick-attempt-verify-finish
- **Requirement:** REQ-001, REQ-002
- **Input:** happy-path fixture run with fake executor branch output and passing Gate.
- **Expected output:** run-record NDJSON contains task ID, command stream, executor attempt evidence, Gate pass summary, branch name, and terminal `completed` outcome.
- **Edge cases:** loop failure records `failed`; timeout records `timed-out`.

## Notes
Framework: Go `testing` with runtime binary invocation and fake process shims for external tools. The test must exercise the public CLI path, not only package-level dependency injection.
