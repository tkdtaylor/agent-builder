# Task 069: verify-checkpoint CLI surface

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** ✅ (verified)

## Goal

Surface `audit-trail checkpoint verify` as an `agent-builder verify-checkpoint` CLI
subcommand (under `internal/cli`) so that a checkpoint produced by task 068 can be
verified OFFLINE against the Ed25519 public key. Add a `CheckpointVerifier` type to
`internal/audit` (mirrors the `CheckpointSigner` seam from task 068). Update
`docs/spec/interfaces.md` in the same commit.

## Context

### What task 068 delivers (the prerequisite)

Task 068 wires `audit-trail checkpoint create` into the supervisor seal path, producing a
`SignedCheckpoint` JSON at `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT` when the signing key is
configured. The checkpoint is a portable, Ed25519-signed attestation over the final
verified chain head.

### The gap this task closes

Without a verify surface, the checkpoint is an opaque file. Task 069 gives operators the
`agent-builder verify-checkpoint` command to confirm the signature is valid with only the
public key — the "survives agent compromise" guarantee. Third parties with only the
`AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY` can confirm the chain was intact at run end.

### Subprocess contract

```
audit-trail checkpoint verify \
  --checkpoint <AGENT_BUILDER_AUDIT_CHECKPOINT_OUT path> \
  --public-key <AGENT_BUILDER_AUDIT_CHECKPOINT_PUBLIC_KEY path> \
  [--logfile <AGENT_BUILDER_AUDIT_RECORD path>]
```

Exit 0 = valid; exit 1 = invalid; exit 2 = usage error.
Stdout: JSON `{"valid": bool, "message": string}`.

`--logfile` is optional and enables cross-checking the checkpoint against the live log.
Without it, only the Ed25519 signature over the checkpoint payload is verified (pure
offline verification).

### `CheckpointVerifier` type (`internal/audit/checkpoint.go` or `checkpoint_verify.go`)

```go
type CheckpointVerifyResult struct {
    Valid   bool
    Message string
}

type CheckpointVerifier struct {
    checkpointPath string
    publicKeyPath  string
    logfile        string  // optional; empty = offline-only
    runner         ExecRunner
}

func NewCheckpointVerifier(binPath, checkpointPath, publicKeyPath, logfile string) *CheckpointVerifier
func NewCheckpointVerifierWithRunner(checkpointPath, publicKeyPath, logfile string, runner ExecRunner) *CheckpointVerifier
func (v *CheckpointVerifier) VerifyCheckpoint() (CheckpointVerifyResult, error)
```

`VerifyCheckpoint` builds the argv `["checkpoint", "verify", "--checkpoint", ...,
"--public-key", ...]` (plus `["--logfile", ...]` only when logfile is non-empty),
invokes `runner.Run(args)`, parses the JSON stdout, and maps it to `CheckpointVerifyResult`.

Error semantics mirror `VerifyChain`:
- Parseable `{"valid": false, ...}` response → `CheckpointVerifyResult{Valid: false}`, nil error.
- Unparseable output or binary-not-found → non-nil error wrapping `ErrVerifierUnavailable`
  (or a new `ErrCheckpointVerifierUnavailable` sentinel); `Valid: false`.
- Callers must never treat an error return as "valid".

### `agent-builder verify-checkpoint` subcommand (`internal/cli`)

The existing `internal/cli` package already implements `agent-builder verify` (task 023)
by calling `audit.VerifyChain`. The new subcommand mirrors that shape:

```
agent-builder verify-checkpoint \
  --checkpoint <path>        required: path to the SignedCheckpoint JSON file
  --public-key <path>        required: path to the PEM Ed25519 public key file
  [--logfile <path>]         optional: chain logfile for live cross-check
```

Exit 0 when `VerifyCheckpoint()` returns `Valid: true`.
Exit 1 when `Valid: false`.
Exit 2 on usage error (missing required flags, binary resolution failure, binary path set
via `AGENT_BUILDER_AUDIT_BIN`).

The binary resolution uses the existing `AGENT_BUILDER_AUDIT_BIN` env var (falls back to
`audit-trail` on `$PATH`) — same as `agent-builder verify`.

### Leaf-package invariant

`internal/audit` must remain a stdlib-only leaf after this task. `CheckpointVerifier`
imports only stdlib packages. F-005 fitness check continues to pass.

## Requirements

