# Test Spec 015: Default-deny egress allowlist

**Linked task:** [`docs/tasks/backlog/015-egress-allowlist.md`](../backlog/015-egress-allowlist.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-003 | ❌ |
| REQ-002 | TC-002 | ❌ |
| REQ-003 | TC-003 | ❌ |

## Test cases
### TC-001: Allowlisted host is reachable (happy path)
- **Requirement:** REQ-001
- **Input:** Launch box with an allowlist containing a known-reachable host (registry / provider API); in-box probe connects to it
- **Expected output:** Connection succeeds (exit 0 / HTTP response received)
- **Edge cases:** allowlisted host with port-specific rule; wildcard vs exact host match

### TC-002: Allowlist is plain-text and spec-documented (config contract)
- **Requirement:** REQ-002
- **Input:** Read the allowlist config file; cross-check against `docs/spec/configuration.md`
- **Expected output:** Config is plain text, parseable, and its format/semantics match the spec entry
- **Edge cases:** empty allowlist (= total deny); malformed entry rejected loudly

### TC-003 (NEGATIVE/escape): Non-allowlisted host is NOT reachable
- **Requirement:** REQ-001, REQ-003
- **Input:** In-box probe connects to a host NOT on the allowlist
- **Expected output:** Connection refused / dropped / DNS-blocked; non-zero exit; quote the failure mode
- **Edge cases:** IP-literal bypass attempt of a hostname rule; redirect from allowlisted to non-allowlisted host

## Notes
Framework: integration test launching a box + in-box network probes; assertion on connection result / exit code per destination. Egress posture is observed at L6 — quote both the allowlisted-success and non-allowlisted-block results. Builds on the task 014 box profile; uses runc unless a runsc result is also recorded (runtime tiering is task 016).
