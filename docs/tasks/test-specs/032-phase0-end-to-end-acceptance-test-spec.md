# Test Spec 032: Phase 0 end-to-end acceptance

**Linked task:** [`docs/tasks/backlog/032-phase0-end-to-end-acceptance.md`](../backlog/032-phase0-end-to-end-acceptance.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-006 | ✅ |
| REQ-002 | TC-002, TC-006 | ✅ |
| REQ-003 | TC-003, TC-006 | ✅ |
| REQ-004 | TC-004 | ✅ |
| REQ-005 | TC-005 | ✅ |

## Test cases
### TC-001: runtime binary starts from fixture backlog task
- **Requirement:** REQ-001
- **Input:** built `agent-builder run` binary, fixture backlog task, fixture test spec, and target worktree.
- **Expected output:** CLI selects the fixture task through the real task source and starts the configured run pipeline.
- **Edge cases:** no ready task returns idle and does not call executor.

### TC-002: branch artifact plus passing Gate are required for success
- **Requirement:** REQ-002
- **Input:** fake executor that writes a branch and mutates the fixture worktree so the production Gate passes.
- **Expected output:** final outcome is success only when branch is non-empty and Gate verdict is OK.
- **Edge cases:** blank branch or failing Gate returns non-success.

### TC-003: run record persists full lifecycle
- **Requirement:** REQ-003
- **Input:** successful fixture run with run-record path configured.
- **Expected output:** NDJSON contains task ID, box/run ID, command, stdout/stderr, branch, PR artifact when configured, Gate summary, and terminal `completed` outcome after teardown.
- **Edge cases:** teardown errors are recorded and returned.

### TC-004: negative paths do not mark done
- **Requirement:** REQ-004
- **Input:** executor failure, Gate failure, timeout, and blocked ingestion fixtures.
- **Expected output:** task status is not changed to done; terminal outcome is failed, timed-out, or needs-human as appropriate.
- **Edge cases:** failure after partial output still closes the run record.

### TC-005: final documentation labels evidence honestly
- **Requirement:** REQ-005
- **Input:** coverage tracker, roadmap, and task 032 verification evidence.
- **Expected output:** docs distinguish fake-provider L5 from real-provider or real-containment L6.
- **Edge cases:** real Claude or real Podman unavailable must be named as pending, not implied as complete.

### TC-006: Phase 0 acceptance trace is printable
- **Requirement:** REQ-001, REQ-002, REQ-003
- **Input:** happy-path end-to-end harness.
- **Expected output:** test logs print `TC-001 Phase 0 accepted: task selected, branch produced, PR recorded, gate passed, run record persisted`.
- **Edge cases:** trace includes enough IDs to correlate task, branch, PR artifact, run record, and Gate verdict.

## Notes
Framework: Go `testing`, likely under `tests/e2e`. Use fake process shims unless an approved runtime environment is available for L6.
