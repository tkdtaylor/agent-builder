# Task 159: seed the pairing-mode owner into the approved-sender store at startup

**Project:** agent-builder
**Created:** 2026-07-02
**Status:** backlog

## Goal

Seed `AGENT_BUILDER_TELEGRAM_OWNER_ID` into the pairing-mode approved-sender store at
`assembleTelegramInbound` startup, additively and persisted, so the owner's own first
ordinary command routes normally instead of through the stranger "pending" flow the
owner would otherwise have to approve themselves out of.

## Context

**Root cause (full-project review, verified 2026-07-02):**
`assembleTelegramInbound` (`internal/cli/orchestrate.go:1246-1379`) resolves the owner
ID (`assembleTelegramOwnerID`, lines 1207-1220) ONLY for `Adapter.ownerID` — consulted
by `DecidePairing` (`internal/channel/telegram/authz/pairing.go:124-152`) to gate the
approve/deny grammar and route the owner-notification path. The approved-sender
STORE is seeded ONLY from `AGENT_BUILDER_TELEGRAM_APPROVED_IDS`
(`assembleTelegramAuthMode`, lines 1173-1185) — the owner's own ID never reaches it.

`DecidePairing` step 3 requires `store.Contains(rawSenderID)` for the owner's own
ordinary plaintext commands (anything that isn't the `approve`/`deny` grammar) to
route normally. Since the owner is never seeded, the owner's first ordinary command
is treated exactly like a stranger's — routed to the pending flow, requiring the
owner to `approve <own-id>` themselves before they can issue their first real command.
This is a confusing, entirely avoidable onboarding footgun for the person the mode is
built around.

The existing test `TestTC152_07_ApproveConsumedButOwnerStatusRoutes`
(`internal/channel/telegram/adapter_152_test.go:409-...`) already works AROUND this
gap by manually pre-seeding the owner's own ID via
`newPairingHarness(t, srv, 1, storePath, "1")`'s `seededIDs` parameter — proving the
test author already knew production doesn't do this, and papering over it rather than
exercising the real path.

**The fix:** `assembleTelegramInbound` seeds `ownerID` (already resolved by
`assembleTelegramOwnerID`) into the approved-sender store additively (union — same
semantics as the existing `APPROVED_IDS` seeding, never removing IDs already on disk)
and persists it, whenever the resolved mode is `pairing`. `TestTC152_07`'s workaround
is updated so the real production seeding path (proven via a new `internal/cli` test)
is the test of record for "the owner is actually pre-approved in production," and the
package-level `adapter_152_test.go` fixture's misleading "this is contrived" comment
is corrected to state the gap is now fixed upstream.

**Reference:**
- `internal/cli/orchestrate.go:1150-1220` (`assembleTelegramAuthMode`,
  `assembleTelegramOwnerID`)
- `internal/cli/orchestrate.go:1242-1379` (`assembleTelegramInbound`)
- `internal/channel/telegram/authz/pairing.go:124-152` (`DecidePairing`)
- `internal/channel/telegram/authz/authz.go:221` (`Store.Add`), `:171` (`Store.Persist`)
- `internal/channel/telegram/adapter_152_test.go:390-420` (`TestTC152_07`, the
  workaround this task removes/relabels)

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-159-01 | In pairing mode, `assembleTelegramInbound` seeds the resolved owner ID into the approved-sender store, additively, before the adapter is returned. | must have |
| REQ-159-02 | The owner seed is persisted to the store's `0600` file so it survives a process restart from the same store path, matching the existing `APPROVED_IDS` seeding's durability contract. | must have |
| REQ-159-03 | Non-pairing modes (envelope, disabled, allowlist, open) are unaffected — no owner-seeding logic runs; a stray `OWNER_ID` env var never reaches an `allowlist`-mode store. | must have |
| REQ-159-04 | End-to-end, through the REAL `assembleTelegramInbound`-constructed `Adapter`: the owner's own first plaintext command routes normally with no prior approve/pending exchange. | must have |
| REQ-159-05 | `TestTC152_07`'s manual owner-preseed workaround is replaced or clearly relabeled so a REAL-wiring test (in `internal/cli`) is the test of record for "production actually seeds the owner," and the misleading "this is contrived" comment is corrected. | must have |
| REQ-159-06 | Pre-existing pairing-mode suites (task 152) continue to pass unchanged for every other assertion. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/159-telegram-pairing-owner-seed-test-spec.md` exists (written first)
- [x] Task 152 merged (pairing mode, `DecidePairing`, `Store`, and the workaround this
  task replaces already exist)
- [x] Task 158 merged (armor wiring lands first in the sequenced Telegram set)
- [ ] `make check` green on `main` before branching

## Acceptance criteria

- [ ] [REQ-159-01] TC-159-01: the owner ID is seeded into the store in pairing mode, even with no static `APPROVED_IDS` set.
- [ ] [REQ-159-02] TC-159-02: the owner seed is additive and survives a simulated restart, alongside any pre-existing statically-seeded IDs.
- [ ] [REQ-159-03] TC-159-03: non-pairing modes are unaffected by owner-seeding logic.
- [ ] [REQ-159-04] TC-159-04: the owner's first plaintext command routes normally end-to-end through the real `assembleTelegramInbound`-constructed adapter, with no pending/approve exchange.
- [ ] [REQ-159-05] TC-159-05: `TestTC152_07`'s workaround is replaced/relabeled; its misleading comment is corrected.
- [ ] [REQ-159-06] TC-159-06: `go test -race -count=1 ./internal/cli/... ./internal/channel/telegram/...` passes in full; `make check` passes.

## Verification plan

- **Highest level achievable:** L5 — a real `Adapter` constructed via
  `assembleTelegramInbound`, driven against a scripted stub Telegram server, proves
  the owner's first command routes without a pending exchange through the actual
  production wiring. A live bot token (L6) adds no additional confidence here.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/cli/... -run TestTC159
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/cli/... ./internal/channel/telegram/... -run TestTC159
  ```
  Expected: TC-159-04 passes (fails against the pre-fix code — the owner would be
  routed to `ActionPairingPending` instead).
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Spec/doc footprint (update in the feat commit)

- `docs/spec/configuration.md` — the `AGENT_BUILDER_TELEGRAM_OWNER_ID` row (task
  152's addition) gains a sentence: "the owner ID is also seeded into the approved-
  sender store at startup (task 159), so the owner's own first plaintext command
  routes without a self-approval step."
- `docs/spec/behaviors.md` — the pairing-mode behavior entry (ADR 063 Decision 3) gets
  the same clarification.

## Out of scope

- Any change to `DecidePairing`'s grammar/ordering logic or the anti-self-approval
  control (TC-152-05) — this task only changes what is written to the store at
  startup, not how the store is consulted.
- Tasks 157/158 — sequenced before this task to avoid merge conflicts.

## Dependencies

- **Blocks on:** task 152 (pairing mode itself), task 158 (must land first — same
  `assembleTelegramInbound` function).
- **Blocks:** none.
