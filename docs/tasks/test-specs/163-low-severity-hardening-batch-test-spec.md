# Test Spec 163: low-severity hardening batch (audit logfile perms, Telegram envelope role assertion, dead sentinel)

**Linked task:** [`docs/tasks/backlog/163-low-severity-hardening-batch.md`](../backlog/163-low-severity-hardening-batch.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

Three independent, low-severity findings from the full-project review, batched into
one task because each is a small, self-contained hardening fix with no shared code
path between them, and each individually is too small to justify its own task/spec
pair:

1. **Audit logfile permissions too permissive.** `requireWritable`
   (`internal/runtime/run.go:1034-1044`) opens the audit chain logfile path with
   `os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)` — world/group
   readable. The audit chain records action-level detail (task IDs, branch names,
   redacted-but-still-sensitive routing/publish info); `0o644` allows any local user
   on the host to read it. Fix: `0o600` (owner read/write only), matching the
   `authz.Store`'s existing `0600` convention (`internal/channel/telegram/authz/authz.go`).

2. **Telegram inbound envelope role is never asserted.** After
   `envelope.VerifyAndOpen` succeeds on the Telegram inbound path
   (`internal/channel/telegram/adapter.go:307-330`), the adapter never checks
   `env.From`/`env.To` against the expected roles (`"operator"`/`"orchestrator"`) —
   unlike `internal/channel/worker/transport.go`'s `Receiver.verifyOpen`
   (lines 250-268), which asserts `env.From != r.expectFrom || env.To != r.expectTo`
   immediately after `VerifyAndOpen` succeeds (task 098 SEC-001: "do not rely solely
   on key separation"). The Telegram adapter relies solely on key separation for role
   correctness today — a defense-in-depth gap task 098 already closed for the worker
   transport but never mirrored on the Telegram inbound leaf.

3. **Dead sentinel `audit.ErrBlockEmitFailed`.** `internal/audit/blocksink.go:112-115`
   declares `var ErrBlockEmitFailed = errors.New("audit: block emit failed")` with a
   doc comment claiming "Callers can use errors.As to distinguish block failures from
   validation errors," but the actual emit-failure return
   (`internal/audit/blocksink.go:217-219`, `fmt.Errorf("audit: emit %s failed: %w",
   ev.Action, err)`) never wraps this sentinel — `errors.Is`/`errors.As` against
   `ErrBlockEmitFailed` can never match anything the package actually returns. Fix:
   wrap the emit-failure return with `%w` around `ErrBlockEmitFailed` so the sentinel
   becomes real and matchable, matching the doc comment's own claim (the smaller,
   more useful fix versus deleting the sentinel and its now-inaccurate comment).

**Module boundaries touched:** `internal/runtime` (item 1), `internal/channel/telegram`
(item 2), `internal/audit` (item 3) — three genuinely independent one-line-to-few-line
fixes, each in its own package, none interacting with the others.

---

## Requirements coverage

| Req ID     | Description                                                                                                    | Test cases            |
|------------|----------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-163-01 | The audit chain logfile is opened/created with mode `0o600`, not `0o644` | TC-163-01               |
| REQ-163-02 | An audit logfile CREATED by a prior version of the code (already on disk with looser permissions) is not silently left loose by `requireWritable`'s `O_APPEND` open — the fix at minimum applies `0o600` to newly-created files; document (and, if easy, also apply via `os.Chmod`) the fix for the pre-existing-file case | TC-163-02               |
| REQ-163-03 | The Telegram inbound adapter asserts `env.From == "operator" && env.To == "orchestrator"` immediately after a successful `VerifyAndOpen`, mirroring `worker/transport.go`'s `verifyOpen` role-assertion pattern | TC-163-03               |
| REQ-163-04 | A role-mismatched (but otherwise validly signed/decrypted) envelope on the Telegram inbound path is rejected with a distinct, audited reason (e.g. `role_mismatch`, matching the worker transport's existing reason string) rather than silently accepted or misclassified as a generic rejection | TC-163-04               |
| REQ-163-05 | `audit.ErrBlockEmitFailed` is wrapped (`%w`) around the block subprocess emit-failure return, so `errors.Is(err, audit.ErrBlockEmitFailed)` is `true` for a real emit failure | TC-163-05               |
| REQ-163-06 | Pre-existing `internal/runtime`, `internal/channel/telegram`, `internal/channel/worker`, `internal/audit` suites continue to pass unchanged aside from the specific assertions this task adds/updates | TC-163-06               |

---

## Pre-implementation checklist

- [x] Task 159 merged (this task's item 2 touches `internal/channel/telegram/adapter.go`,
  landing after the sequenced Telegram fix set 157-159 to avoid merge conflicts)
- [ ] `make check` green before branching

---

## Test cases

### TC-163-01 — Audit logfile is created with mode 0600

- **Requirement:** REQ-163-01
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go` (extend the existing `requireWritable` coverage, if any, or add one)

**Setup:** A fresh temp path (file does not exist).

**Step:** Call `requireWritable(path)`.

**Expected output:** The file exists afterward with mode `0600` (verified via
`os.Stat(path).Mode().Perm() == 0o600`), not `0o644`.

---

### TC-163-02 — Pre-existing looser-permission file handling is documented/addressed

- **Requirement:** REQ-163-02
- **Level:** L2 (unit test)
- **Test file:** `internal/runtime/run_test.go`

**Setup:** A file pre-created at `0o644` before calling `requireWritable`.

**Step:** Call `requireWritable(path)`.

**Expected output:** Either (a) the implementation additionally `os.Chmod`s the path
to `0o600` when it already exists (preferred — verify the resulting mode is `0o600`),
or (b) the task's implementation notes explicitly document that only NEWLY created
files get the tightened mode and an operator upgrading from a prior deployment must
`chmod 600` any pre-existing chain file manually (acceptable IF documented, not
acceptable as an unstated gap). This test asserts whichever choice is made,
consistently.

---

### TC-163-03 — Telegram inbound asserts the envelope role

- **Requirement:** REQ-163-03
- **Level:** L2 (unit test, mirrors the worker transport's role-assertion tests, e.g. TC-098-xx)
- **Test file:** `internal/channel/telegram/adapter_test.go` or a new `adapter_163_test.go`

**Setup:** Construct a validly signed, replay-fresh, correctly-decryptable envelope
whose `From`/`To` fields do NOT match `"operator"`/`"orchestrator"` (e.g. swapped, or
an arbitrary wrong string) — everything else about the envelope is valid.

**Step:** Feed it to the adapter's stub server; call `Next()`.

**Expected output:** `Next()` returns `(supervisor.Message{}, false, nil)` for that
update (rejected, adapter re-polls per task 157) — the mismatched-role envelope is
NOT accepted as a message despite passing `VerifyAndOpen`.

---

### TC-163-04 — Role mismatch produces a distinct audited reason

- **Requirement:** REQ-163-04
- **Level:** L2 (unit test, same harness as TC-163-03, stub `audit.Sink`)
- **Test file:** `internal/channel/telegram/adapter_163_test.go`

**Step:** Same setup as TC-163-03; inspect the stub audit sink's recorded event.

**Expected output:** Exactly one rejection event with a distinct reason (e.g.
`"role_mismatch"`, matching `worker/transport.go`'s existing reason string for the
same underlying check) — not the generic envelope-rejected fallback, and not silently
un-audited.

---

### TC-163-05 — `ErrBlockEmitFailed` is now a real, matchable sentinel

- **Requirement:** REQ-163-05
- **Level:** L2 (unit test, mirrors `internal/audit/blocksink_test.go`'s existing emit-failure coverage)
- **Test file:** `internal/audit/blocksink_test.go`

**Setup:** A fake block `runner.Run` that returns a non-nil error (simulating a
subprocess failure).

**Step:** Call `sink.Append(ev)`.

**Expected output:** The returned error satisfies `errors.Is(err,
audit.ErrBlockEmitFailed)` — `true` — where it was `false` before this task (the
pre-task `fmt.Errorf` call had no `%w` around the sentinel). The existing descriptive
message text (`"audit: emit %s failed: ..."`) is preserved.

---

### TC-163-06 — Full regression

- **Requirement:** REQ-163-06
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/runtime/... ./internal/channel/telegram/... ./internal/channel/worker/... ./internal/audit/...
make check
```

**Expected output:** All packages `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L2/L3 — all three fixes are small, unit-testable
  changes with no runtime-observable surface beyond file permissions (checked via
  `os.Stat` in-process) and audit-sink assertions (already the established L2 pattern
  for this class of finding, e.g. task 154). No L5/L6 required.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/runtime/... ./internal/channel/telegram/... ./internal/audit/...
  ```
  Expected: all TC-163-01..05 pass.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Any change to `Seal`'s/`VerifyAndOpen`'s core cryptographic behavior.
- Retroactively `chmod`-ing every audit chain file an operator might already have on
  disk from a prior deployment — covered only to the extent TC-163-02 requires (the
  `requireWritable` call site's own handling of a pre-existing file).
- Any change to the worker transport's OWN role-assertion (`internal/channel/worker/transport.go`)
  — it is already correct; this task only mirrors its pattern onto the Telegram leaf.
