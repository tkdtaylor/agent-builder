# Task 051: rootless egress path runs under runc (+ `--add-host` on pod-create)

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** backlog

## Goal

Make the real-host `--egress-probe` reach the allow/deny assertions and exit 0 under
rootless podman by implementing ADR 030, **without changing the egress allowlist
semantics**:

1. **Egress workload runs under runc.** In the egress (pod) block of
   `containment/execution-box/run.sh`, resolve the egress *workload* runtime to `runc`
   (overriding the agent-tier `runsc` default), because the workload must join the pod's
   keep-id userns and gVisor's gofer cannot. Substitute the `--runtime` value and the
   `agent-builder.runtime` label carried into `workload_args` accordingly.
2. **Fail loudly on explicit runsc + egress.** If the operator explicitly passed
   `--runtime runsc` (or `EXEC_BOX_RUNTIME=runsc`) AND the egress path is taken, `die`
   with a message naming ADR 030 and the rootless-pod-userns / gVisor limitation —
   never silently downgrade.
3. **`--add-host` on the pod.** Declare the resolved allowlisted host entries via
   `--add-host` on `podman pod create` (not on the pod member), so the `--dns none`
   workload resolves allowlisted hosts while satisfying podman's "extra host entries must
   be specified on the pod" rule.

This is the second half of the rootless-egress phase: task 050 fixed the sidecar (ADR
029); this task makes the egress *workload* launch and the probe go green (ADR 030).

## Context

- Tech stack: bash. One file: `containment/execution-box/run.sh` (egress pod block,
  ~lines 595-667; runtime resolution `resolve_runtime`/`runtime_source` ~lines 176-302;
  `common_args` ~lines 497-522). No Go code.
- **ADR 030** governs this — Option A (egress path → runc; explicit runsc + egress fails
  loudly; gVisor unavailable on the networked rootless path only; non-networked paths keep
  ADR-016 defaults). Read ADR 030 before implementing. ADR 015 (default-deny egress) is
  the load-bearing control preserved unchanged.
- Reproduction-confirmed on the real host (podman 5.7.0, rootless, keep-id, subuid 100000):
  with `--add-host` on the pod + the workload under runc, `--egress-probe` exits 0 with
  `TC-003 PASS` (allow) + `TC-004 PASS` (deny host + direct-IP); runsc in a keep-id pod
  fails with the gofer userns error; runsc standalone works.
- The egress allowlist is the **load-bearing** control. This task changes only the
  workload's OCI runtime and the placement of `--add-host` — not the deny/allow logic, the
  nftables ruleset, NET_ADMIN, or the workload cap-drop/read-only/user/dns posture.
- **Model tier: balanced (sonnet)** — a small, security-relevant launch-arg change in the
  egress path; the runtime divergence + fail-loud guard must be precise and test-covered.
- Dependencies: builds on task 050 (merged: sidecar fixes) and 049 (pod/userns launch).

## Requirements

| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-051-01 | On the egress path, the workload `podman run` carries `--runtime runc` and `--label agent-builder.runtime=runc` when the resolved runtime is `runsc` from the agent-tier default (silent resolve to runc, no error) | must have |
| REQ-051-02 | If `--runtime runsc` (or `EXEC_BOX_RUNTIME=runsc`) is set **explicitly** (`runtime_source` ≠ `default`) AND the egress path is taken, `run.sh` `die`s with a message naming ADR 030 and the rootless-pod-userns limitation, before any egress workload `podman run`; non-zero exit | must have |
| REQ-051-03 | Explicit `--runtime runc` + egress is honored unchanged (workload runs runc, no die); a non-`runsc` resolved runtime passes through unchanged | must have |
| REQ-051-04 | Resolved allowlisted host entries are declared as `--add-host H:IP` on `podman pod create`, and are NOT passed on the egress workload member `podman run` | must have |
| REQ-051-05 | The non-pod `--probe`/plain-workload paths keep the ADR-016 runtime (agent→runsc) unchanged — the egress runc override does not leak to non-egress paths | must have |
| REQ-051-06 | Real-host `--egress-probe` (default) reaches `TC-003 PASS` (allow) + `TC-004 PASS` (deny host + direct-IP) and exits 0; `--runtime runsc --egress-probe` dies with the ADR-030 message (non-zero) | must have |
| REQ-051-07 | `docs/spec/behaviors.md` (B-010), `docs/spec/interfaces.md` (`--runtime`/`--egress-probe`), and `docs/spec/architecture.md` (runtime-tier/egress rows, if present) rewritten in place to state the rootless egress path uses runc, referencing ADR 030; `docs/architecture/diagrams.md` updated if a diagrammed runtime flow changed; egress allow/deny contract text unchanged | must have |

