# Test Spec 008: Fitness F-001 — no Docker dev-env references

**Linked task:** [`docs/tasks/backlog/008-fitness-no-docker.md`](../backlog/008-fitness-no-docker.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-002, TC-003 | ✅ |
| REQ-002 | TC-004 | ✅ |
| REQ-003 | TC-005 | ✅ |

## Test cases
### TC-001: happy path — rule passes on clean repo
- **Requirement:** REQ-001
- **Input:** current clean tree; run `make fitness-no-docker`
- **Expected output:** exit code 0; pass message indicating no Docker dev-env references found
- **Assertion:** final line contains `PASS fitness-no-docker`; the command leaves the working tree clean
- **Edge cases:** prose mentions of "docker" in docs/task/test-spec files, `.claude/`, and `docs/spec/fitness-functions.md` must not trip the rule

### TC-002: NEGATIVE — rule fails when invariant is violated
- **Requirement:** REQ-001
- **Input:** add each root-level fixture independently, then run `make fitness-no-docker`:
  - `Dockerfile`
  - `docker-compose.yml`
  - a temporary text file containing `docker run`
- **Expected output:** non-zero exit code; message names the offending path
- **Cleanup:** remove the temporary fixture after each assertion and confirm `git status --short` no longer includes it
- **Edge cases:** lowercase/uppercase variants; `docker-compose.yaml` vs `.yml`; root-level filenames must be caught even if file contents are empty. A fitness function that can't fail is worthless; this TC proves it fails.

### TC-003: exclusion — product-container path is allowed
- **Requirement:** REQ-001
- **Input:** create a temporary file containing a Docker/OCI reference under `containment/`; run `make fitness-no-docker`
- **Expected output:** exit code 0 — the product-container path is excluded; references elsewhere still fail
- **Cleanup:** remove the temporary allowed-path fixture and any empty temporary directories created for the test

### TC-004: wired into umbrella
- **Requirement:** REQ-002
- **Input:** `make fitness`
- **Expected output:** the run includes `fitness-no-docker` and exits 0 on the clean tree
- **Negative assertion:** a root-level `Dockerfile` causes `make fitness` to fail before the umbrella success message

### TC-005: spec row present
- **Requirement:** REQ-003
- **Input:** inspect `docs/spec/fitness-functions.md` Rules table
- **Expected output:** F-001 row present with category `structural/security`, threshold `0 hits`, check command `make fitness-no-docker`, severity `block`, and a why statement tied to rootless Podman containment

## Notes
Framework: grep over the working tree (scoped to exclude the product-container path, vendored deps, `.claude/`, and prose docs) invoked via Makefile target; assertion = exit code + message.

Manual verification evidence to collect before the feature commit:
- `make fitness-no-docker` on the clean tree
- root-level `Dockerfile` negative fixture fails and names the path
- root-level `docker-compose.yml` negative fixture fails and names the path
- root-level `docker run` content fixture fails and names the path
- allowed-path fixture under `containment/` passes
- `make fitness` includes the rule and passes on the clean tree
