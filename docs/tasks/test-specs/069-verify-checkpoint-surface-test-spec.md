# Test spec — Task 069: verify-checkpoint CLI surface

**Linked task:** `docs/tasks/backlog/069-verify-checkpoint-surface.md`
**Written:** 2026-06-19
**Status:** ready

## Context

Task 068 wires `audit-trail checkpoint create` into the supervisor seal path, producing a
`SignedCheckpoint` JSON file at `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT`. This task surfaces
`audit-trail checkpoint verify` so that a produced checkpoint can be verified OFFLINE
against the public key — the "survives agent compromise" guarantee.

The surface is an `agent-builder verify-checkpoint` CLI subcommand (under `internal/cli`)
that wraps `audit-trail checkpoint verify --checkpoint <path.json> --public-key <pub.pem>
[--logfile <path>]` via the existing `ExecRunner` subprocess seam pattern.

**Why this is a separate task from 068:**
- 068 touches `internal/audit` + `internal/runtime/run.go` + the supervisor (two modules).
- 069 touches `internal/audit` (new `CheckpointVerifier` type) + `internal/cli` (new
  subcommand). Two different module pairs — task-boundary is clean.
- The verify surface has its own acceptance criteria (exit codes, operator-facing output,
  `interfaces.md` spec update) that are independent of the signer seam.

**Subprocess contract (frozen v1, verified against audit-trail main.go):**

```
audit-trail checkpoint verify \
  --checkpoint <path.json> \
  --public-key <pub.pem> \
  [--logfile <path>]
```

