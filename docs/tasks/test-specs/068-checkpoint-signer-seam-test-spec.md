# Test spec — Task 068: checkpoint signer seam, config, and creation at seal

**Linked task:** `docs/tasks/backlog/068-checkpoint-signer-seam.md`
**Written:** 2026-06-19
**Status:** ready

## Context

The audit-trail block ships `audit-trail checkpoint create --logfile <path> --log-id <id>
--signing-key <key.pem> [--out <path.json>]`. This task wires agent-builder to call that
verb at supervisor seal time — after `VerifyChain` passes — whenever checkpoint config is
present.

The design mirrors the existing `BlockSink`/`ExecRunner` pattern precisely:
- A new `CheckpointSigner` type in `internal/audit/` reuses the existing `ExecRunner`
  seam (or a structurally identical one) to shell out to `audit-trail checkpoint create`.
- The supervisor calls `CreateCheckpoint` after `Seal()` and `VerifyChain` succeed, in
  the success path of the run. Checkpoint failure is logged but does not block teardown
  (the chain is already sealed and verified — the checkpoint is forensic metadata, not a
  gate condition on the run itself).
- Config is extended in `internal/runtime/run.go` `ConfigFromEnv` to read four new
  `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env vars. The fail-fast guard pattern mirrors
  `resolveAuditBin`/`requireWritable` exactly.

**Subprocess contract (frozen v1 contract, verified against audit-trail main.go):**

```
audit-trail checkpoint create \
  --logfile <AGENT_BUILDER_AUDIT_RECORD> \
  --log-id  <AGENT_BUILDER_AUDIT_CHECKPOINT_LOG_ID> \
  --signing-key <AGENT_BUILDER_AUDIT_CHECKPOINT_KEY> \
  --out <AGENT_BUILDER_AUDIT_CHECKPOINT_OUT>
```

Exit 0 = checkpoint created; exit non-zero = error (message on stderr). Output is a JSON
file written to `--out`. When `--out` is omitted, the checkpoint JSON goes to stdout.

**When checkpoint is NOT configured** (signing key env var absent or empty): `CheckpointSigner`
is nil; the supervisor skips the `CreateCheckpoint` call; run is unchanged.

**When checkpoint IS configured** (signing key env var set): the four env vars must all be
resolvable before dispatch — missing key file or unexecutable binary → fail fast (same
pattern as `resolveAuditBin`); unwritable output directory → fail fast (same as
`requireWritable`).

## Requirements coverage

| Req ID     | Test cases                    | Covered? |
|------------|-------------------------------|----------|
| REQ-068-01 | TC-068-01, TC-068-02          | yes      |
| REQ-068-02 | TC-068-03, TC-068-04          | yes      |
| REQ-068-03 | TC-068-05                     | yes      |
| REQ-068-04 | TC-068-06, TC-068-07          | yes      |
| REQ-068-05 | TC-068-08                     | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-068-01 — `CheckpointSigner` shape and ExecRunner seam

- **Requirement:** REQ-068-01
- **Level:** L2 (compile-time)
- **Test file:** `internal/audit/checkpoint_test.go`

**Assertions:**
- `internal/audit` package (no new package needed; `CheckpointSigner` lives alongside
  `BlockSink`) compiles with the new type.
- `CheckpointSigner` is a struct with at least `logfile`, `logID`, `signingKeyPath`,
  `outPath` fields and a `runner ExecRunner` field.
- A constructor `NewCheckpointSigner(binPath, logfile, logID, signingKeyPath, outPath string) *CheckpointSigner`
  exists and builds the struct with an `emitRunner`-equivalent runner.
- A constructor `NewCheckpointSignerWithRunner(logfile, logID, signingKeyPath, outPath string, runner ExecRunner) *CheckpointSigner`
  exists for test injection.
- A `CreateCheckpoint() error` method exists on `*CheckpointSigner`.
- `go build ./internal/audit/...` exits 0.

---

### TC-068-02 — `CreateCheckpoint` builds the correct argv and delegates to ExecRunner

- **Requirement:** REQ-068-01
- **Level:** L5 (unit test with fake `ExecRunner`)
- **Test file:** `internal/audit/checkpoint_test.go`

**Setup:** construct a `CheckpointSigner` via `NewCheckpointSignerWithRunner` with a
recording fake `ExecRunner` that captures `args` and returns `(nil, nil)` (success).

**Test case — all fields set:**
- Input: `logfile="/tmp/audit.log"`, `logID="prod-001"`, `signingKeyPath="/tmp/key.pem"`,
  `outPath="/tmp/checkpoint.json"`
- Expected argv passed to runner:
  `["checkpoint", "create", "--logfile", "/tmp/audit.log", "--log-id", "prod-001", "--signing-key", "/tmp/key.pem", "--out", "/tmp/checkpoint.json"]`
- `CreateCheckpoint()` returns nil.

**Test case — outPath empty (stdout mode):**
- Input: same except `outPath=""`
- Expected argv: `["checkpoint", "create", "--logfile", "/tmp/audit.log", "--log-id", "prod-001", "--signing-key", "/tmp/key.pem"]`
  (no `--out` flag emitted when outPath is empty)
- `CreateCheckpoint()` returns nil.

**Test case — runner returns non-zero:**
- Fake runner returns `([]byte("err"), errors.New("exit status 1"))`
- `CreateCheckpoint()` returns a non-nil error wrapping the runner error.

---

### TC-068-03 — Config reads four `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env vars

