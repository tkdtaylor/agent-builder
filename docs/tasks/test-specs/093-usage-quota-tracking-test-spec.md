# Test spec — Task 093: Usage/quota tracking

**Linked task:** `docs/tasks/backlog/093-usage-quota-tracking.md`
**Written:** 2026-06-27
**Status:** ready

## Context

ADR 043 requires router-owned quota state that persists across dispatches. The router
from task 092 holds in-memory state only. This task adds:

1. **Usage tally increment** — the router increments `Usage` on each dispatch;
   proactive budget check pre-empts sends to an over-budget entry.
2. **Reactive exhaustion detection** — 429 / `Retry-After` signal → mark exhausted,
   derive `ResetAt` from the header or a configured cooldown.
3. **Clock seam** — `Clock` interface with `Now() time.Time`; the router takes an
   injected clock instead of calling the wall clock directly, enabling deterministic
   tests.
4. **Reset-window recovery** — when `now > entry.Availability.ResetAt`, the entry
   flips back to `AvailStatusAvailable` automatically.
5. **File persistence** — the quota state (Usage, Availability) is saved to a
   plain-text file across runs (JSON or TOML). The memory-guarded store for the
   orchestrator is a forward-link, not built here.

## Requirements coverage

| Req ID     | Test cases                     | Covered? |
|------------|--------------------------------|----------|
| REQ-093-01 | TC-093-01, TC-093-02           | yes      |
| REQ-093-02 | TC-093-03                      | yes      |
| REQ-093-03 | TC-093-04                      | yes      |
| REQ-093-04 | TC-093-05                      | yes      |
| REQ-093-05 | TC-093-06                      | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-093-01 — Usage tally increments on each dispatch; proactive budget check

- **Requirement:** REQ-093-01
- **Level:** L2 (unit test)
- **Test file:** `internal/router/quota_test.go`

**Input:** Registry entry `{ID:"claude-oauth", Budget:{Limit:3, Window:5h}, Usage:0}`.
Call `router.RecordDispatch("claude-oauth")` three times (incrementing `Usage` to 3).
Then call `router.Select(RoutingSpec{MinCapability:1})` while `"claude-oauth"` is the
only eligible entry.

**Expected output:**
- After 3 dispatches, `entry.Usage == 3` (equal to `Budget.Limit`).
- `router.Select` returns `ErrNoEligibleExecutor` (entry is proactively excluded
  because `Usage >= Budget.Limit`).
- `entry.Availability.Status == AvailStatusExhausted`.

---

### TC-093-02 — Rolling window reset

- **Requirement:** REQ-093-01
- **Level:** L2 (unit test with injected clock)
- **Test file:** `internal/router/quota_test.go`

**Input:** Advance the injected clock past `entry.Availability.ResetAt` (which was
set to `now + Budget.Window` when the entry was marked exhausted).

**Expected output:**
- On the next `router.Select`, the entry re-enters the eligible set automatically.
- `entry.Usage` resets to 0.
- `entry.Availability.Status == AvailStatusAvailable`.
- No manual intervention is required.

---

### TC-093-03 — Reactive exhaustion: 429 / Retry-After parsing

- **Requirement:** REQ-093-02
- **Level:** L2 (unit test)
- **Test file:** `internal/router/quota_test.go`

**Input A:** Call `router.OnRateLimit("claude-oauth", retryAfterHeader="60")` where
the header value is 60 seconds.

**Expected output A:**
- `entry.Availability.Status == AvailStatusExhausted`.
- `entry.Availability.ResetAt == now + 60s` (using the injected clock).

**Input B:** Call `router.OnRateLimit("claude-oauth", retryAfterHeader="")` — no
`Retry-After` header.

**Expected output B:**
- `entry.Availability.Status == AvailStatusExhausted`.
- `entry.Availability.ResetAt == now + configuredCooldown` (falls back to a
  configured cooldown value, e.g. 5 minutes).

---

### TC-093-04 — Clock seam: injected clock controls now()

- **Requirement:** REQ-093-03
- **Level:** L2 (unit test)
- **Test file:** `internal/router/quota_test.go`

**Input:** Construct the router with a `FakeClock` that starts at time T.
Mark an entry exhausted with `ResetAt = T + 10s`. Call `router.Select` immediately
(clock at T) — entry is excluded. Advance the fake clock to `T + 11s`. Call
`router.Select` again.

**Expected output:**
- At time T: `router.Select` excludes the entry → `ErrNoEligibleExecutor` (no other
  entries in this test).
- At time T+11s: entry is re-eligible; `router.Select` returns it.
- No `time.Sleep` call exists in the test — the clock is advanced programmatically.

---

### TC-093-05 — File persistence: quota state survives process restart

- **Requirement:** REQ-093-04
- **Level:** L2 (unit test with temp file)
- **Test file:** `internal/router/quota_test.go`

**Input:** Mark `"claude-oauth"` exhausted (Usage=3, ResetAt=T+1h). Call
`router.SaveState(filePath)`. Construct a new router from the same registry catalog
and call `router.LoadState(filePath)`. Call `router.Select` before the reset time.

**Expected output:**
- After `LoadState`, `entry.Usage == 3`, `entry.Availability.Status == AvailStatusExhausted`.
- `router.Select` excludes `"claude-oauth"` (still exhausted).
- The file is plain text (JSON or TOML — implementation picks; both are acceptable).
- A corrupted file returns a descriptive error from `LoadState`, not a silent zero
  value (fail loud on state corruption).

---

### TC-093-06 — Availability-axis fallback: routes around exhausted entry

- **Requirement:** REQ-093-05
- **Level:** L2 (unit test)
- **Test file:** `internal/router/quota_test.go`

**Input:** Registry with `"claude-oauth"` (tier=3, cost=10, exhausted) and
`"local-qwen"` (tier=1, cost=1, available, unlimited). Dispatch with
`RoutingSpec{MinCapability:1}`.

**Expected output:**
- `router.Select` returns `"local-qwen"` — the local entry is the always-available
  fallback when cloud entries are exhausted.
- This emergent property comes from the same "cheapest eligible" rule: `"local-qwen"`
  is the only available entry, so it wins.

---

## Verification plan

- **Highest level achievable:** L3 — no runtime-observable surface for quota tracking
  alone. Unit tests with the injected clock seam are the verification.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/router/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/router`
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Memory-guarded store for the orchestrator (forward-link, not built here — noted in
  ADR 043 Consequences).
- The router reading from `context.Context` for rate-limit signals from the executor
  (the executor adapter calls `router.OnRateLimit` directly — implementation detail).
- Per-entry clock configuration (one shared clock seam for the whole router).
