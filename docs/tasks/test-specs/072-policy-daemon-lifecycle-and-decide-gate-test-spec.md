# Test spec — Task 072: PolicyDaemon lifecycle + decide-gate before exec-sandbox

**Linked task:** `docs/tasks/backlog/072-policy-daemon-lifecycle-and-decide-gate.md`
**Written:** 2026-06-19
**Status:** ready

## Context

This task wires the policy-engine block into agent-builder's run path. It has two
responsibilities that are delivered in the same task (they share a coupling point in
`internal/runtime/run.go`):

**`internal/policy/lifecycle.go`** — start `policy-engine serve --socket <path>
--allow <hosts>` as a subprocess, ping-wait for it to become reachable, and stop it
cleanly (mirroring `internal/vault/lifecycle.go`).

**Decide-gate in `runtime.Run`** — before `sandboxBox.Create` is called (before the
box starts), build an AuthZEN `DecideRequest`, call `PolicyClient.Decide`, and apply
the response:
- `deny` → write a needs-human status on the task, return without dispatching; box
  never starts.
- `require_approval` → same flow as deny but with a distinct status reason
  (`"policy: requires human approval"` vs `"policy: decision denied"`); handled in
  task 073.
- `tier_select` obligation → set `sandbox.Request.Tier` (overrides the default
  `"bubblewrap"`).
- `vault_injection_floor` obligation → raise `sandbox.RunWiring.InjectionMode` if the
  obligation value is stricter than the current wiring (env → proxy; never lower).
- `audit_emit` obligation → handled in task 073.

**Ordering invariant (load-bearing):** `decide` must run BEFORE vault floor
finalization. The vault wiring block runs before `decide` in `runtime.Run` (it
resolves handles), but the `InjectionMode` field on `RunWiring` may be raised by
the `vault_injection_floor` obligation AFTER vault handles are resolved. The correct
ordering is: start vault (if configured) → resolve handles → call `decide` → apply
`vault_injection_floor` to the already-constructed `RunWiring`. This means the
obligation can raise `proxy` even if vault wiring defaulted to `env`; it can never
lower proxy to env.

**Opt-in:** `AGENT_BUILDER_POLICY_BIN` (+ `AGENT_BUILDER_POLICY_SOCKET`,
`AGENT_BUILDER_POLICY_RISK`). Unset `AGENT_BUILDER_POLICY_BIN` → no policy gate,
unchanged behavior.

**Spec updates in this task (same commit as code):**
- `docs/spec/configuration.md` — three new env vars.
- `docs/spec/architecture.md` — policy-gate component + decide-before-box flow.
- `docs/spec/behaviors.md` or `docs/spec/data-model.md` — deny → needs-human status
  reason, tier override.
- `docs/architecture/diagrams.md` — decide-gate in the run flow.

The L6 evidence path (not one of the five backlog tasks — operator-gated, same as
TC-066-05/06 for vault): a live `policy-engine` binary gating a real exec-sandbox run,
asserting deny blocks dispatch and allow raises the real vault proxy floor. See the
L6 operator runbook for when this is scheduled.

## Requirements coverage

| Req ID     | Test cases                  | Covered? |
|------------|-----------------------------|----------|
| REQ-072-01 | TC-072-01                   | yes      |
| REQ-072-02 | TC-072-02, TC-072-03        | yes      |
| REQ-072-03 | TC-072-04                   | yes      |
| REQ-072-04 | TC-072-05                   | yes      |
| REQ-072-05 | TC-072-06 (L5 fake binary)  | yes      |

## Pre-implementation checklist

- [ ] All test cases below are defined
- [ ] Expected inputs and outputs are specified for each case
- [ ] Edge cases and error paths are covered
- [ ] Every REQ-ID from the task has at least one test case
- [ ] Success criteria are unambiguous

---

## Test cases

### TC-072-01 — `PolicyDaemon` starts, becomes reachable via Ping, and stops cleanly

- **Requirement:** REQ-072-01
- **Level:** L5 (subprocess test; requires the real `policy-engine` binary)
- **Test file:** `internal/policy/lifecycle_test.go`
- **Test name:** `TestPolicyDaemonLifecycle`
- **Gate flag:** `AGENT_BUILDER_LIVE_POLICY=1`; skip with `t.Skip` if unset

