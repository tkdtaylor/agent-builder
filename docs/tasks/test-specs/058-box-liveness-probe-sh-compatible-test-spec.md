# Test spec — Task 058: box liveness probe must be /bin/sh-compatible

## Context

The execution-box image is `ENTRYPOINT ["/bin/sh"]`, so any container command is handed to
`/bin/sh` as its arguments. `sandboxBox.Create` ran `Command: ["/bin/true"]` → `sh /bin/true`
→ the shell read the ELF binary as a script (`ELF: not found`) → exit 2, so the box never
started and the live Phase-0 probes (022/028/032) failed at `supervisor: create box`. The bug
was latent because `box.Create` was only ever exercised against L5 **fake** launchers that ran
`exec "$@"` directly (no `/bin/sh` entrypoint), so they never modelled the real image. See ADR 032.

## Test cases

### TC-058-01 — box liveness probe command is sh-compatible
- **Assertion:** `sandboxBox.Create` issues `Command: ["-c", "true"]` (not `["/bin/true"]`).
  Verified through the L5 wiring tests: the fake launcher log shows `cmd=-c true`
  (`tests/cli/run_wiring_test.go` TC-001/TC-036-02).

### TC-058-02 — fake launchers model ENTRYPOINT ["/bin/sh"]
- **Assertion:** the Podman execution-box fake launchers exec the wrapped command **under
  `/bin/sh`** (`exec /bin/sh "$@"`), not `exec "$@"` — so a non-sh-compatible command (the old
  `/bin/true`) would fail in the fakes just as it does against the real image, and the
  sh-compatible `-c true` / `-c "echo hello"` commands pass. Files: `tests/cli/run_wiring_test.go`,
  `tests/e2e/branch_pr_publication_test.go`. (The srt fake in `tests/sandbox` is a different
  contract and is unchanged.)

### TC-058-03 — full pipeline wiring still green
- **Assertion:** `go test ./tests/cli ./tests/e2e ./tests/supervisor` and `make check` pass —
  the run-wiring, publication, capstone (fake-provider), and audit-chain e2e tests that drive
  `box.Create` through the fake launcher all succeed with the new probe command.

### TC-058-04 — real-image box launch (L6, operator/observed)
- **Assertion:** against the real execution-box image, the old `-- /bin/true` form exits
  non-zero (ELF error) and the new `-- -c true` form exits 0.
- **Observed 2026-06-17 (this host, podman 5.7.0 + runsc):**
  `bash containment/execution-box/run.sh --gate-tools … --worktree . -- /bin/true` → exit 2;
  `… -- -c true` → exit 0.

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green (TC-058-01..03).
- **L6 (observed):** real-image launch comparison above (TC-058-04); and the unblocked live
  capstone `TestLivePhase0EndToEndAcceptance_TC032` now proceeds past `box.Create`.

## Out of scope

- The general `sandbox.Runner` command-wrapping contract (callers supply sh-compatible commands
  per ADR 032); a future exec-style option is deferred.