| Req ID     | Description                                                                                                                                                                                                     | Priority  |
|------------|-----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-069-01 | `internal/audit` gains `CheckpointVerifier` + `CheckpointVerifyResult` + constructors `NewCheckpointVerifier` and `NewCheckpointVerifierWithRunner`. `VerifyCheckpoint()` builds the correct `checkpoint verify` argv (omitting `--logfile` when empty), parses the JSON response, and maps it to `CheckpointVerifyResult`. Error semantics mirror `VerifyChain`: parseable `valid: false` is a clean verdict (nil error); unparseable output or binary failure returns a non-nil error with `Valid: false`. | must have |
| REQ-069-02 | `agent-builder verify-checkpoint` CLI subcommand exists under `internal/cli`. Accepts `--checkpoint` (required), `--public-key` (required), and optional `--logfile`. Exit 0 valid / exit 1 invalid / exit 2 usage error or binary resolution failure. Binary resolved via `AGENT_BUILDER_AUDIT_BIN` (same pattern as `agent-builder verify`). | must have |
| REQ-069-03 | `internal/audit` remains a stdlib-only leaf after this task: `go list -deps ./internal/audit/...` contains no `agent-builder/internal/` paths. `make fitness-audit-isolation` exits 0. | must have |
| REQ-069-04 | `make check` exits 0. `docs/spec/interfaces.md` documents `agent-builder verify-checkpoint` (flags, exit codes, binary resolution) in the same commit as the code. The existing `agent-builder verify` behavior (task 023) is unaffected. | must have |

## Readiness gate

- [ ] Test spec `069-verify-checkpoint-surface-test-spec.md` exists (written first — already done)
- [ ] Task 068 (checkpoint signer seam) merged and verified — `CheckpointSigner` must exist
      before `CheckpointVerifier` is added (both live in `internal/audit`; the seam pattern
      is established by 068)
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-069-01] TC-069-01: `CheckpointVerifier` shape compiles; constructors exist; `CheckpointVerifyResult` type present
- [ ] [REQ-069-01] TC-069-02: `VerifyCheckpoint` builds correct argv; `valid: false` response is clean verdict (nil error); binary failure returns error wrapping unavailable sentinel
- [ ] [REQ-069-02] TC-069-03: `agent-builder verify-checkpoint` exits 0/1/2 correctly with fake binary
- [ ] [REQ-069-02] TC-069-04: `--logfile` flag threads through to binary argv; omitted when flag absent
- [ ] [REQ-069-03] TC-069-05: `go list -deps ./internal/audit/...` shows no internal imports; `make fitness-audit-isolation` passes
- [ ] [REQ-069-04] TC-069-06: `make check` green; `interfaces.md` documents subcommand in same commit; existing `verify` subcommand unaffected

## Verification plan

- **Highest level achievable:** L5 — unit tests for `CheckpointVerifier` + CLI subprocess
  tests for `verify-checkpoint` + `make check` green.
- **L6 with real binary** (gated on `AGENT_BUILDER_LIVE_AUDIT=1`):
  ```
  # Produce a real checkpoint (task 068 wiring must be active)
  AGENT_BUILDER_LIVE_AUDIT=1 \
  AGENT_BUILDER_AUDIT_BIN=$HOME/Code/Public/audit-trail/audit-trail \
  go test -count=1 -v ./internal/audit/... -run TestCheckpointSignerRealBinary

  # Verify offline
  ./agent-builder verify-checkpoint \
    --checkpoint /tmp/agent-builder-checkpoint.json \
    --public-key /tmp/agent-builder-pub.pem
  ```
  Expected: exit 0; operator sees `valid: true` output.
- **Harness command (L5):**
  ```
  go test -count=1 ./internal/audit/... ./tests/cli/...
  go list -deps ./internal/audit/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check `PASS-leaf`; e2e `PASS`;
  final line `All checks passed.`

## Out of scope

- Rekor anchoring (`checkpoint anchor` / `verify-anchor`) — explicitly out of scope in
  ADR 037; named as a future follow-up.
- Vault key brokering.
- A gated checkpoint-verify step in the supervisor (the subcommand is for operator use;
  in-run verification of the checkpoint is not added by this task).
- Updating `docs/spec/configuration.md` (the checkpoint env vars are already documented
  by task 068).
- Changing `BlockSink`, `VerifyChain`, or `CheckpointSigner` (task 068 artifacts).

## Dependencies

- Task 067 (ADR-037) — must be merged and human-approved.
- Task 068 (checkpoint signer seam) — must be merged; establishes `CheckpointSigner`,
  the `ExecRunner` reuse pattern for checkpoint verbs, and the `AGENT_BUILDER_AUDIT_CHECKPOINT_*`
  env var names that this task's subcommand relies on.
- Sequenced **after** vault tasks 064–066 per the 2026-06-19 roadmap decision.