Exit 0 = valid (signature verified, optionally cross-checked against live log).
Exit 1 = invalid (signature bad or chain mismatch).
Exit 2 = usage error (missing required flags).
Stdout: JSON `{"valid": bool, "message": string}` (structure inferred from
`CheckpointVerificationResult` in the block's `checkpoint_signature.go`).

**`agent-builder verify-checkpoint` subcommand contract:**

```
agent-builder verify-checkpoint --checkpoint <path> --public-key <pub.pem> [--logfile <path>]
```

Exit 0 when `audit-trail checkpoint verify` reports valid.
Exit 1 when invalid.
Exit 2 when the binary can't be resolved or required flags are missing (mirrors `agent-builder verify`).

**Existing pattern to follow:** `internal/cli` already has `agent-builder verify` (task 023)
which wraps `audit.VerifyChain`. The `verify-checkpoint` subcommand mirrors that shape.

## Requirements coverage

| Req ID     | Test cases                    | Covered? |
|------------|-------------------------------|----------|
| REQ-069-01 | TC-069-01, TC-069-02          | yes      |
| REQ-069-02 | TC-069-03, TC-069-04          | yes      |
| REQ-069-03 | TC-069-05                     | yes      |
| REQ-069-04 | TC-069-06                     | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-069-01 — `CheckpointVerifier` shape and ExecRunner seam

- **Requirement:** REQ-069-01
- **Level:** L2 (compile-time)
- **Test file:** `internal/audit/checkpoint_test.go` (or `checkpoint_verify_test.go`)

**Assertions:**
- `internal/audit` compiles with a new `CheckpointVerifier` type (alongside `CheckpointSigner`
  from task 068).
- `CheckpointVerifier` has fields: `checkpointPath`, `publicKeyPath`, `logfile` (optional),
  `runner ExecRunner`.
- Constructor `NewCheckpointVerifier(binPath, checkpointPath, publicKeyPath, logfile string) *CheckpointVerifier`
  exists.
- Constructor `NewCheckpointVerifierWithRunner(checkpointPath, publicKeyPath, logfile string, runner ExecRunner) *CheckpointVerifier`
  exists for test injection.
- A `VerifyCheckpoint() (CheckpointVerifyResult, error)` method exists on `*CheckpointVerifier`.
- `CheckpointVerifyResult` has at minimum a `Valid bool` field.
- `go build ./internal/audit/...` exits 0.

---

### TC-069-02 — `VerifyCheckpoint` builds the correct argv and maps the result

- **Requirement:** REQ-069-01
- **Level:** L5 (unit test with fake `ExecRunner`)
- **Test file:** `internal/audit/checkpoint_test.go`

**Sub-case — valid result, logfile set:**
- Fake runner returns `([]byte(`{"valid":true,"message":"signature ok"}`), nil)` with exit 0.
- Input: `checkpointPath="/tmp/cp.json"`, `publicKeyPath="/tmp/pub.pem"`, `logfile="/tmp/audit.log"`
- Expected argv: `["checkpoint", "verify", "--checkpoint", "/tmp/cp.json", "--public-key", "/tmp/pub.pem", "--logfile", "/tmp/audit.log"]`
- `VerifyCheckpoint()` returns `CheckpointVerifyResult{Valid: true}`, nil error.

**Sub-case — invalid result, logfile empty:**
- Fake runner returns `([]byte(`{"valid":false,"message":"signature mismatch"}`), errors.New("exit status 1"))`.
- Input: `logfile=""`
- Expected argv: `["checkpoint", "verify", "--checkpoint", "/tmp/cp.json", "--public-key", "/tmp/pub.pem"]`
  (no `--logfile` flag when logfile is empty)
- `VerifyCheckpoint()` returns `CheckpointVerifyResult{Valid: false}`, nil error.
  (Note: a parseable `valid: false` response is a clean verdict, not an error — mirrors
  `VerifyChain` semantics for tamper detection.)

**Sub-case — binary not found or unparseable output:**
- Fake runner returns `(nil, errors.New("exec: not found"))` with no stdout.
- `VerifyCheckpoint()` returns an error wrapping `ErrVerifierUnavailable` (or a new
  `ErrCheckpointVerifierUnavailable` sentinel). Result is `CheckpointVerifyResult{Valid: false}`.
- Callers must never interpret this as "valid".

---

### TC-069-03 — `agent-builder verify-checkpoint` subcommand dispatches correctly

- **Requirement:** REQ-069-02
- **Level:** L5 (CLI subprocess test with fake binary)
- **Test file:** `tests/cli/verify_checkpoint_test.go`

**Setup:** compile a fake `audit-trail` stub binary that checks `checkpoint verify` in its
argv and returns a configurable exit code + JSON stdout.

**Sub-case — valid checkpoint:**
- Invoke `agent-builder verify-checkpoint --checkpoint /tmp/cp.json --public-key /tmp/pub.pem`
  with the fake binary returning `{"valid":true,"message":"ok"}` exit 0.
- Expected: `agent-builder verify-checkpoint` exits 0; stdout contains `valid` or prints
  the JSON result.

**Sub-case — invalid checkpoint:**
- Fake binary returns `{"valid":false,"message":"bad sig"}` exit 1.
- Expected: `agent-builder verify-checkpoint` exits 1.

**Sub-case — missing required flags:**
- Invoke without `--checkpoint` or without `--public-key`.
- Expected: `agent-builder verify-checkpoint` exits 2 with a usage error message.

**Sub-case — binary not resolvable (AGENT_BUILDER_AUDIT_BIN points at nonexistent path):**
- Expected: exits 2 with an error naming the binary resolution failure.

---

### TC-069-04 — `agent-builder verify-checkpoint` accepts `--logfile` for chain cross-check

- **Requirement:** REQ-069-02
- **Level:** L5 (CLI subprocess test with fake binary)
- **Test file:** `tests/cli/verify_checkpoint_test.go`

**Setup:** fake binary captures the full argv and returns exit 0 + valid JSON.

**Assertion:**
- Invoking `agent-builder verify-checkpoint --checkpoint /tmp/cp.json --public-key /tmp/pub.pem
  --logfile /tmp/audit.log` causes the fake binary to receive `--logfile /tmp/audit.log`
  in its argv (the optional cross-check is threaded through).
- Invoking without `--logfile` does NOT include `--logfile` in the argv passed to the
  binary.

---

### TC-069-05 — `internal/audit` remains a stdlib-only leaf after 069

- **Requirement:** REQ-069-03
- **Level:** L3 (import-graph check)

**Assertion:**
- `go list -deps ./internal/audit/...` contains no `github.com/tkdtaylor/agent-builder/internal/`
  paths.
- The F-005 fitness check (`make fitness-audit-isolation`) exits 0 after the code change.
- `CheckpointVerifier` imports only stdlib packages.

---

### TC-069-06 — `make check` exits 0; `docs/spec/interfaces.md` updated in same commit

- **Requirement:** REQ-069-04
- **Level:** L3 / L5
- **Test file:** CI / make

**Assertions:**
- `make check` → `All checks passed.`
- `docs/spec/interfaces.md` documents the `agent-builder verify-checkpoint` subcommand:
  its flags (`--checkpoint`, `--public-key`, `--logfile`), exit codes (0 valid / 1 invalid
  / 2 error), and the binary resolution via `AGENT_BUILDER_AUDIT_BIN`.
- `docs/spec/interfaces.md` is committed in the same commit as the Go code changes.
- The existing `agent-builder verify` subcommand behavior (task 023) is unaffected:
  `go test ./tests/cli/... -run TestVerifySubcommand` passes.
- `TestPhase0EndToEndAcceptance` passes (no regression from the CLI surface addition).

---

## Verification plan

- **Highest level achievable:** L5 — unit tests for `CheckpointVerifier` + CLI subprocess
  tests for `verify-checkpoint` + `make check` green. No live `audit-trail` binary required
  at L5.
- **L6 with real binary** (gated on `AGENT_BUILDER_LIVE_AUDIT=1`):
  ```
  # 1. Produce a real chain and checkpoint with task 068 wiring
  AGENT_BUILDER_LIVE_AUDIT=1 \
  AGENT_BUILDER_AUDIT_BIN=$HOME/Code/Public/audit-trail/audit-trail \
  go test -count=1 -v ./internal/audit/... -run TestCheckpointSignerRealBinary

  # 2. Verify it with the new subcommand
  ./agent-builder verify-checkpoint \
    --checkpoint /tmp/agent-builder-checkpoint.json \
    --public-key /tmp/agent-builder-pub.pem
  ```
  Expected: exit 0; output confirms `valid: true`.
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

- Rekor anchoring (`checkpoint anchor` / `verify-anchor`) — explicitly out of scope for
  this feature and named as a future follow-up in ADR 037.
- Vault key brokering.
- A TUI or interactive mode for checkpoint verification — the CLI subcommand (exit code +
  JSON stdout) is the full surface.
- Checkpoint rotation or segment-aware verification (handled by the block itself when
  `--logfile` is supplied).
- Changing `BlockSink`, `VerifyChain`, or `CheckpointSigner` (task 068 artifacts).
