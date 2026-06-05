# Test Spec 009: Fitness F-002 — verification gate is blocking (no skip route)

**Linked task:** [`docs/tasks/backlog/009-fitness-gate-blocking.md`](../backlog/009-fitness-gate-blocking.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002, TC-005 | ✅ |
| REQ-002 | TC-003 | ✅ |
| REQ-003 | TC-004 | ✅ |

## Test cases
### TC-001: happy path — rule passes on clean repo
- **Requirement:** REQ-001
- **Input:** current tree with gate + dep-scan + code-scanner steps present and no skip route; run `make fitness-gate-blocking`
- **Expected output:** exit code 0; pass message indicating no bypass found around the scanner steps
- **Assertion:** final line contains `PASS fitness-gate-blocking`; the command leaves the working tree clean
- **Edge cases:** docs/task/test-spec prose that mentions `--no-verify`, skip routes, or bypasses only to define this rule must not trip the check

### TC-002: NEGATIVE — rule fails when invariant is violated
- **Requirement:** REQ-001
- **Input:** add each temporary source violation independently, then run `make fitness-gate-blocking`:
  - a `--no-verify` or `--skip-verify` flag in `cmd/agent-builder`
  - an env-var short-circuit such as `SKIP_SCAN`, `SKIP_DEP_SCAN`, `SKIP_CODE_SCANNER`, or `NO_VERIFY`
  - an early-return conditional near the gate/scanner path such as `if skip { return StepResult{OK: true} }`
- **Expected output:** non-zero exit code; message names the offending file/location
- **Cleanup:** remove the temporary fixture after each assertion and confirm `git status --short` no longer includes it
- **Edge cases:** source comments containing the forbidden terms should not count unless they are on code lines that define a bypass affordance. A fitness function that can't fail is worthless; this TC proves it fails.

### TC-003: wired into umbrella
- **Requirement:** REQ-002
- **Input:** `make fitness`
- **Expected output:** the run includes `fitness-gate-blocking` and exits 0 on the clean tree
- **Negative assertion:** a temporary `--no-verify` source fixture causes `make fitness` to fail before the umbrella success message

### TC-004: spec row present
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/fitness-functions.md` Rules table
- **Expected output:** F-002 row present with category `security`, threshold `0 bypass affordances`, check command `make fitness-gate-blocking`, severity `block`, and a why statement tied to the verification gate being the definition of done

### TC-005: scoped source scan avoids prose false positives
- **Requirement:** REQ-001
- **Input:** docs and task files contain prose references to `--no-verify`, skip routes, scanner bypasses, and this test case; run `make fitness-gate-blocking`
- **Expected output:** exit code 0 as long as the gate package and CLI source expose no bypass affordance
- **Edge cases:** the scanner scope must include `cmd/agent-builder` and `internal/gate`, and exclude prose-only docs so the rule is strict on code but not self-triggering

## Notes
Framework: grep over the gate package + CLI for skip/no-verify flags, env-var short-circuits, and early-return conditionals around the scanner steps, invoked via Makefile target; assertion = exit code + message.

Manual verification evidence to collect before the feature commit:
- `make fitness-gate-blocking` on the clean tree
- temporary `--no-verify` CLI fixture fails and names the path
- temporary scanner skip env-var fixture fails and names the path
- temporary early-return skip conditional fixture fails and names the path
- `make fitness` includes the rule and passes on the clean tree
- `docs/spec/fitness-functions.md` contains the F-002 row
