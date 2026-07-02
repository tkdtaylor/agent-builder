# Test Spec 159: seed the pairing-mode owner into the approved-sender store at startup

**Linked task:** [`docs/tasks/backlog/159-telegram-pairing-owner-seed.md`](../backlog/159-telegram-pairing-owner-seed.md)
**Written:** 2026-07-02
**Status:** ready for implementation

## Context

`assembleTelegramInbound` (`internal/cli/orchestrate.go:1246-1379`) resolves
`AGENT_BUILDER_TELEGRAM_OWNER_ID` via `assembleTelegramOwnerID`
(`internal/cli/orchestrate.go:1207-1220`) ONLY for `Adapter`'s `ownerID` field — used
by `DecidePairing` to gate the approve/deny grammar and the owner-notification path.
The approved-sender STORE is seeded ONLY from `AGENT_BUILDER_TELEGRAM_APPROVED_IDS`
(`assembleTelegramAuthMode`, `internal/cli/orchestrate.go:1173-1185`) — the owner's own
ID is never added to it.

`DecidePairing` (`internal/channel/telegram/authz/pairing.go:124-152`) step 3 requires
`store.Contains(rawSenderID)` for the OWNER's own plaintext commands (status/info/new-goal
text, anything that is not the approve/deny grammar) to route normally. Since the
owner is never seeded, the owner's own FIRST ordinary command routes through the
stranger "pending" flow exactly like any unknown sender — the owner has to
approve themselves via `approve <own-id>`, a confusing, avoidable onboarding footgun.

The existing test `TestTC152_07_ApproveConsumedButOwnerStatusRoutes`
(`internal/channel/telegram/adapter_152_test.go:409-...`) WORKS AROUND this gap by
pre-seeding the owner's own ID via the test harness's `seededIDs` parameter
(`newPairingHarness(t, srv, 1, storePath, "1")` — passing `"1"`, the owner's own ID, as
a pre-approved seed) rather than exercising the real production seeding path (which
does not exist).

**The fix:** `assembleTelegramInbound` seeds `ownerID` into the approved-sender store
at startup — additively (union, same semantics as the existing
`AGENT_BUILDER_TELEGRAM_APPROVED_IDS` seeding: never removes IDs already on disk) and
persisted (so a restart does not lose it, matching every other store mutation's
`0600`-file durability contract) — whenever the resolved mode is `pairing`. The
workaround in `TestTC152_07` is replaced with a test that exercises the REAL
`assembleTelegramInbound` wiring (owner-seeding happens automatically, with no manual
`seededIDs` pre-population needed for the owner specifically).

**Module boundaries touched:** `internal/cli` (`assembleTelegramInbound`/
`assembleTelegramAuthMode` gain the owner-seeding step) and
`internal/channel/telegram` (the `adapter_152_test.go` workaround is replaced/updated
— test-only change, no production code change in this package, since `Adapter`/
`DecidePairing`/`Store` already correctly consult whatever the store contains; the
gap was purely in what got written to the store at startup).

---

## Requirements coverage

| Req ID     | Description                                                                                                                 | Test cases            |
|------------|----------------------------------------------------------------------------------------------------------------------------------|--------------------------|
| REQ-159-01 | When the resolved auth mode is `pairing`, `assembleTelegramInbound` (or the owner-resolution step it calls) seeds the owner's normalized ID into the approved-sender store BEFORE the adapter is returned, additively (union with any existing/statically-seeded IDs) | TC-159-01               |
| REQ-159-02 | The owner-seed is persisted to the store's `0600` file at startup, so it survives a process restart from the same store path, matching the existing `AGENT_BUILDER_TELEGRAM_APPROVED_IDS` seeding's durability contract | TC-159-02               |
| REQ-159-03 | Non-pairing modes (`envelope`, `disabled`, `allowlist`, `open`) are unaffected — no owner-seeding logic runs, and `allowlist` mode's store is unaffected by an `OWNER_ID` env var that happens to be set but is irrelevant outside pairing mode | TC-159-03               |
| REQ-159-04 | End-to-end through the REAL `assembleTelegramInbound`-constructed `Adapter`: the owner's own FIRST plaintext command (e.g. `status`) routes normally as a `supervisor.Message` WITHOUT any prior `approve`/pending exchange — closing the self-approval footgun | TC-159-04               |
| REQ-159-05 | `TestTC152_07_ApproveConsumedButOwnerStatusRoutes`'s workaround (manually pre-seeding the owner's own ID via `newPairingHarness`'s `seededIDs` parameter) is replaced or supplemented by a test exercising the real production seeding path — the workaround's underlying assumption ("owner is somehow already approved") is now actually true by construction, not by test artifice | TC-159-05               |
| REQ-159-06 | Pre-existing `internal/cli` and `internal/channel/telegram` pairing-mode suites (task 152) continue to pass unchanged for every OTHER assertion | TC-159-06               |

