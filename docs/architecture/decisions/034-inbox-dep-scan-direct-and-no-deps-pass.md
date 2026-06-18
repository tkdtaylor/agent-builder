# ADR 034 — In-box dependency scanning calls `dep-scan` directly; a module with no `go.sum` passes

**Status:** Accepted
**Date:** 2026-06-17
**Relates to:** ADR 032 (box liveness probe — same "gate step only run against L5 fakes" failure class), the CLAUDE.md verification-gate invariant, dep-scan tooling

## Context

The production verification gate's `DepScanStep`
([internal/gate/go_steps.go](../../../internal/gate/go_steps.go)) ran `gods` with **no arguments**.
`gods` is a `go` **drop-in wrapper** (gate-tools/gods): it dep-scans any package arguments, then
`exec go "$@"`. With no arguments it scans nothing and falls through to a bare `exec go`, which
prints Go's top-level help and exits non-zero. So the step failed on a tooling misuse — not a real
dependency finding.

This was latent because:
- The host `make check` target is `lint test fitness` — it **never runs dep-scan at all**.
- `DepScanStep`'s unit tests used a **fake `gods`** that always `exit 0`, so they never modelled the
  real wrapper's no-args behavior.

It surfaced the first time the real production gate ran inside the execution box during the live
Phase-0 capstone (probes 022/028/032): `PASS go build; PASS go vet; PASS go test; PASS gofmt; PASS
golangci-lint; FAIL gods`. Same failure class as ADR 032 (box liveness probe): a gate step only ever
exercised against L5 fakes, breaking on the first real run.

Two further facts shaped the decision:
- `agent-builder` itself has **no third-party dependencies** (`go.mod` has no `require`, there is no
  `go.sum`). A supply-chain scanner has nothing to scan.
- The `dep-scan` binary was **not** among the mounted gate tools in the box (gate-tools shipped
  `golangci-lint`, `gods`, `code-scanner`), and `gods` shells out to `dep-scan`.

## Decision

**The gate scans dependencies by calling `dep-scan` directly, not the `gods` go-wrapper**, and **a
module with no `go.sum` passes the step without invoking the scanner.**

Concretely, `DepScanStep.Run(repoPath)`:
- If `repoPath/go.sum` does **not** exist → **PASS** (no third-party dependencies ⇒ no supply-chain
  surface; Go requires a `go.sum` for any `require`, so "no go.sum" reliably means "no external
  deps"). The scanner is not invoked and need not be present.
- If `go.sum` **exists** → run `dep-scan check --registry go --lockfile go.sum --lockfile-type go`
  in `repoPath`; a non-zero exit (high-severity finding) fails the step with captured output.

`dep-scan` becomes a **required, read-only, mounted gate tool**, replacing `gods` in the box
toolchain contract (`required_mounted_gate_tools` in
[containment/execution-box/run.sh](../../../containment/execution-box/run.sh)). The `gods` wrapper
remains available for interactive dev use but is no longer what the gate runs.

## Consequences

- The in-box gate clears the dep-scan step for `agent-builder` (no `go.sum`), unblocking the live
  capstone past dep-scan.
- The gate's dep-scan invocation is now correct for any module: a real lockfile is scanned with the
  right `dep-scan` arguments; an empty dependency set is a trivial pass rather than a tooling crash.
- The box toolchain contract now requires `dep-scan` to be mounted; like the other gate-tool
  binaries it is gitignored and populated per host.
- **Out of scope / deferred:** when a scanned module *does* have a `go.sum`, `dep-scan` queries a
  CVE backend over the network, which the default-deny egress allowlist would block. Adding that
  egress destination is deferred until a scanned module actually carries dependencies — `agent-builder`
  has none, so the live capstone never reaches the network path. This is noted here so the next
  module with deps does not rediscover it as a latent bug.
