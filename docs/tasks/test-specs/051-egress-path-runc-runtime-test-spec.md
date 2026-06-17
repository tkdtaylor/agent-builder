# Test spec — Task 051: rootless egress path runs under runc (+ `--add-host` on pod-create)

**Task:** 051-egress-path-runc-runtime
**Created:** 2026-06-17
**ADR:** 030 (the networked/egress-pod rootless path runs under runc; gVisor unavailable on that path only)

## Context

Task 050 fixed the egress sidecar to work rootless (ADR 029), which let the
`--egress-probe` run advance past the sidecar and exposed two *further* rootless-pod
constraints on the real host (podman 5.7.0, keep-id, subuid 100000):

1. **`--add-host` placement.** Extra host entries on a pod *member* are rejected:
   `Error: invalid config provided: extra host entries must be specified on the pod:
   network cannot be configured when it is shared with a pod`. They must be declared on
   `podman pod create`.
2. **gVisor cannot join a rootless pod userns.** With the agent-tier default runtime
   (`runsc`), the egress workload member fails: `runsc: cannot create gofer process:
   gofer: error setting namespace of type user ... invalid argument`. Confirmed general
   limitation: `runsc` works standalone, fails in any keep-id pod. The egress workload
   must run under `runc`; egress filtering is enforced by the shared **pod-netns**
   nftables regardless of the workload runtime (verified: under `runc` the probe exits 0
   with `TC-003`/`TC-004 PASS`).

ADR 030 (Accepted, Option A) decides: on the rootless egress path the workload runtime
resolves to `runc`; an **explicit** `--runtime runsc` together with the egress path
**fails loudly** (pointing at ADR 030) rather than silently downgrading; non-networked
paths keep the ADR-016 defaults (`agent`→`runsc` standalone). `--add-host` moves to
`podman pod create`. **The default-deny egress allowlist (ADR 015) is unchanged and fully
enforced** — this task changes only which OCI runtime the egress *workload* uses and where
`--add-host` is declared.

## Test cases

All L5 cases drive `run.sh` with the stub-podman harness (the pattern in
`containment/execution-box/tests/userns-pod-test.sh`), capturing argv per podman
subcommand (`pod create`, sidecar `run -d`, workload `run`).

### TC-051-01 (L5) — default agent (runsc) + `--egress-probe`: workload runs under runc
- **Assertion:** with no `--runtime` override (agent tier → runsc default), the egress
  workload `podman run` argv contains `--runtime runc` (NOT `--runtime runsc`) and
  `--label agent-builder.runtime=runc`; the run does NOT `die`. (The agent-tier runsc
  default is silently resolved to runc on the egress path, per ADR 030.)

### TC-051-02 (L5) — explicit `--runtime runsc` + `--egress-probe`: fail loudly
- **Assertion:** `run.sh --egress-probe --runtime runsc` exits non-zero and prints a
  `die` message that names ADR 030 and the rootless-pod-userns / gVisor limitation; NO
  egress workload `podman run` is issued (the guard fires before workload launch). It must
  NOT silently downgrade to runc.

### TC-051-03 (L5) — explicit `--runtime runc` + `--egress-probe`: runs under runc, no die
- **Assertion:** the egress workload argv contains `--runtime runc`; the run does not
  `die`. (Explicit runc on the egress path is honored unchanged.)

### TC-051-04 (L5) — `--add-host` declared on the pod, not the member
- **Assertion:** the resolved allowlisted host entries appear as `--add-host H:IP` on the
  `podman pod create` argv, and do NOT appear on the egress workload `podman run` argv.
  (The `--dns none` workload still resolves allowlisted hosts via the pod's host entries.)

### TC-051-05 (L5, regression) — non-pod paths keep the ADR-016 runtime unchanged
- **Assertion:** the plain `--probe` path (no pod) still uses the agent-tier default
  `--runtime runsc` on its container argv; the egress-path runc override must NOT leak to
  the non-egress probe/workload paths. (Pairs with the existing TC-016 runtime tests.)

### TC-051-06 (L6, real host) — the real `--egress-probe` enforces default-deny and exits 0
- **Mechanism (real host, rootless podman 5.x, runsc-rootless wrapper, gate-tools
  populated):**
  - (a) `bash containment/execution-box/run.sh --worktree . --gate-tools <populated>
    --egress-probe` (default agent tier).
  - (b) `bash containment/execution-box/run.sh --worktree . --gate-tools <populated>
    --runtime runsc --egress-probe`.
- **Assertion (the proof of ADR 030 + the phase deliverable):**
  - (a) prints `TC-003 PASS: allowlisted connect succeeded: <allow_host>:<port>`,
    `TC-004 PASS: non-allowlisted connect blocked: <deny_host>:<port>`, and `TC-004 PASS:
    direct IP bypass blocked: <deny_ip>:<port>`, and the launcher **exits 0** — egress
    default-deny is enforced rootless with the workload under runc.
  - (b) exits non-zero with the ADR-030 `die` message (explicit runsc + egress refused).
  Record both verbatim. This real run is the only thing that can prove rootless egress
  enforcement end-to-end; the L5 stub cannot.

## Verification plan

- **Highest level achievable:** **L6** — the real `--egress-probe` (default) exits 0 with
  `TC-003`/`TC-004 PASS`, and `--runtime runsc --egress-probe` fails loudly, on the rootless
  host. This is the phase's green-probe deliverable.
- **L5 harness:** extend `containment/execution-box/tests/` (stub-podman argv capture) with
  TC-051-01..05; run standalone as L5 evidence recorded in `coverage-tracker.md` (the
  045–051 execution-box harness convention; not wired into `make check`, which runs
  `go test`).
- **L6 evidence:** quote the verbatim `--egress-probe` output (TC-003/TC-004 PASS + exit 0)
  and the `--runtime runsc` `die` line from the real rootless host.
- **Cross-module state risk:** none — confined to the egress (pod) block of `run.sh`
  (runtime resolution for the egress workload + `--add-host` placement).
- **Runtime-visible surface:** the egress workload `--runtime`/`--label` argv, the
  `--add-host`-on-pod-create argv, the explicit-runsc `die`, and the `--egress-probe`
  allow/deny outcome + exit code.

## Out of scope

- The egress allowlist policy, `@allowed_tcp4` model, default-deny `policy drop`,
  NET_ADMIN sidecar, workload `--cap-drop`/`--read-only`/`--user`/`--dns none` posture —
  all unchanged (only the workload OCI runtime and `--add-host` placement change).
- The sidecar fixes (task 050 / ADR 029) and the `--userns`/`--pod` launch mechanics
  (task 049).
- Non-`runsc` non-`runc` runtimes (e.g. `kata`) on the egress path — untested under
  rootless pods; only `runsc` is special-cased (it is the runtime with the gofer
  userns-join limitation). `runc` passes through; other runtimes pass through unchanged
  and are not validated here.
- A rootful or Kata/Firecracker networked tier, or an upstream gVisor fix — the ADR 030
  reopening paths, deferred.
