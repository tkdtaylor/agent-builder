# Task 068: checkpoint signer seam, config, and creation at seal

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** ✅ (verified)

## Goal

Extend `internal/audit` with a `CheckpointSigner` type that wraps the `audit-trail
checkpoint create` CLI subprocess (reusing the `ExecRunner` seam pattern from `BlockSink`).
Add four new `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env vars to `ConfigFromEnv` with fail-fast
validation that mirrors `resolveAuditBin`/`requireWritable`. Wire the supervisor to call
`CreateCheckpoint` at seal time (after `VerifyChain` passes) on the success path.

Unit-tested with a fake `ExecRunner`; updates `docs/spec/configuration.md`. No new runtime
surface beyond the existing audit-trail binary.

## Context

### What already exists

`internal/audit` (tasks 038–042) ships:
- `BlockSink` — calls `audit-trail emit` per event via `ExecRunner`.
- `VerifyChain` / `VerifyChainWithRunner` — calls `audit-trail verify` via `ExecRunner`.
- `ExecRunner` interface — the subprocess seam reused by both.
- `internal/runtime/run.go` `ConfigFromEnv` — reads `AGENT_BUILDER_AUDIT_RECORD` and
  `AGENT_BUILDER_AUDIT_BIN`; runs `resolveAuditBin` (fail-fast on missing binary) and
  `requireWritable` (fail-fast on unwritable logfile path).
- Supervisor `WithSink(sink audit.Sink)` option — injects the `BlockSink` before dispatch.

### What this task adds

**`internal/audit/checkpoint.go`** — `CheckpointSigner` type:
```go
type CheckpointSigner struct {
    logfile        string
    logID          string
    signingKeyPath string
    outPath        string   // empty = stdout
    runner         ExecRunner
}

func NewCheckpointSigner(binPath, logfile, logID, signingKeyPath, outPath string) *CheckpointSigner
func NewCheckpointSignerWithRunner(logfile, logID, signingKeyPath, outPath string, runner ExecRunner) *CheckpointSigner
func (c *CheckpointSigner) CreateCheckpoint() error
```

`CreateCheckpoint` builds the argv:
```
["checkpoint", "create", "--logfile", <logfile>, "--log-id", <logID>,
 "--signing-key", <signingKeyPath>] + (["--out", <outPath>] if outPath != "")