**Setup:**
1. Locate the policy-engine binary via `AGENT_BUILDER_POLICY_BIN`.
2. Construct `PolicyDaemon{BinPath: …, SocketPath: <tempdir>/policy.sock, Allow: ["api.github.com"]}`.
3. Call `daemon.Start(ctx)`.

**Assertions:**
- `Start(ctx)` returns nil.
- Within 5 seconds, `PolicyClient.Ping()` on the socket returns nil.
- `daemon.Stop()` returns nil and the socket file is removed (or the daemon process
  exits within 2 seconds).

**Edge cases:**
- Missing binary path → `Start()` returns non-nil error naming the missing binary.
- Binary path is a non-executable file → `Start()` returns non-nil error.
- Second `Start()` on a running daemon → returns non-nil error (already started).

---

### TC-072-02 — `deny` decision blocks dispatch; box never starts; needs-human status written

- **Requirement:** REQ-072-02
- **Level:** L5 (e2e with fake policy-engine binary via env override)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go` (or extension of existing e2e)
- **Test name:** `TestPolicyGateDenyBlocksDispatch`
- **Gate flag:** `AGENT_BUILDER_LIVE_POLICY_FAKE=1` (uses a fake policy-engine binary
  that always responds `{"decision":"deny",…}`)

**Setup:**
1. Build a fake `policy-engine` binary (shell script or tiny Go binary) that starts a
   Unix-socket server and always responds with `{"decision":"deny","context":{"reason":"test","obligations":[]}}`.
2. Set `AGENT_BUILDER_POLICY_BIN=<path-to-fake>` and `AGENT_BUILDER_POLICY_SOCKET=<tempdir>/policy.sock`.
3. Use the existing `go test ./tests/e2e/...` harness with a ready task in the task root
   and a fake Claude executor and sandbox runner.

**Assertions:**
- `runtime.Run` returns without error (deny is a valid terminal outcome, not an
  unexpected error).
- The sandbox runner (`FakeRunner` or equivalent) receives ZERO `Run(Request)` calls —
  the box is never started.
- The task status file shows a needs-human status reason containing `"policy"` and
  `"denied"` (or equivalent: the exact text is implementation-defined but must be
  distinct from the vault/gate failure paths).
- No vault daemon is started when policy denies before vault would be initialized
  (ordering: policy gate runs before box.Create, but AFTER vault handle resolution —
  this test does not require vault to be configured; it confirms the box never starts).

---

### TC-072-03 — `tier_select` obligation sets `sandbox.Request.Tier`

- **Requirement:** REQ-072-02
- **Level:** L5 (unit/integration test with in-process fake policy client)
- **Test file:** `internal/runtime/run_test.go` or `tests/e2e/policy_gate_e2e_test.go`
- **Test name:** `TestPolicyGateTierSelect`

**Setup:**
Fake policy client (in-process, implementing `policy.Decider` interface or similar
seam) that returns:
```json
{"decision":"allow","context":{"obligations":[{"type":"tier_select","value":"gvisor"}]}}
```

**Assertions:**
- The `sandbox.Request.Tier` field passed to the sandbox runner is `"gvisor"` (not the
  default `"bubblewrap"`).
- The sandbox runner is called exactly once (the run proceeds normally after policy allow).

---

### TC-072-04 — `vault_injection_floor` obligation raises `InjectionMode`; never lowers it

- **Requirement:** REQ-072-03
- **Level:** L5 (unit test)
- **Test file:** `internal/runtime/run_test.go` or `internal/policy/obligation_test.go`
- **Test name:** `TestVaultInjectionFloorObligation`

Sub-cases:

| Initial `InjectionMode` | Obligation value | Expected final `InjectionMode` |
|------------------------|------------------|---------------------------------|
| `""` (empty, vault off) | `"proxy"` | `"proxy"` |
| `"env"` | `"proxy"` | `"proxy"` (raised) |
| `"proxy"` | `"env"` | `"proxy"` (never lowered) |
| `"proxy"` | `"proxy"` | `"proxy"` (unchanged) |
| `""` | `"env"` | `"env"` |

**Assertions (for each sub-case):**
- The `InjectionMode` field on the `sandbox.RunWiring` that reaches `runner.Run` equals
  the `Expected final InjectionMode` column for that sub-case.
- The `VaultSocket` and `SecretRefs` fields are not cleared by applying the obligation
  (the obligation only touches `InjectionMode`).

---

### TC-072-05 — spec files updated; new env vars in `configuration.md`; `make check` green

- **Requirement:** REQ-072-04
- **Level:** L3 / L5
- **Test file:** `Makefile` + spec files (grep assertions)

**Assertions:**
- `docs/spec/configuration.md` documents at least:
  - `AGENT_BUILDER_POLICY_BIN` — path to the `policy-engine` binary; unset disables.
  - `AGENT_BUILDER_POLICY_SOCKET` — Unix socket path; defaults to a temp path when
    policy is enabled and this is unset.
  - `AGENT_BUILDER_POLICY_RISK` — static risk level passed as `context.risk`;
    defaults to `"low"`.
- `docs/spec/architecture.md` (or equivalent spec file) describes the policy-gate
  component and the decide-before-box flow.
- `docs/architecture/diagrams.md` is updated (date bump at top; decide-gate visible in
  the run-flow diagram).
- `go test ./...` exits 0.
- `make check` → `All checks passed.`
- `go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance` passes with
  `AGENT_BUILDER_POLICY_BIN` unset (policy is opt-in; existing capstone is unchanged).

---

### TC-072-06 — L5 fake-binary e2e: deny blocks dispatch; allow passes through; tier raised

- **Requirement:** REQ-072-05
- **Level:** L5 (e2e; fake policy binary launched as a subprocess)
- **Test file:** `tests/e2e/policy_gate_e2e_test.go`
- **Test name:** `TestPolicyGateFakeBinaryE2E`
- **Gate flag:** tests must pass without requiring the real `policy-engine` binary;
  the fake binary IS the binary under test

**Sub-case A — fake deny binary:**
- Fake binary starts, serves one request with `{"decision":"deny",…}`, exits.
- `runtime.Run`: box not started; task set to needs-human.

**Sub-case B — fake allow + tier_select binary:**
- Fake binary starts, serves one request with `{"decision":"allow","context":{"obligations":[{"type":"tier_select","value":"gvisor"}]}}`.
- `runtime.Run`: box started once; `sandbox.Request.Tier == "gvisor"`.

**Sub-case C — fake allow with vault_injection_floor raise:**
- Fake binary serves `{"decision":"allow","context":{"obligations":[{"type":"vault_injection_floor","value":"proxy"}]}}`.
- Initial `RunWiring.InjectionMode` is `""` (vault not configured).
- `runtime.Run`: `sandbox.Request.Wiring.InjectionMode == "proxy"` after obligation
  is applied (even though no vault handles are present).

**Assertions common to all sub-cases:**
- The fake binary is launched as a subprocess with `--socket <path>` and `--allow <hosts>`
  args (confirming the lifecycle wiring passes the right flags).
- The fake binary's socket is cleaned up after `Stop()`.

---

## Verification plan

- **Highest level achievable in-repo (L5, fake binary):** TC-072-06 plus the gate flag
  tests in TC-072-02/03/04. The fake binary approach mirrors the fake vault/Podman
  launcher pattern used in the Phase-0 capstone.
- **L5 harness command (fake binary, no real policy-engine):**
  ```
  go test -count=1 ./internal/policy/... ./internal/runtime/... ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPolicyGate'
  make check
  ```
  Expected: all tests `ok`; `make check` → `All checks passed.`
- **L5 with real policy-engine binary** (gated on `AGENT_BUILDER_LIVE_POLICY=1`):
  ```
  AGENT_BUILDER_LIVE_POLICY=1 \
  AGENT_BUILDER_POLICY_BIN=$HOME/Code/Public/policy-engine/bin/policy-engine \
  go test -count=1 -v ./internal/policy/... -run TestPolicyDaemonLifecycle
  ```
- **L6 capstone (operator-gated, not a backlog task):**
  A live `policy-engine` binary gates a real exec-sandbox run. Deny blocks dispatch;
  allow with `tier_select` routes to gVisor; `vault_injection_floor` raises the real
  vault proxy floor. See `docs/plans/l6-operator-runbook.md` for the scheduled evidence
  collection.

## Out of scope

- `require_approval` routing (task 073).
- `audit_emit` obligation wiring (task 073).
- The F-006 fitness check (task 074).
- Dynamic risk scoring (deferred per ADR 038).
- OPA/Cedar evaluator backend swap (deferred per the policy-engine block's v1 roadmap).
