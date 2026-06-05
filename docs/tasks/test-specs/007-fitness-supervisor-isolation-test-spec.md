# Test Spec 007: Fitness F-003 — supervisor has no LLM/untrusted-content dependency

**Linked task:** [`docs/tasks/active/007-fitness-supervisor-isolation.md`](../active/007-fitness-supervisor-isolation.md)
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
- **Input:** current clean tree; run `make fitness-supervisor-isolation`
- **Expected output:** exit code 0; pass message indicating the supervisor import graph is clean
- **Verification command:** `make fitness-supervisor-isolation`
- **Edge cases:** supervisor with only stdlib + intra-repo non-forbidden imports

### TC-002: NEGATIVE — rule fails when invariant is violated
- **Requirement:** REQ-001
- **Input:** add an import of an executor/LLM/web-fetch package (or a stub package matching the forbidden path pattern) into `internal/supervisor`; run `make fitness-supervisor-isolation`
- **Expected output:** non-zero exit code; message names the offending package
- **Verification command:** temporarily create a forbidden package path and import chain under `internal/supervisor`, run `make fitness-supervisor-isolation`, then remove the temporary files before commit
- **Edge cases:** transitive (not direct) forbidden import — must still be caught via `go list -deps`. A fitness function that can't fail is worthless; this TC is the proof it fails.

### TC-003: wired into umbrella
- **Requirement:** REQ-002
- **Input:** `make fitness`
- **Expected output:** the run includes `fitness-supervisor-isolation`; a forbidden import causes `make fitness` to fail
- **Verification command:** `make fitness`

### TC-004: spec row present
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/fitness-functions.md` Rules table
- **Expected output:** F-003 row present, structural category, threshold 0, check command `make fitness-supervisor-isolation`, severity block

### TC-005: forbidden package report uses package paths
- **Requirement:** REQ-001
- **Input:** import graph contains a forbidden package path matching executor, llm, web, webfetch, or web-fetch naming
- **Expected output:** failure message lists the matching package path rather than only a generic failure
- **Edge cases:** multiple forbidden imports are all reported deterministically

## Notes
Framework: `go list -deps ./internal/supervisor/...` filtered by forbidden package-path pattern, invoked via Makefile target; assertion = exit code + message. The negative test should exercise a transitive import so the check proves it uses the full dependency graph instead of direct source grep.
