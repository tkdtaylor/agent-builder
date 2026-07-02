# Task 163: low-severity hardening batch (audit logfile perms, Telegram envelope role assertion, dead sentinel)

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Fix three independent, low-severity findings from the full-project review in one
batched task: tighten the audit chain logfile's permissions from `0o644` to `0o600`,
add the missing `env.From`/`env.To` role assertion on the Telegram inbound envelope
path (mirroring the worker transport's existing pattern), and make
`audit.ErrBlockEmitFailed` a real, matchable sentinel instead of dead code.

## Context

**Three verified findings, batched (each is independent, self-contained, and
individually too small for its own task/spec pair):**

1. **Audit logfile `0o644` → `0o600`.** `requireWritable`
   (`internal/runtime/run.go:1039`) opens the audit chain logfile with
   `os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)` —
   world/group-readable. The chain records action-level detail (task IDs, branch
   names, redacted-but-sensitive routing/publish info). Fix: `0o600`, matching the
   `authz.Store`'s existing `0600` convention.

2. **Telegram inbound envelope role never asserted.** After
   `envelope.VerifyAndOpen` succeeds (`internal/channel/telegram/adapter.go:307-330`),
   the adapter never checks `env.From`/`env.To`. `internal/channel/worker/transport.go`'s
   `Receiver.verifyOpen` (lines 250-268) already asserts
   `env.From != r.expectFrom || env.To != r.expectTo` immediately after
   `VerifyAndOpen` (task 098 SEC-001: "do not rely solely on key separation"). The
   Telegram leaf never adopted this defense-in-depth check. Fix: mirror the same
   assertion (`env.From == "operator" && env.To == "orchestrator"`) on the Telegram
   inbound path, with a distinct audited rejection reason (`role_mismatch`, matching
   the worker transport's string) on failure.

3. **Dead sentinel `audit.ErrBlockEmitFailed`.** `internal/audit/blocksink.go:112-115`
   declares the sentinel with a doc comment claiming callers can `errors.As` against
   it, but the actual emit-failure return (`blocksink.go:217-219`) never wraps it —
   the sentinel can never match anything the package returns. Fix: wrap the
   emit-failure return with `%w` around `ErrBlockEmitFailed`.

**Reference:**
- `internal/runtime/run.go:1034-1044` (`requireWritable`)
- `internal/channel/telegram/adapter.go:307-330` (the `VerifyAndOpen` success path,
  no role check today)
- `internal/channel/worker/transport.go:250-268` (`Receiver.verifyOpen` — the pattern
  to mirror)
- `internal/audit/blocksink.go:112-115, 217-219` (`ErrBlockEmitFailed`, the emit-failure return)
- `docs/tasks/completed/098-*.md` (SEC-001, the original role-assertion precedent)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-163-01 | The audit chain logfile is opened/created with mode `0o600`, not `0o644`. | must have |
| REQ-163-02 | A pre-existing looser-permission file is handled consistently — either tightened via `os.Chmod` at the same call site, or the gap is explicitly documented as an operator upgrade step. | must have |
| REQ-163-03 | The Telegram inbound adapter asserts `env.From == "operator" && env.To == "orchestrator"` immediately after a successful `VerifyAndOpen`, mirroring the worker transport's pattern. | must have |
| REQ-163-04 | A role-mismatched envelope is rejected with a distinct audited reason (`role_mismatch`), not silently accepted or generically misclassified. | must have |
| REQ-163-05 | `audit.ErrBlockEmitFailed` is wrapped (`%w`) around the emit-failure return, so `errors.Is` against it succeeds for a real emit failure. | must have |
| REQ-163-06 | Pre-existing suites across the three touched packages continue to pass unchanged aside from this task's additions. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/163-low-severity-hardening-batch-test-spec.md` exists (written first)
- [x] Task 159 merged (this task's Telegram edit lands after the sequenced 157-159 set,
  same file, to avoid merge conflicts)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-163-01] TC-163-01: the audit logfile is created with mode `0600`.
- [ ] [REQ-163-02] TC-163-02: pre-existing looser-permission files are handled consistently (chmod'd or documented).
- [ ] [REQ-163-03] TC-163-03: a role-mismatched Telegram-inbound envelope is rejected despite passing `VerifyAndOpen`.
- [ ] [REQ-163-04] TC-163-04: the rejection is audited with a distinct `role_mismatch` reason.
- [ ] [REQ-163-05] TC-163-05: `errors.Is(err, audit.ErrBlockEmitFailed)` is true for a real emit failure.
- [ ] [REQ-163-06] TC-163-06: `go test -race -count=1 ./internal/runtime/... ./internal/channel/telegram/... ./internal/channel/worker/... ./internal/audit/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L2/L3 — all three fixes are small, unit-testable
  changes (file-permission assertions via `os.Stat`, audit-sink assertions matching
  the established task-097/098/154 pattern). No L5/L6 required.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/channel/telegram/... ./internal/audit/...
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md` — the audit chain logfile path entry (grep
  `AGENT_BUILDER_AUDIT_RECORD` / `requireWritable`) notes the file is created `0600`.
- `docs/spec/interfaces.md` — the `Receiver`/sentinel block (extended by task 154)
  notes the Telegram adapter now also asserts `env.From`/`env.To`, mirroring the
  worker transport's `role_mismatch` classification.
- `docs/spec/data-model.md` — the `audit.EventDetail.Reason` example value list gains
  `"role_mismatch"` for the Telegram leaf (it may already be documented for the worker
  transport — add the Telegram cross-reference if the entry is transport-specific).

## Out of scope

- Any change to `Seal`'s/`VerifyAndOpen`'s cryptographic behavior.
- Retroactively fixing every audit chain file an operator might have on disk from a
  prior deployment, beyond `requireWritable`'s own call-site handling.
- Any change to the worker transport's own (already-correct) role assertion.

## Dependencies

- **Blocks on:** task 159 (lands after the sequenced Telegram fix set 157-159 — same
  `internal/channel/telegram/adapter.go` file).
- **Blocks:** none.
