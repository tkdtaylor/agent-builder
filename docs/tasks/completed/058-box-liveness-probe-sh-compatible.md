# Task 058: box liveness probe must be /bin/sh-compatible

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Fix the latent product bug that blocks the live Phase-0 probes (022/028/032): `sandboxBox.Create`
([internal/runtime/run.go](../../../internal/runtime/run.go)) ran a `/bin/true` liveness probe,
but the execution-box image is `ENTRYPOINT ["/bin/sh"]`, so the box ran `sh /bin/true` →
`ELF: not found` → exit 2 → `supervisor: create box: sandbox: create probe exited 2`. Change the
probe to an sh-compatible `-c true`, and make the fake launchers model the real image entrypoint
so this class of bug is caught at L5. Governing decision: ADR 032.

## Context

- Found by running the live capstone (task 032) for real — it reached real Claude/box setup then
  failed at `box.Create`. The publish leg is already proven (task 034: real PR l6#2).
- Latent because `box.Create` was only ever run against L5 fake launchers that did `exec "$@"`
  (no `/bin/sh` wrapper), so they never modelled `ENTRYPOINT ["/bin/sh"]`.
- Real-image check (this host): `… run.sh … -- /bin/true` → exit 2; `… -- -c true` → exit 0.

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-058-01 | `sandboxBox.Create` uses `Command: ["-c", "true"]` (sh-compatible liveness probe). | must |
| REQ-058-02 | The Podman execution-box fake launchers exec the command under `/bin/sh` (`exec /bin/sh "$@"`), modelling the real image's `ENTRYPOINT ["/bin/sh"]`. | must |
| REQ-058-03 | `go test ./...` + `make check` green; the live `TestPodmanRunnerLive` uses an sh-compatible command (`-c "echo hello"`). | must |
| REQ-058-04 | ADR 032 records the contract. | must |

## Readiness gate

- [x] Test spec `058-box-liveness-probe-sh-compatible-test-spec.md` exists
- [x] ADR 032 written
- [x] Real-image fix validated (`-- -c true` → exit 0) before merge

## Acceptance criteria

- [ ] [REQ-058-01] TC-058-01: wiring fake-launcher log shows `cmd=-c true`
- [ ] [REQ-058-02] TC-058-02: fakes use `exec /bin/sh "$@"`
- [ ] [REQ-058-03] TC-058-03: `make check` exit 0
- [ ] [REQ-058-04] TC-058-04: real-image old vs new launch comparison

## Verification plan

- **Highest level achievable in-repo:** L5 — `make check` / `go test ./...` green.
- **L6 (observed):** real-image `run.sh … -- -c true` → exit 0 (vs `-- /bin/true` → exit 2); the
  live capstone now proceeds past `box.Create`.

## Out of scope

- General `sandbox.Runner` exec-style command wrapping (callers supply sh-compatible commands per ADR 032).
