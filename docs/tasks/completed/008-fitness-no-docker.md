# Task 008: Fitness F-001 — no Docker dev-env references

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** completed (verified L6)

## Goal
Add a fitness check (`make fitness-no-docker`) that greps the repo for `docker`/`docker-compose`/`Dockerfile` dev-environment references and fails on any hit outside a designated product-container directory, enforcing that the substrate is rootless Podman, not Docker.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §4 (rootless Podman replaces Docker; tiered runtime runc → gVisor → Kata/Firecracker)
- Spec: `docs/spec/fitness-functions.md` (Rules table — add F-001 row), `docs/spec/SPEC.md` (candidate fitness fn F-001: no Docker dev-env references; product container defs live under a named dir, not a dev container)
- Related ADRs: none yet
- Dependencies: 001 (walking skeleton — repo baseline to scan)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | A `fitness-no-docker` Makefile target that greps the repo for `docker`, `docker-compose`, and `Dockerfile` references and exits non-zero on any hit, EXCLUDING the designated product-container path (the execution-box profile from task 014); exits 0 otherwise | must have |
| REQ-002 | The target is added to the `fitness` umbrella target's prerequisites | must have |
| REQ-003 | A row for F-001 is added to the Rules table in `docs/spec/fitness-functions.md` (structural/security; asserts no Docker dev-env references outside the allowed product-container dir; threshold 0 hits; severity block) | must have |

## Readiness gate
- [x] Test spec exists in `docs/tasks/test-specs/`
- [x] All acceptance criteria have a linked REQ ID
- [x] Blocking tasks complete: 001

## Acceptance criteria
- [x] [REQ-001] `make fitness-no-docker` exits 0 on the current clean tree and prints a pass message
- [x] [REQ-001] A `Dockerfile` (or `docker-compose.yml`, or a `docker` reference) added at the repo root causes the target to exit non-zero and report the offending path
- [x] [REQ-001] A Docker reference inside the designated product-container path does NOT trip the rule
- [x] [REQ-002] `make fitness` invokes `fitness-no-docker` as part of the umbrella run
- [x] [REQ-003] The F-001 row exists in `docs/spec/fitness-functions.md` and points to the `make fitness-no-docker` check command

## Verification plan
- **Highest level achievable:** L3 — fitness rule run via Makefile target.
- Command: `make fitness-no-docker` passes on the current tree.
- Negative test: drop a `Dockerfile` at the repo root, re-run the target, confirm it exits non-zero and names the path; then remove it.
- **Cross-module state risk:** none — read-only grep; adds a Makefile target and a spec row.
- **Runtime-visible surface:** `make fitness-no-docker` output (pass/fail + exit code), and the same rule via `make fitness`.

## Out of scope
- Creating the product-container directory itself (introduced by task 014, the containment profile)
- Scanning git history; this is a working-tree scan only
- The `.claude/` tooling/hook tree, vendored deps, and this task/spec file's own literal mentions of "docker" must not produce false positives — scope the grep accordingly

## Notes
- The allowed product-container dir is introduced by task 014 (execution-box / containment profile). Reference it as the single exclusion path but do not hard-couple: if the dir does not yet exist, the rule still passes (no hits anywhere). When 014 lands it places its Docker/OCI artifacts under that path.
- Exclude the grep from matching its own task/spec/ADR documentation files where the words appear as prose, not config — match dev-env config references, not narrative.
- Per `docs/spec/fitness-functions.md` "How to run", the three sub-changes (target, umbrella prerequisite, Rules row) land together in the implementing commit.

## Verification evidence

- **Positive fitness check:** `make fitness-no-docker` -> `PASS fitness-no-docker: no forbidden dev-environment references found.`
- **Negative filename checks:** temporary root-level `Dockerfile`, `dockerfile`, `DOCKERFILE`, `docker-compose.yml`, and `Docker-compose.yml` fixtures made `make fitness-no-docker` fail and name the offending path; temporary files removed before commit.
- **Negative content check:** temporary root-level `tmp-reference.txt` containing `docker run example` made `make fitness-no-docker` fail and name `./tmp-reference.txt`; temporary file removed before commit.
- **Allowed product-container path:** temporary `containment/tmp-reference.txt` containing `docker run allowed-product-artifact` did not trip `make fitness-no-docker`; temporary file removed before commit.
- **Umbrella fitness:** `make fitness` includes `fitness-no-docker`; clean tree -> `Fitness checks passed.`; root-level `Dockerfile` fixture fails through the umbrella target before the success message.
- **Repo checks:** `gofmt -w .` -> no changes; `go test ./...` -> `ok github.com/tkdtaylor/agent-builder/internal/gate`; `go build ./...` -> success; `env PATH=/tmp/agent-builder-tools:$PATH make check` -> `All checks passed.`
- **Spec-verifier:** read-only worker verifier APPROVE — all REQ/TC assertions satisfied; case-insensitive filename variants covered; lifecycle hygiene confirmed.