- **Requirement:** REQ-068-02
- **Level:** L5 (unit test with env manipulation)
- **Test file:** `internal/runtime/run_test.go`

**Sub-cases (table-driven):**

| Env state | Expected Config field values |
|-----------|------------------------------|
| None of the four checkpoint vars set | All four checkpoint fields empty/zero; no fail-fast error |
| `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY=/tmp/key.pem` only | `Config.AuditCheckpointKey == "/tmp/key.pem"` (other three zero) |
| All four set: KEY, LOG_ID, OUT, PUBLIC_KEY | All four `Config.AuditCheckpoint*` fields reflect the env values |

**Assertion:** `ConfigFromEnv` reads each of the four env vars into typed fields on
`Config`. When none are set, no error and checkpoint signer is nil. When `KEY` is set,
the other three fields (LOG_ID, OUT, PUBLIC_KEY) may be zero without causing an error at
config-parse time (fail-fast happens at dispatch time, not parse time, consistent with
`AGENT_BUILDER_AUDIT_RECORD` behavior).

---

### TC-068-04 — Fail-fast pre-dispatch: unresolvable signing key or binary

- **Requirement:** REQ-068-02
- **Level:** L5 (unit test with filesystem manipulation)
- **Test file:** `internal/runtime/run_test.go` or `internal/audit/checkpoint_test.go`

**Sub-cases:**

| Scenario | Expected behavior |
|----------|-------------------|
| `AGENT_BUILDER_AUDIT_CHECKPOINT_KEY=/nonexistent/key.pem` set, binary resolves | `ConfigFromEnv` or `resolveCheckpointConfig` returns a non-nil error naming the missing key file, before dispatch |
| Checkpoint key set but `AGENT_BUILDER_AUDIT_BIN` points at a non-executable path | Returns a non-nil error naming the binary resolution failure, before dispatch |
| Checkpoint key set, binary resolves, `AGENT_BUILDER_AUDIT_CHECKPOINT_OUT=/nonexistent-dir/cp.json` | Returns a non-nil error: the output directory is not writable |
| No checkpoint vars set | No error; proceed as normal |

**Assertion:** the fail-fast guard runs before any sandbox dispatch is attempted. The error
message names the specific env var or path that failed. These failures do NOT produce a
partial run (the supervisor never starts).

---

### TC-068-05 — Supervisor calls `CreateCheckpoint` at seal, after `VerifyChain` passes

- **Requirement:** REQ-068-03
- **Level:** L5 (unit test using fake ExecRunner and fake VerifyResult)
- **Test file:** `internal/runtime/run_test.go` or `tests/supervisor/`

**Setup:** construct a run with:
- A fake audit chain that produces a verifiable logfile.
- A `CheckpointSigner` with a recording fake `ExecRunner`.
- The supervisor is wired with both the `BlockSink` (via `WithSink`) and the
  `CheckpointSigner` (via a new `WithCheckpointSigner` option or equivalent).

**Assertions:**
- The fake `ExecRunner` for `CreateCheckpoint` is called exactly once after the run
  completes successfully.
