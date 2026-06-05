# Test Spec 008: Fitness F-001 — no Docker dev-env references

**Linked task:** [`docs/tasks/backlog/008-fitness-no-docker.md`](../backlog/008-fitness-no-docker.md)
**Written:** 2026-06-04
**Status:** stub — fleshed out fully when the task is picked up (before implementation)

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002, TC-003 | ❌ |
| REQ-002 | TC-004 | ❌ |
| REQ-003 | TC-005 | ❌ |

## Test cases
### TC-001: happy path — rule passes on clean repo
- **Requirement:** REQ-001
- **Input:** current clean tree; run `make fitness-no-docker`
- **Expected output:** exit code 0; pass message indicating no Docker dev-env references found
- **Edge cases:** prose mentions of "docker" in docs/task files must not trip the rule

### TC-002: NEGATIVE — rule fails when invariant is violated
- **Requirement:** REQ-001
- **Input:** add a `Dockerfile` (or `docker-compose.yml`, or a `docker run` reference) at the repo root; run `make fitness-no-docker`
- **Expected output:** non-zero exit code; message names the offending path
- **Edge cases:** lowercase/uppercase variants; `docker-compose.yaml` vs `.yml`. A fitness function that can't fail is worthless; this TC proves it fails.

### TC-003: exclusion — product-container path is allowed
- **Requirement:** REQ-001
- **Input:** place a Docker/OCI reference inside the designated product-container path (task 014's execution-box dir); run `make fitness-no-docker`
- **Expected output:** exit code 0 — the product-container path is excluded; references elsewhere still fail

### TC-004: wired into umbrella
- **Requirement:** REQ-002
- **Input:** `make fitness`
- **Expected output:** the run includes `fitness-no-docker`; a root-level Dockerfile causes `make fitness` to fail

### TC-005: spec row present
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/fitness-functions.md` Rules table
- **Expected output:** F-001 row present, threshold 0 hits, check command `make fitness-no-docker`, severity block

## Notes
Framework: grep over the working tree (scoped to exclude the product-container path, vendored deps, `.claude/`, and prose docs) invoked via Makefile target; assertion = exit code + message.