```
and delegates to `runner.Run(args)`. A non-zero runner exit returns a non-nil error.

**Config surface** (`internal/runtime/run.go`):

Four new env vars (names decided in ADR 037):
- `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` — path to PEM Ed25519 signing key (required when
  checkpoint is enabled).
- `AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID` — stable log identifier for the checkpoint
  (required when checkpoint is enabled; the ADR decides the required vs optional semantics).
- `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` — path where the checkpoint JSON is written
  (optional; empty = stdout, but stdout is typically not useful in production).
- `AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY` — path to PEM Ed25519 public key for the
  verify surface (consumed by task 069; stored in `Config` by this task).

Fail-fast validation (runs before dispatch, same as `resolveAuditBin`/`requireWritable`):
- `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY` set but file does not exist → non-nil error naming
  the path and the env var.
- `AGENT_BUILDER_AUDIT_BIN` can not be resolved (already checked by existing code; no
  separate check needed when checkpoint is enabled).
- `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` set: verify the parent directory exists and is
  writable.
- When none of the checkpoint env vars are set: checkpoint is disabled (opt-in). No
  `CheckpointSigner` is constructed; run proceeds exactly as before.

**Supervisor wiring** (`internal/supervisor/`):

A new option `WithCheckpointSigner(cs *audit.CheckpointSigner)` (or equivalent) injects
the signer. The success path of `Run` calls `cs.CreateCheckpoint()` after:
1. `Seal()` is called on the `audit.Sink`.
2. `VerifyChain` returns `IsTampered() == false` (Valid == true).

If `CreateCheckpoint()` returns an error: log the error; do NOT abort teardown; do NOT
change the run outcome. The chain is already sealed and verified — the checkpoint is
forensic metadata, not a gate condition on the current run.

If the checkpoint signer is nil (checkpoint disabled): skip silently.

### The `IsTampered()` guard before `CreateCheckpoint`

The supervisor must NOT call `CreateCheckpoint` when `VerifyChain` returns a tampered
result. A signed checkpoint over a tampered chain would be a false attestation. The
ordering is: seal → verify → (only if valid) → create-checkpoint.

### Leaf-package invariant

`internal/audit` must remain a stdlib-only leaf. `CheckpointSigner` must import only
stdlib packages. The F-005 fitness check (`make fitness-audit-isolation`) enforces this;
`go list -deps ./internal/audit/...` must contain no `agent-builder/internal/` paths.

## Requirements

| Req ID     | Description                                                                                                                                                                                                           | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-068-01 | `internal/audit/checkpoint.go` implements `CheckpointSigner` with `NewCheckpointSigner`, `NewCheckpointSignerWithRunner`, and `CreateCheckpoint`. `CreateCheckpoint` builds the correct `checkpoint create` argv (including `--out` only when outPath is non-empty) and delegates to `ExecRunner`. A non-zero runner exit returns a non-nil error. | must have |
| REQ-068-02 | `internal/runtime/run.go` `ConfigFromEnv` reads four `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env vars into `Config`. When the signing key env var is set: `resolveCheckpointConfig` fails fast (before dispatch) on a missing key file, unwritable output directory, or unresolvable binary — mirroring `resolveAuditBin`/`requireWritable`. When none are set: checkpoint is disabled, run unchanged. | must have |
| REQ-068-03 | The supervisor success path calls `cs.CreateCheckpoint()` after `Seal()` and after `VerifyChain` returns valid. When `VerifyChain` returns tampered, `CreateCheckpoint` is NOT called. When the checkpoint signer is nil, the call is skipped silently. | must have |
| REQ-068-04 | `CreateCheckpoint` failure is logged but does not block teardown or change the run outcome. `TestPhase0EndToEndAcceptance` (fake-provider) passes with checkpoint disabled. `go list -deps ./internal/audit/...` shows no `agent-builder/internal/` paths (leaf invariant). | must have |
| REQ-068-05 | `make check` exits 0. `docs/spec/configuration.md` documents all four `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env vars (type, default, required, effect) in the same commit as the code. | must have |

## Readiness gate

- [ ] Test spec `068-checkpoint-signer-seam-test-spec.md` exists (written first — already done)
- [ ] Task 067 (ADR-037) merged and human-approved — the ADR establishes the env var names
      and the behavioral contract that this task implements
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-068-01] TC-068-01: `CheckpointSigner` shape compiles; constructors exist
- [ ] [REQ-068-01] TC-068-02: `CreateCheckpoint` builds correct argv; handles `outPath=""` (no `--out`); returns non-nil error on runner failure
- [ ] [REQ-068-02] TC-068-03: `ConfigFromEnv` reads four checkpoint env vars correctly
- [ ] [REQ-068-02] TC-068-04: Fail-fast pre-dispatch for missing key file, unwritable output dir, or unresolvable binary
- [ ] [REQ-068-03] TC-068-05: Supervisor calls `CreateCheckpoint` after Seal + VerifyChain; not called when tampered; skipped when signer nil
- [ ] [REQ-068-04] TC-068-06: Checkpoint failure logged, teardown and run outcome unaffected
- [ ] [REQ-068-04] TC-068-07: All existing tests pass; leaf invariant confirmed
- [ ] [REQ-068-05] TC-068-08: `make check` green; `configuration.md` updated with all four env vars in same commit

## Verification plan

- **Highest level achievable:** L5 — unit tests + `make check` green. No new runtime
  surface; the real `audit-trail checkpoint create` binary is not required for L5.
- **L6 with real binary** (gated on `AGENT_BUILDER_LIVE_AUDIT=1`):
  ```
  AGENT_BUILDER_LIVE_AUDIT=1 \
  AGENT_BUILDER_AUDIT_BIN=$HOME/Code/Public/audit-trail/audit-trail \
  go test -count=1 -v ./internal/audit/... -run TestCheckpointSignerRealBinary
  ```
  Expected: checkpoint JSON created at `--out` path; `make check` → `All checks passed.`
- **Harness command (L5):**
  ```
  go test -count=1 ./internal/audit/... ./internal/runtime/...
  go list -deps ./internal/audit/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check `PASS-leaf`; e2e test `PASS`;
  final line `All checks passed.`

## Out of scope

- The `agent-builder verify-checkpoint` CLI subcommand (task 069).
- `audit-trail checkpoint verify` invocation (task 069).
- Rekor anchoring (`checkpoint anchor` / `verify-anchor`).
- Vault key brokering (a future follow-on named in ADR 037).
- Changing `BlockSink`, `VerifyChain`, or existing audit code.
- Updating `docs/spec/interfaces.md` (task 069).

## Dependencies

- Task 067 (ADR-037) — must be merged and human-approved; the ADR defines the env var
  names and the behavioral contract this task implements.
- Sequenced **after** vault tasks 064–066 per the 2026-06-19 roadmap decision.
- Task 069 (verify-checkpoint surface) depends on this task.
