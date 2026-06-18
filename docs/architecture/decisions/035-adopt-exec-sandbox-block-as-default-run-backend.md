# ADR 035 — Adopt the shipped exec-sandbox block as the default run() backend

**Status:** Accepted
**Date:** 2026-06-18
**Supersedes (in part):** ADR 020 (exec-sandbox run adapter seam — its "internal launcher-shell adapter is the production default" stance) and ADR 021 (Podman is the *sole* default-run containment backend — that "sole default" claim no longer holds). The `sandbox.Runner` seam contract from ADR 020 is unchanged; only the concrete default backend wired behind it changes.

## Context

ADR 020 defined the exec-sandbox `run()` adapter seam as an *internal* launcher-shell
adapter: `Request{Command, Worktree, Limits}` in, `Result{Stdout, Stderr, Duration}` plus
exit code out. ADR 021 then made the repo-owned Podman launcher
(`internal/sandbox/podman` driving `containment/execution-box/run.sh`) the sole default
containment backend. Both decisions reflected a world where the exec-sandbox *block* was
not yet wired in — agent-builder ran inside isolation it shelled out to itself.

The exec-sandbox block (`~/Code/Public/exec-sandbox`, commit `ab03804`) now ships a real
`run()` with enforced `profile.limits` on bubblewrap and gVisor (its ADR-003), behind a
first-class `tier` seam, with vault-injection hooks and audit emission. agent-builder is
the block's intended first real consumer — the project's north star — so the default run
backend should now be the block, not the in-repo launcher.

## Decision

**Adopt the shipped exec-sandbox block binary as agent-builder's default `run()` backend.**
The in-repo Podman launcher (`internal/sandbox/podman` + `containment/execution-box/run.sh`)
is demoted to a still-selectable fallback — **not deleted**. Deleting it is a separate
Ask-first decision once the block proves capstone parity.

**1. New backend package `internal/sandbox/execsandbox` implements `sandbox.Runner`.**
It marshals the typed `Request`/`Limits` into a `RunRequest` JSON, execs the block binary,
parses the JSON result back into `Result` plus exit code, and surfaces `sandbox_status`.
The `sandbox.Runner` interface is unchanged; this is a third concrete backend behind it.

**2. The block's stdin/stdout contract is the new seam agent-builder speaks.**
Invocation is `exec-sandbox run`, reading a JSON `RunRequest` on stdin and writing a JSON
result on stdout. Process exit `0` = *handled* (the payload's own exit lives in
`result.exit_code`); `1` = stdin/JSON error; `2` = usage error. The shapes:

- `RunRequest = { "run": { "payload": <shell script string>, "profile": { "capabilities": [{"type":"NetConnect","allowlist":["host:port",...]}], "limits": {"cpu_count","memory_mb","pids","disk_mb","timeout_sec"} }, "tier": "bubblewrap"|"gvisor", "secret_refs": [<handle>,...] }, "wiring": { "vault_socket", "audit_socket", "origin_map", "request_id", "injection_mode":"proxy"|"env"|"" } }`
- `Result = { "stdout", "stderr", "exit_code", "sandbox_status": {"sandbox_id","tier","duration_ms","secrets_injected":[{"handle_prefix","delivery"}],"status":"clean"|"timeout","limits":{...,"degraded":[...]}} }` — or `{"error": <str>}` on early failure.

**3. Field mapping from the typed seam to `RunRequest`:**
`MemoryBytes → memory_mb`, `CPUCount → cpu_count`, `PidsLimit → pids`,
`WallClockTimeout → timeout_sec`, `EgressAllowlist → profile.capabilities[NetConnect].allowlist`.
`tier` becomes first-class in the seam — today it is selected out-of-band by `run.sh`
env (ADR 016). `disk_mb` is a new limit field agent-builder may set or leave `0`.
`tier` `"bubblewrap"` (default) and `"gvisor"` are implemented; `firecracker` hard-errors
("tier not implemented").

**4. The block binary is a runtime dependency, located by config and failing loud.**
Its path is config-provided; it is built with `go build -o bin/exec-sandbox ./...` in the
exec-sandbox repo. A missing or unbuilt binary, an unknown/unavailable `tier`, and the
block's `{"error": ...}` response all **fail loud** — never a silent fallback to the
launcher. This is consistent with the no-silent-fallback invariant (ADR 021's
`fitness-no-srt` lineage, the fail-fast principle).

**5. Vault and audit wiring are plumbed but empty — deferred, additive future work.**
Because the vault block is still WIP, `secret_refs` and `wiring.vault_socket` are sent
**empty** and `injection_mode` is `""`. `wiring.audit_socket` is also empty:
agent-builder's existing audit-trail consumption (ADR 026) is unchanged and out of scope
here. Wiring these in is additive and follows in later tasks; nothing in this change
depends on them.

## Consequences

- agent-builder becomes the first real consumer of the shipped exec-sandbox block — the
  north-star transition from *builds the blocks* toward *built on the blocks*.
- The Phase-0 end-to-end capstone must be **re-verified on the block backend** before this
  task is marked ✅; the launcher path remains for fallback and side-by-side comparison
  during that verification.
- The launcher (`internal/sandbox/podman` + `containment/execution-box/run.sh`) stays in
  the tree as a selectable fallback. Its deletion is a separate Ask-first decision gated
  on capstone parity, mirroring how ADR 021 left `sandboxruntime` importable.
- In-box verification-gate semantics (the gate run *inside* the box) are unchanged.
- One external runtime dependency is added: the built block binary must be present on the
  host and discoverable via config, or the default run path fails loud at startup.
- `tier` graduates from an out-of-band `run.sh` env selection to a first-class field on the
  run seam; ADR 016's tier defaults remain the operator-facing contract for the launcher
  fallback.
