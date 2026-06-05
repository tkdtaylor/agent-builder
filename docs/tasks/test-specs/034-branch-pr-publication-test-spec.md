# Test Spec 034: branch and PR publication

**Linked task:** [`docs/tasks/backlog/034-branch-pr-publication.md`](../backlog/034-branch-pr-publication.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004 | ✅ |

## Test cases
### TC-001: successful verified run publishes PR artifact
- **Requirement:** REQ-001
- **Input:** fake publisher seam with run outcome containing task ID, branch, passing Gate verdict, and target repo remote.
- **Expected output:** publisher receives one request, branch is pushed or recorded, and returned PR URL/ID is stored in run evidence.
- **Edge cases:** existing PR for the branch is reused or reported idempotently.

### TC-002: publisher is called only after Gate pass and branch capture
- **Requirement:** REQ-002
- **Input:** executor failure, blank branch, and Gate failure outcomes.
- **Expected output:** publisher call count remains zero for each non-success prerequisite.
- **Edge cases:** Gate pass with empty branch still blocks publication.

### TC-003: publication failure does not mark task done
- **Requirement:** REQ-003
- **Input:** fake publisher returning push failure, auth failure, or PR creation failure.
- **Expected output:** run outcome is non-success; task status is not `done`; error is written to run record.
- **Edge cases:** retry/escalation policy can see the publication failure as a terminal failure reason.

### TC-004: publication secrets are redacted
- **Requirement:** REQ-004
- **Input:** fake publisher emits stdout/stderr containing a fake token value.
- **Expected output:** CLI output, logs, and run record contain `[REDACTED]` or omit the token entirely.
- **Edge cases:** both `GIT_TOKEN` and provider-specific token names are covered.

### TC-005: optional real PR smoke records evidence
- **Requirement:** REQ-001
- **Input:** approved private fixture repo with real git credentials.
- **Expected output:** branch is pushed, PR URL is produced, and evidence is recorded without leaking secrets.
- **Edge cases:** unavailable credentials leave task at L5, not L6.

## Notes
Framework: Go `testing` with fake publisher seam. Real `gh`/git PR creation should only run in an approved environment.
