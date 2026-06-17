# ADR 032 — Execution-box commands must be `/bin/sh`-compatible; box liveness probe uses `-c true`

**Status:** Accepted
**Date:** 2026-06-17
**Supersedes / relates to:** ADR 021 (Podman containment swap), ADR 026 (Podman containment), ADR 031 (L6 live-mode probes)

## Context

The execution-box image declares `ENTRYPOINT ["/bin/sh"]`
([containment/execution-box/Containerfile](../../../containment/execution-box/Containerfile)).
Therefore any container command passed after the launcher's `--` separator is handed to
`/bin/sh` as **its arguments**: `podman run <image> X Y` becomes `sh X Y`, i.e. `sh` runs
`X` as a **script file** with `Y` as `$1`.

The orchestrator's box-liveness probe, `sandboxBox.Create`
([internal/runtime/run.go](../../../internal/runtime/run.go)), ran `Command: ["/bin/true"]`.
Against the real image that becomes `sh /bin/true` — `/bin/sh` tries to *read the `/bin/true`
ELF binary as a shell script*, producing `/bin/true: line 0: ELF: not found` and exit 2. The
box never started, so the supervisor failed with `supervisor: create box: sandbox: create
probe exited 2`, blocking the live Phase-0 probes (022/028/032).

This was latent because `box.Create` (and the `sandbox.Runner` command path generally) was only
ever exercised against the **L5 fake launcher**; the bug surfaced the first time a real Claude
capstone drove the real image. The same latency affected `TestPodmanRunnerLive`, which passed
`["echo","hello"]` (→ `sh echo hello`, also broken) but had never been run live.

## Decision

**Commands run inside the execution box must be `/bin/sh`-compatible** — either a shell script
path the image can execute, or an `sh -c`-style invocation. We do **not** change the image
entrypoint (the `--probe`/`--egress-probe` paths and the gate rely on the `sh <script>`
contract) and we do **not** make the `podman` runner silently wrap arbitrary commands (that
would change the `sandbox.Request.Command` contract and ripple into every fake launcher).

Concretely:
- `sandboxBox.Create`'s liveness probe is `Command: ["-c", "true"]` → `sh -c true` → exits 0,
  a faithful "can the box start under the runtime" check.
- Callers of `sandbox.Runner.Run` supply `sh`-compatible commands. `TestPodmanRunnerLive` uses
  `["-c", "echo hello"]`.

## Consequences

- The live Phase-0 box launch starts cleanly; 022/028/032 can proceed past `box.Create`.
- The `Command` contract is now explicit: it is the argument vector passed to the image's
  `/bin/sh` entrypoint, not an exec-style argv. This is documented here and asserted by the
  live-gated `TestPodmanRunnerLive`.
- Follow-up (out of scope here): if a future caller needs exec-style argv semantics, add an
  explicit `sh -c` wrapper in the runner behind a documented option rather than changing this
  contract implicitly.
