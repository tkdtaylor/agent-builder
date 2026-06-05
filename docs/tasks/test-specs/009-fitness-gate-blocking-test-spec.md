# Test Spec 009: Fitness F-002 — verification gate is blocking (no skip route)

**Linked task:** [`docs/tasks/backlog/009-fitness-gate-blocking.md`](../backlog/009-fitness-gate-blocking.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002 | ❌ |
| REQ-002 | TC-003 | ❌ |
| REQ-003 | TC-004 | ❌ |

## Test cases
### TC-001: happy path — rule passes on clean repo
- **Requirement:** REQ-001
- **Input:** current tree with gate + dep-scan + code-scanner steps present and no skip route; run `make fitness-gate-blocking`
- **Expected output:** exit code 0; pass message indicating no bypass found around the scanner steps
- **Edge cases:** comments/tests that mention `--no-verify` only to assert its absence must not trip the rule

### TC-002: NEGATIVE — rule fails when invariant is violated
- **Requirement:** REQ-001
- **Input:** add a `--no-verify` CLI flag (or an `if skip { return ok }` conditional that returns a passing verdict without running dep-scan/code-scanner); run `make fitness-gate-blocking`
- **Expected output:** non-zero exit code; message names the offending file/location
- **Edge cases:** env-var short-circuit (e.g. `SKIP_SCAN=1`) must also be caught. A fitness function that can't fail is worthless; this TC proves it fails.

### TC-003: wired into umbrella
- **Requirement:** REQ-002
- **Input:** `make fitness`
- **Expected output:** the run includes `fitness-gate-blocking`; a bypass causes `make fitness` to fail

### TC-004: spec row present
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/fitness-functions.md` Rules table
- **Expected output:** F-002 row present, security category, threshold 0 bypasses, check command `make fitness-gate-blocking`, severity block

## Notes
Framework: grep over the gate package + CLI for skip/no-verify flags, env-var short-circuits, and early-return conditionals around the scanner steps, invoked via Makefile target; assertion = exit code + message.
