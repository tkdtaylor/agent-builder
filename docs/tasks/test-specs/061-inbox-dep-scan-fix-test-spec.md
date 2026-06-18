# Test spec — Task 061: fix in-box dep-scan gate step

## Context

The production gate's `DepScanStep` ran `gods` with no arguments. `gods` is a `go` drop-in wrapper:
with no package args it scans nothing and `exec go` (no args) → prints Go's help → exits non-zero.
So the step crashed on tooling misuse. This was latent: host `make check` never runs dep-scan, and
the step's tests used a fake `gods` that always exited 0. It surfaced when the real gate ran in-box
during the live capstone (`PASS build/vet/test/gofmt/golangci-lint; FAIL gods`). agent-builder has no
third-party deps (no `go.sum`), so there is nothing to scan. See ADR 034.

The fix: `DepScanStep` calls `dep-scan` directly; a module with no `go.sum` passes without invoking
the scanner; a module with `go.sum` is scanned with the correct arguments.

## Test cases

### TC-061-01 — module with no go.sum passes without invoking the scanner
- **Assertion:** `DepScanStep.Run` on a repoPath containing `go.mod` but **no `go.sum`** returns
  `OK == true`, and the scanner binary is **not** invoked (verified: passes even when no `dep-scan`
  is on PATH — it is not a missing-tool failure). File: `internal/gate/dep_scan_step_test.go`.

### TC-061-02 — module with go.sum invokes dep-scan with the correct arguments
- **Assertion:** with a `go.sum` present and a fake `dep-scan` on PATH, `DepScanStep.Run` invokes
  `dep-scan check --registry go --lockfile go.sum --lockfile-type go` in repoPath. The fake records
  its argv; the test asserts the exact argument vector and that `cmd.Dir == repoPath`.

### TC-061-03 — high-severity finding fails the step with captured output
- **Assertion:** with `go.sum` present and a fake `dep-scan` that prints a finding and exits 1,
  `DepScanStep.Run` returns `OK == false` and the captured output contains the finding text.

### TC-061-04 — go.sum present but dep-scan missing is a hard failure
- **Assertion:** with `go.sum` present and an empty PATH, `DepScanStep.Run` returns `OK == false`
  with output naming `dep-scan` and "missing tool" — a configuration error, not a silent pass.

### TC-061-05 — box toolchain contract requires dep-scan
- **Assertion:** `containment/execution-box/run.sh` `required_mounted_gate_tools` lists `dep-scan`
  (replacing `gods`); `--print-toolchain-plan` names `dep-scan`. Verified via the run.sh toolchain
  test or a direct `--print-toolchain-plan` assertion.

### TC-061-06 — gate green
- **Assertion:** `go test ./...` + `make check` pass.

### TC-061-07 — live in-box gate clears dep-scan (L6, operator/observed)
- **Assertion:** the live capstone's in-box gate now reports `PASS gods`/dep-scan (no `FAIL`) and
  proceeds to the next step. Recorded as operator observation; **stays pending until run.**

## Verification plan

- **Highest level achievable in-repo:** L5 — `go test ./...` + `make check` green (TC-061-01..06).
- **L6 (observed):** the live capstone clears the dep-scan gate step (TC-061-07) — the next step in
  the chain toward a fully-green capstone.

## Out of scope

- Egress allowlist for `dep-scan`'s CVE backend when a scanned module has dependencies (deferred per
  ADR 034 — agent-builder has none).
- The `code-scanner` gate step (the next link in the chain — its own task if it also breaks in-box).
- Removing the `gods` wrapper from gate-tools (it stays for interactive use).
