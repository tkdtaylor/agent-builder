# Test Spec 164: fail-closed validation of the `tier_select` obligation value

**Linked task:** [`docs/tasks/backlog/164-sandbox-tier-select-fail-closed.md`](../backlog/164-sandbox-tier-select-fail-closed.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

**Deviation note (read before implementing):** the original planning brief for this
task ID assumed `tier_select`/`vault_injection_floor`/`audit_emit` were entirely
unconsumed (an "R1 gap"). That is stale, tasks 072/073 (2026-06-19) already wire
all three obligations end-to-end and prove it in `tests/e2e/policy_gate_e2e_test.go`
(`TC-072-03_allow_tier_select_starts_box_with_tier`,
`TC-072-06C_allow_vault_injection_floor_raised`) and `internal/policy/obligation_test.go`.
`docs/plans/roadmap.md` (block-status table, "policy-engine" row) confirms this is
**✅ Adopted (L5)**. This task closes a narrower, real, residual gap found while
verifying that area: nothing validates a `tier_select` obligation's VALUE before it
is threaded onto the box request.

`policy.TierSelect(resp.Obligations)` (`internal/policy/obligation.go:38-48`) returns
whatever string the policy engine sends, uncoerced beyond a `string` type assertion.
`internal/runtime/run.go`'s `decideGate` (`DecisionAllow` branch, lines 1046-1054)
passes that string straight into `gateOutcome.tier`, which `Run` then assigns to
`sandboxBox.tier` (line 868) and `Request.Tier` (`internal/sandbox/run.go:30`, via
`execsandbox/run.go:369-370`). Nothing in `sandbox.ValidateRequest`
(`internal/sandbox/run.go:70-75`) or anywhere else on this path checks the string
against the two tiers agent-builder actually knows about (`"bubblewrap"`,
`"gvisor"`, `internal/sandbox/run.go:30,64`, `execsandbox/run.go:281-283`). A
policy-engine bug, a config typo in its obligation table, or (worst case) an
untrusted/compromised policy-engine response can currently send an arbitrary string
straight to the external exec-sandbox block binary, which is the FIRST point
anything would notice, after the host-side gate already decided to proceed. This
task moves that check to the host-side boundary, matching the fail-closed posture
`docs/architecture/decisions/038-policy-engine-integration.md` established for every
other obligation on this path.

**Module boundaries touched:** `internal/sandbox` (new exported constants + a pure
validator function, no new imports, no behavior change to existing exported
surface) and `internal/runtime` (one new branch inside `decideGate`'s
`DecisionAllow` case). Two modules, one narrow responsibility each.

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-164-01 | `internal/sandbox` exports `TierBubblewrap = "bubblewrap"`, `TierGvisor = "gvisor"`, and `func ValidTier(tier string) bool` where `""`, `TierBubblewrap`, `TierGvisor` are valid and every other string is not (case-sensitive, no aliasing). | TC-164-01 |
| REQ-164-02 | `decideGate`'s `DecisionAllow` branch calls `sandbox.ValidTier(tier)` (where `tier := policy.TierSelect(resp.Obligations)`, already computed at line 1041) before constructing the allowed `gateOutcome`. An invalid tier flips the outcome to `allowed: false`, with `reason` containing both `"unknown tier"` and the offending tier value (via `%q`), so the operator-visible halt message names the bad value. | TC-164-02, TC-164-04 |
| REQ-164-03 | The halted-on-invalid-tier outcome still carries `auditEmit`/`policyDecision`/`policyReason` exactly as the existing allow path does, so `maybeEmitPolicyDecision` (untouched by this task) emits an `ActionPolicyDecision` event with `Detail.PolicyDecision == "allow"` when `audit_emit` is present, letting an operator distinguish "the engine allowed but sent a bad obligation" from a genuine `deny`. | TC-164-03 |
| REQ-164-04 | End-to-end: an `allow` decision carrying `{"type":"tier_select","value":"<unknown>"}` never starts the box (zero exec-sandbox block invocations) and marks the task `needs-human`, mirroring the existing deny/require_approval halt shape (`run.go:847-856`). | TC-164-04 |
| REQ-164-05 | Valid tier values (`""`, `"bubblewrap"`, `"gvisor"`) are byte-for-byte unaffected, the pre-existing `TC-072-03_allow_tier_select_starts_box_with_tier` e2e case passes unchanged. | TC-164-05 |

---

## Pre-implementation checklist

- [x] Task 072 merged (`decideGate`, `gateOutcome`, the `tests/e2e` fake-policy-engine
  harness all already exist, this task extends them, does not build them)
- [x] Task 073 merged (`maybeEmitPolicyDecision`, `ActionPolicyDecision` already exist)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-164-01, `sandbox.ValidTier` table test

- **Requirement:** REQ-164-01
- **Level:** L2 (unit test)
- **Test file:** `internal/sandbox/tier_test.go` (new)

**Step:** Table-drive `ValidTier` over: `""`, `"bubblewrap"`, `"gvisor"`, `"Gvisor"`
(wrong case), `"runsc"` (a real gVisor binary name, deliberately NOT a valid tier
value in this codebase, must not be aliased), `"nonsense"`, `"  gvisor"` (leading
whitespace, must not be trimmed/normalized).

**Expected output:** `true` for `""`/`"bubblewrap"`/`"gvisor"`; `false` for all five
other cases. Also assert `sandbox.TierBubblewrap == "bubblewrap"` and
`sandbox.TierGvisor == "gvisor"` verbatim (pins the constants against
`internal/sandbox/run.go:30,64`'s existing doc-comment values).

---

### TC-164-02, decideGate halts on an invalid tier (design-level pin)

- **Requirement:** REQ-164-02
- **Level:** L2 (unit test, table pin on the routing contract, mirrors
  `TestRequireApprovalStatusReason` in `run_policy_audit_test.go`, which pins
  `gateOutcome` shapes without dialing a real policy daemon)
- **Test file:** `internal/runtime/run_164_test.go` (new)

**Step:** Since `decideGate` dials a live Unix socket and cannot be called with a
canned response in a pure unit test, pin the CONTRACT directly: construct the two
`gateOutcome` values decideGate must produce for (a) `tier="gvisor"` valid-allow and
(b) `tier="quantum-tier"` invalid-allow, using the exact field values the
implementation is required to set (`allowed`, `reason`, `auditEmit`,
`policyDecision`, `policyReason`), and assert:
- (a) `allowed == true`, `tier == "gvisor"` (unaffected)
- (b) `allowed == false`, `reason` contains `"unknown tier"` AND contains
  `"quantum-tier"` (via `strings.Contains` on both substrings), `tier` field is the
  zero value (never threads to `tierOverride`)

**Expected output:** both pinned shapes match; this test is the executor's
implementation contract for the new branch inside `decideGate`. The REAL proof that
`decideGate` actually produces these shapes when talking to a daemon is TC-164-04
(L5), this test alone is not sufficient evidence of REQ-164-02, only necessary.

---

### TC-164-03, audit_emit unaffected by the new halt path

- **Requirement:** REQ-164-03
- **Level:** L2 (unit test, extends the existing `maybeEmitPolicyDecision` suite)
- **Test file:** `internal/runtime/run_164_test.go`

**Step:** Call `maybeEmitPolicyDecision(sink, "164", gateOutcome{allowed: false,
reason: `policy: tier_select obligation names unknown tier "quantum-tier"`,
auditEmit: true, policyDecision: "allow", policyReason: "ok"})` against a fresh
`audit.NewFakeSink()`.

**Expected output:** one `ActionPolicyDecision` event with `Detail.PolicyDecision ==
"allow"` and `Detail.PolicyReason == "ok"`, proving the event still names the
ENGINE's decision (`allow`), not agent-builder's own halt reason, so an operator
reading the audit chain can tell the engine allowed the action and the halt was
agent-builder's own obligation-validation kicking in.

---

### TC-164-04, end-to-end: unknown tier halts dispatch, zero box invocations

- **Requirement:** REQ-164-02, REQ-164-04
- **Level:** L5 (real `agent-builder` binary + fake policy-engine + fake exec-sandbox
  block, the exact harness `tests/e2e/policy_gate_e2e_test.go` already uses for
  `TC-072-03`)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go` (extend
  `TestPolicyGateFakeBinaryE2E` with a new `t.Run` sub-test, or add a sibling test
  function `TestPolicyGateUnknownTierHalts`, either is acceptable, match the file's
  existing style)

**Setup:** `policyEnv(fixture, fakePolicy, socket, argsLog,
'{"decision":"allow","context":{"reason":"ok","obligations":[{"type":"tier_select","value":"quantum-tier"}]}}')`,
`env[runtimewiring.EnvExecSandboxBin] = fakeBlock` (mirroring TC-072-03's setup
exactly, only the obligation value differs).

**Step:** `runAgentBuilder(t, binary, env, "run")`.

**Expected output:** exit code `0` (a halt is a terminal outcome, not a process
error, matches the deny/require_approval convention); stdout contains `"run
halted"`, `"unknown tier"`, and `"quantum-tier"`; the fake block's log file is
either absent or contains zero `"REQUEST"` occurrences (`strings.Count(recorded,
"REQUEST") == 0`, mirrors `assertNoPublishLog`'s "the box never started" proof
pattern used for TC-072-02); the task file
(`docs/tasks/backlog/001-first.md` in the fixture) contains `"**Status:**
needs-human"`.

---

### TC-164-05, regression: valid tiers unaffected

- **Requirement:** REQ-164-05
- **Level:** L5 (regression, re-run, do not modify)

**Step:** `go test -race -count=1 ./tests/e2e/... -run
'TestPolicyGateFakeBinaryE2E/TC-072-03_allow_tier_select_starts_box_with_tier'`

**Expected output:** unchanged pass, `"tier":"gvisor"` recorded on the fake block's
request, exit 0, `"run completed: task 001"` in stdout. Byte-identical to pre-task
behavior.

---

### TC-164-06, full regression

- **Requirement:** all
- **Level:** L2/L3/L5

**Step:**
```
go test -race -count=1 ./internal/sandbox/... ./internal/runtime/... ./tests/e2e/... -run 'TestValidTier|TestPolicyGate'
make check
```

**Expected output:** all packages `ok`; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, the same fake-policy-engine + fake-exec-sandbox
  harness `tests/e2e/policy_gate_e2e_test.go` already uses for the sibling
  `tier_select` case (TC-072-03). No L6 required; the mechanism (a string compared
  against an allowlist) needs no live operator observation beyond what L5 already
  proves.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/sandbox/... ./internal/runtime/... -run 'TestValidTier|TestTC164'
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./tests/e2e/... -run 'TestPolicyGateFakeBinaryE2E|TestPolicyGateUnknownTierHalts'
  ```
  Expected: the new unknown-tier subtest halts with zero block invocations; the
  pre-existing `TC-072-03`/`TC-072-06C` subtests pass unchanged.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Any change to `vault_injection_floor` or `audit_emit` obligation handling (task
  166 covers a distinct, unrelated residual gap in this same file; this task does
  not touch that logic).
- Validating tier values anywhere other than the host-side `decideGate` boundary
  (e.g. inside `execsandbox`'s wire protocol), the external exec-sandbox block is
  a separate trust boundary with its own contract; agent-builder's job is to not
  forward an untrusted/malformed value past its own gate.
- Adding new tier values (e.g. Kata/Firecracker), this task validates against the
  CURRENT known set only.
