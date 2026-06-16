# ADR 021: Podman is the sole default-run containment backend (srt removed)

**Date:** 2026-06-16
**Status:** Accepted
**Supersedes (in part):** the rented-isolation portion of ADR 020 for the *default run pipeline* — the `sandbox.Runner` seam itself is unchanged; only the concrete backend wired into `internal/runtime` changes.

## Context

Phase 0 wired `agent-builder run` to the rented `@anthropic-ai/sandbox-runtime`
(`srt`) backend behind the `sandbox.Runner` seam (ADR 020). Task 035 produced the
repo-owned Podman adapter (`internal/sandbox/podman`) behind the same seam. Phase 1
of the roadmap calls for swapping the rented isolation for the produced one,
resolving the chicken-and-egg: the agent that builds the blocks now runs inside a
block it owns.

The swap forces two design choices the seam itself does not dictate, plus an
evidence-shape choice for the Phase 1 acceptance harness.

## Decision

**1. Remove `srt` from the default run pipeline.** `internal/runtime` constructs a
`podman.Runner`-backed containment box and no longer imports
`internal/sandbox/sandboxruntime`. The `sandboxruntime` package remains in the tree
for reference/future deletion; only its use in the production run wiring is removed.
A fitness check (`fitness-no-srt`) asserts `internal/runtime` does not transitively
import `sandboxruntime`, wired into `make fitness`.

**2. The deprecated `AGENT_BUILDER_SANDBOX_RUNTIME` variable errors loudly when set.**
`ConfigFromEnv` no longer reads it as a required input. If a non-empty value is
present, `ConfigFromEnv` returns an error naming the variable as removed and pointing
at the Podman swap. This follows the project's fail-fast principle and satisfies the
"no silent acceptance of the deprecated variable" contract — a stale Phase 0 env var
produces a clear migration signal rather than being silently ignored.

**3. The launcher path is injectable via `AGENT_BUILDER_EXEC_BOX_LAUNCHER`,**
defaulting to `containment/execution-box/run.sh`. This mirrors the existing
overridable CLI-path seams (`AGENT_BUILDER_CLAUDE_CLI`, `AGENT_BUILDER_GIT_CLI`,
`AGENT_BUILDER_GH_CLI`) and lets the subprocess-level CLI and e2e harnesses point the
real binary at a fake launcher, so the Phase 1 fake-Podman acceptance harness reaches
L5 without requiring real Podman on the host. Podman adapter resource/image/egress
configuration continues to flow through the `EXEC_BOX_*` variables the launcher
already reads; the Go side adds no new `EXEC_BOX_*` parsing.

**4. The run record carries Podman containment evidence.** The default run wiring
emits launcher evidence naming `containment=podman` (and the resolved launcher path)
into the run record, with no `srt`/`sandbox-runtime` reference anywhere in the
pipeline output. This is the observable assertion the Phase 1 acceptance harness
(task 037) checks.

## Consequences

- `agent-builder run` no longer requires `srt` or `AGENT_BUILDER_SANDBOX_RUNTIME`;
  this is a breaking change to the Phase 0 run contract, updated in
  `docs/spec/configuration.md`, `docs/spec/interfaces.md`, and `docs/spec/SPEC.md` in
  the same change.
- The Phase 0 acceptance docs and the doc-honesty assertions in the existing e2e
  harness (which named `srt` as *pending* L6 evidence) are realigned to Phase 1
  reality: Podman is the containment backend; `srt` is no longer a required runtime.
  The shared e2e/CLI fixtures (tasks 028, 032, 034) drop the fake `srt` subprocess and
  set the fake launcher instead.
- The `sandbox.Runner` seam and the `podman.Runner` adapter are unchanged — the swap
  is a wiring + configuration change, not a contract change.
- `sandboxruntime` remains importable for reference; the fitness check is what keeps
  it out of the production pipeline, not its deletion.