- The call happens AFTER `Seal()` is called on the `BlockSink` (ordering: emit events →
  Seal → VerifyChain → CreateCheckpoint).
- The argv passed to the fake runner contains `checkpoint create` and the configured
  `--logfile`, `--log-id`, `--signing-key`, and (when set) `--out`.

**Negative sub-case — checkpoint config absent:**
- When no `CheckpointSigner` is wired, the supervisor completes without calling
  `CreateCheckpoint`. The run outcome is unchanged (exit 0, gate green).

**Negative sub-case — `VerifyChain` fails (tampered chain):**
- The supervisor must NOT call `CreateCheckpoint` when `VerifyChain` returns
  `IsTampered() == true`. The checkpoint must not attest a tampered chain.

---

### TC-068-06 — Checkpoint creation failure does not block teardown or change run outcome

- **Requirement:** REQ-068-04
- **Level:** L5 (unit test)
- **Test file:** `tests/supervisor/` or `internal/runtime/run_test.go`

**Setup:** `CheckpointSigner`'s fake runner returns a non-nil error.

**Assertions:**
- The supervisor logs or surfaces the checkpoint error (does not swallow it silently).
- The run outcome (success/fail/timed-out) is NOT changed by the checkpoint failure.
- Containment teardown proceeds normally.
- `TestPhase0EndToEndAcceptance` (fake-provider) still passes with checkpoint disabled.

---

### TC-068-07 — `go test ./...` exits 0 after the change (no existing test regressions)

- **Requirement:** REQ-068-04
- **Level:** L3 (regression guard)
- **Test file:** all existing tests

**Assertions:**
- `go test ./internal/audit/... ./internal/runtime/...` exits 0.
- `go test ./tests/e2e/... -run TestPhase0EndToEndAcceptance` exits 0 (fake-provider
  capstone unaffected by checkpoint being unconfigured).
- `internal/audit` remains a stdlib-only leaf: `go list -deps ./internal/audit/...`
  contains no `github.com/tkdtaylor/agent-builder/internal/` paths outside the audit
  package itself.

---

### TC-068-08 — `make check` exits 0; `docs/spec/configuration.md` updated in same commit

- **Requirement:** REQ-068-05
- **Level:** L3 / L5
- **Test file:** CI / make

**Assertions:**
- `make check` → `All checks passed.`
- `docs/spec/configuration.md` contains all four `AGENT_BUILDER_AUDIT_CHECKPOINT_*` env
  var names with their type, default, required status, and effect description (same table
  format as existing audit env vars in that file).
- The four env var names in `configuration.md` match exactly the names decided in ADR 037.
- `docs/spec/configuration.md` is committed in the same commit as the Go code changes.

---

## Verification plan

- **Highest level achievable:** L5 — unit tests covering the `CheckpointSigner` seam +
  supervisor wiring + fail-fast guards + `make check` green. No new runtime surface beyond
  the fake ExecRunner.
- **L6 with real binary** (gated on `AGENT_BUILDER_LIVE_AUDIT=1`):
  ```
  AGENT_BUILDER_LIVE_AUDIT=1 \
  AGENT_BUILDER_AUDIT_BIN=$HOME/Code/Public/audit-trail/audit-trail \
  go test -count=1 -v ./internal/audit/... -run TestCheckpointSignerRealBinary
  ```
  Expected: checkpoint JSON file created at configured `--out` path; exit 0.
- **Harness command (L5):**
  ```
  go test -count=1 ./internal/audit/... ./internal/runtime/...
  go list -deps ./internal/audit/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
  go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance
  make check
  ```
  Expected: first command `ok`; leaf check prints `PASS-leaf`; e2e test `PASS`;
  final line `All checks passed.`

## Out of scope

- The `agent-builder verify-checkpoint` CLI subcommand or any operator-facing verify
  surface (task 069).
- `audit-trail checkpoint verify` invocation (task 069).
- Rekor anchoring (`checkpoint anchor` / `verify-anchor`).
- Vault key brokering (a named future follow-on).
- Changing the `BlockSink` or `VerifyChain` code (only new code is added; existing code
  is untouched except for wiring additions in `run.go`).
- Updating `docs/spec/interfaces.md` with the verify-checkpoint surface (task 069).