---

## Pre-implementation checklist

- [x] Task 152 merged (pairing mode, `DecidePairing`, the approved-sender `Store`, and
  the `TestTC152_07` workaround this task replaces all already exist)
- [x] Task 158 merged (armor wiring lands first in the sequenced Telegram set — this
  task's edits to `assembleTelegramInbound` come after, to avoid merge conflicts)
- [ ] `make check` green before branching

---

## Test cases

### TC-159-01 — Owner ID is seeded into the store in pairing mode

- **Requirement:** REQ-159-01
- **Level:** L2 (unit test)
- **Test file:** `internal/cli/orchestrate_159_test.go` (new)

**Setup:** Build a full valid pairing-mode env (`tc153FullTelegramEnv`-style helper)
with `AGENT_BUILDER_TELEGRAM_OWNER_ID=1001` and a fresh (non-existent) store path — no
`AGENT_BUILDER_TELEGRAM_APPROVED_IDS` set.

**Step:** Call `assembleTelegramInbound`, then read back the store (via
`authz.NewStore(path).Load()` + `Contains("1001")`, or by inspecting the returned
adapter's behavior per TC-159-04).

**Expected output:** `store.Contains("1001")` is `true` — the owner is seeded even
with NO static `APPROVED_IDS` configured.

---

### TC-159-02 — Owner seed is additive and persisted across a simulated restart

- **Requirement:** REQ-159-02
- **Level:** L2 (unit test, mirrors `TestTC152_08_ApprovalSurvivesSimulatedRestart`'s pattern)
- **Test file:** `internal/cli/orchestrate_159_test.go`

**Setup:** A store path pre-seeded on disk (before the first `assembleTelegramInbound`
call) with one static approved ID (`"555"`, via a direct `authz.Store.Add`+`Persist`
call simulating a prior `APPROVED_IDS` run). Call `assembleTelegramInbound` with
`OWNER_ID=1001` and the SAME store path — this simulates first startup.

**Step:** After the call, construct a FRESH `authz.Store` over the same path (a "process
restart") and `Load()` it.

**Expected output:** The restarted store contains BOTH `"555"` (the pre-existing ID,
untouched — additive/union) AND `"1001"` (the newly-seeded owner) — matching the
existing `APPROVED_IDS` seeding's additive persistence contract exactly.

---

### TC-159-03 — Non-pairing modes are unaffected

- **Requirement:** REQ-159-03
- **Level:** L2 (unit test, one sub-test per non-pairing mode)
- **Test file:** `internal/cli/orchestrate_159_test.go`

**Step:** Call `assembleTelegramInbound` for each of `envelope`, `disabled`,
`allowlist`, `open` — including a case where `AGENT_BUILDER_TELEGRAM_OWNER_ID` happens
to be set in the env (irrelevant/ignored outside pairing mode, per
`assembleTelegramOwnerID`'s existing (0, nil) short-circuit) alongside `allowlist`
mode's own `APPROVED_IDS`.

**Expected output:** For `envelope`/`disabled`, no store is built at all (unchanged:
`store == nil`). For `allowlist`, the store contains ONLY the `APPROVED_IDS`-seeded
entries — no owner-seeding logic ran (the env's stray `OWNER_ID` value, if set, never
reaches the store).

---

### TC-159-04 — End-to-end: the owner's first plaintext command routes without a pending/approve exchange

- **Requirement:** REQ-159-04
- **Level:** L5 (real `Adapter` constructed via `assembleTelegramInbound`, scripted stub Telegram server)
- **Test file:** `internal/cli/orchestrate_159_test.go` or `internal/channel/telegram/adapter_159_test.go`

**Setup:** Build a real `Adapter`/`ReplyAdapter` via `assembleTelegramInbound` with
`AUTH_MODE=pairing`, `OWNER_ID=1001`, and a FRESH store path (no pre-seeding of any
kind — this is the critical difference from task 152's `TestTC152_07` workaround).
Scripted stub server: one update, sender ID `1001` (the owner), text `"status"`.

**Step:** Call `adapter.Next()`.

**Expected output:** `Next()` returns `(msg, true, nil)` with `msg.Kind ==
supervisor.MsgStatus` — the owner's very first command routes normally. NO
`pairing_request` audit event fires, NO "pending" notification is sent to the owner,
and the owner did not need to send `approve 1001` first. Contrast: on the PRE-159
code, this exact scenario would produce `ActionPairingPending` (the owner treated as
an unknown stranger) — this test fails against the old code and passes against the fix.

---

### TC-159-05 — `TestTC152_07`'s workaround is replaced with real-wiring coverage

- **Requirement:** REQ-159-05
- **Level:** L2 (test-file diff review + regression run)
- **Test file:** `internal/channel/telegram/adapter_152_test.go`

**Step:** Locate `TestTC152_07_ApproveConsumedButOwnerStatusRoutes`'s
`newPairingHarness(t, srv, 1, storePath, "1")` call (the `"1"` seeded-ID workaround).
Either (a) remove the manual owner pre-seed from this specific test's harness call and
rely on the SAME production seeding path TC-159-04 exercises (if
`newPairingHarness` is refactored to go through `assembleTelegramInbound`-equivalent
wiring), or (b) leave `adapter_152_test.go`'s package-level harness as a lower-level
`telegram`-package-only fixture (which cannot itself call `internal/cli`'s
`assembleTelegramInbound` without an import cycle) and add an explicit comment noting
that TC-159-04 (in `internal/cli`) is now the test that exercises the REAL
production seeding path end-to-end, while this package-level test intentionally keeps
constructing its store pre-seeded to isolate the ordering property under test
(unchanged from task 152's original intent) — whichever approach is taken, this task's
diff removes the misleading comment text ("no: the owner must be approved to route
'status'... instead seed the owner as approved... this is contrived") since it is
contrived ONLY because production doesn't do it — post-fix, the comment must state
plainly that production seeds this automatically and the manual seed here is a
package-level test convenience, not a workaround for a real gap.

**Expected output:** `TestTC152_07_ApproveConsumedButOwnerStatusRoutes` still passes;
its comment no longer describes the pre-seed as a workaround for a real gap (the gap
is fixed); TC-159-04 in `internal/cli` is the test of record for "does production
actually seed the owner."

---

### TC-159-06 — Full regression: pairing-mode suites pass unchanged elsewhere

- **Requirement:** REQ-159-06
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/...
make check
```

**Expected output:** All packages `ok`; every other task-152 assertion
(`TestTC152_05_StrangerCannotSelfApprove`, `TestTC152_08_ApprovalSurvivesSimulatedRestart`,
etc.) continues to pass unchanged. `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5 — a real `Adapter` constructed via
  `assembleTelegramInbound` (the actual production assembly function), driven against
  a scripted stub Telegram server, proves the owner's first command routes without a
  pending exchange through the REAL wiring — not a hand-seeded test double. A live bot
  token (L6) adds no additional confidence for this specific fix.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC159
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... ./internal/channel/telegram/... -run TestTC159
  ```
  Expected: TC-159-04's end-to-end owner-first-command-routes-without-pending assertion passes.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Any change to `DecidePairing`'s grammar/ordering logic (task 152's anti-self-approval
  control, TC-152-05, is untouched — this task only changes what gets WRITTEN to the
  store at startup, not how the store is CONSULTED).
- Tasks 157 (idle/reject termination) and 158 (armor wiring) — sequenced before this
  task to avoid merge conflicts; this task's diff to `assembleTelegramInbound` is
  additive to whatever those tasks land.
