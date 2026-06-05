# Test Spec 029: Claude executor ingestion control

**Linked task:** [`docs/tasks/completed/029-claude-ingestion-control.md`](../completed/029-claude-ingestion-control.md)
**Written:** 2026-06-05
**Status:** ready

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|------------|----------|
| REQ-001 | TC-001, TC-004 | ✅ |
| REQ-002 | TC-002, TC-005 | ✅ |
| REQ-003 | TC-003 | ✅ |
| REQ-004 | TC-004, TC-005 | ✅ |

## Test cases
### TC-001: Claude executor declares reviewed-or-disabled web/tool policy
- **Requirement:** REQ-001
- **Input:** default Claude executor configuration and explicit reviewed/disabled configurations.
- **Expected output:** default policy is fail-closed; reviewed policy requires a configured harness; disabled policy rejects web/tool requests with a typed error.
- **Edge cases:** blank or unknown policy is rejected before subprocess start.

### TC-002: reviewed web content reaches Claude context only after broker release
- **Requirement:** REQ-002
- **Input:** fake Claude-facing web event plus armor runner returning `allow`.
- **Expected output:** content candidate is produced, armor consumes the matching candidate, broker releases it, and only then does the Claude-facing continuation receive content.
- **Edge cases:** empty content is still reviewed; unsupported source URI is rejected before armor.

### TC-003: direct bypass fails
- **Requirement:** REQ-003
- **Input:** fake executor path attempts to inject web content or execute a tool without `ContentRelease` or `ToolCallRelease`.
- **Expected output:** harness returns `ErrUnreviewedRelease` or equivalent blocking error; callback is not invoked.
- **Edge cases:** bypass detection covers both content and tool-call routes.

### TC-004: disabled policy is fail-closed and observable
- **Requirement:** REQ-001, REQ-004
- **Input:** Claude executor configured with disabled web/tool policy.
- **Expected output:** web/tool requests are denied before executor context or execution; reason names disabled capability; no armor call is required for disabled routes.
- **Edge cases:** normal code-editing subprocess invocation still works when it does not request web/tool events.

### TC-005: armor block, quarantine, findings, and unavailable cases prevent executor use
- **Requirement:** REQ-002, REQ-004
- **Input:** reviewed policy with armor responses `block`, `quarantine`, `allow` plus findings, runner error, missing command, timeout, and malformed JSON.
- **Expected output:** no blocked candidate reaches Claude context or tool execution; decision reason and metadata remain visible.
- **Edge cases:** candidate ID/kind mismatch from armor fails closed.

## Notes
Framework: Go `testing`. Use fake Claude-facing event producers and fake armor runners. Real Claude CLI is not required unless an approved environment is available.