## Readiness gate

- [x] Test spec `051-egress-path-runc-runtime-test-spec.md` exists
- [x] ADR 030 written and Accepted
- [x] All acceptance criteria below have a linked REQ ID
- [ ] No blocking dependencies (049, 050 merged on main)

## Acceptance criteria

- [ ] [REQ-051-01] egress workload argv has `--runtime runc` + `--label agent-builder.runtime=runc` on agent default (TC-051-01)
- [ ] [REQ-051-02] explicit runsc + egress → die naming ADR 030, non-zero, no workload run (TC-051-02)
- [ ] [REQ-051-03] explicit runc + egress → runc, no die (TC-051-03)
- [ ] [REQ-051-04] `--add-host` on pod create, absent from workload member (TC-051-04)
- [ ] [REQ-051-05] non-pod `--probe` runtime unchanged (runsc default) (TC-051-05)
- [ ] [REQ-051-06] real-host: default egress-probe TC-003+TC-004 PASS + exit 0; runsc egress-probe dies (TC-051-06)
- [ ] [REQ-051-07] spec (B-010 + interfaces + architecture) + diagrams reference ADR 030; egress contract unchanged

## Verification plan

- **Highest level achievable:** **L6** — the real `--egress-probe` (default) exits 0 with
  `TC-003`/`TC-004 PASS`, and `--runtime runsc --egress-probe` fails loudly, on the rootless
  host. This is the phase's green-probe deliverable; the stub L5 cannot prove rootless
  egress enforcement.
- **L5 harness:** extend `containment/execution-box/tests/` (stub-podman argv capture) with
  TC-051-01..05; run standalone as L5 evidence in `coverage-tracker.md` (045–051 harness
  convention; not wired into `make check`). `make check`/`make fitness` still gate Go +
  fitness.
- **L6 evidence:** quote the verbatim `--egress-probe` output (TC-003/TC-004 PASS + exit 0)
  and the explicit-`runsc` `die` line from the real rootless host.
- **Cross-module state risk:** none — confined to the egress (pod) block of `run.sh`.
- **Runtime-visible surface:** egress workload `--runtime`/`--label` argv, `--add-host`
  placement, the explicit-runsc die, and the `--egress-probe` allow/deny outcome + exit.

## Out of scope

- Egress allowlist policy / `@allowed_tcp4` / default-deny `policy drop` / NET_ADMIN /
  workload cap-drop/read-only/user/dns posture — all unchanged.
- Sidecar fixes (task 050 / ADR 029) and `--userns`/`--pod` launch (task 049).
- Non-`runsc` non-`runc` runtimes (`kata`) on the egress path — only `runsc` is
  special-cased; `runc` and others pass through, not validated here.
- A rootful / Kata networked tier or upstream gVisor fix — ADR 030 reopening paths.

## Notes

- Keep the change surgical and inside the egress block. Compute an `egress_runtime` and
  build `workload_args` substituting `--runtime`'s value and the `agent-builder.runtime`
  label; the existing filter that drops `--userns=keep-id` stays. Do not touch
  `resolve_runtime`/`common_args` for the non-egress paths (REQ-051-05).
- The fail-loud guard (REQ-051-02) must fire BEFORE the egress workload `podman run` (place
  it near the start of the egress block so an explicit-runsc operator never gets a
  half-started pod). The fail-loud guards from task 045 stay.
- `--add-host` entries are built from `$add_hosts_file`, which `resolve_egress_plan`
  populates before `podman pod create` — so the pod-create `--add-host` args can be built
  from it (the exploratory fix used a `pod_add_host_args` array filled from the same file).
