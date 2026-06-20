# Task 072: PolicyDaemon lifecycle + decide-gate before exec-sandbox

**Project:** agent-builder
**Created:** 2026-06-19
**Status:** backlog

## Goal

1. Add `internal/policy/lifecycle.go` — start `policy-engine serve --socket <path>
   --allow <hosts>` as a subprocess, ping-wait for readiness, and stop cleanly
   (mirroring `internal/vault/lifecycle.go`).
2. Wire the decide-gate into `runtime.Run`: build an AuthZEN `DecideRequest`, call
   `PolicyClient.Decide` BEFORE `sandboxBox.Create`, and apply `deny`, `tier_select`,
   and `vault_injection_floor` obligations.
3. Update `docs/spec/configuration.md`, `docs/spec/architecture.md`,
   `docs/spec/behaviors.md`/`data-model.md` as needed, and `docs/architecture/diagrams.md`.

The `require_approval` and `audit_emit` obligations are wired in task 073.

## Context

### Components introduced

**`internal/policy/lifecycle.go`** — manages the policy-engine daemon subprocess:

```go
type PolicyDaemon struct {
    BinPath    string
    SocketPath string
    Allow      []string  // fed from config.Limits.EgressAllowlist
}
// Start execs policy-engine serve, waits for Ping (up to 5 seconds).
func (d *PolicyDaemon) Start(ctx context.Context) error
// Stop kills the subprocess and removes the socket file.
func (d *PolicyDaemon) Stop() error
```

Start fails loud when:
- `BinPath` is empty or not executable.
- Ping does not succeed within the timeout (5 seconds, mirroring the vault pattern).

The daemon is launched with `policy-engine serve --socket <path> --allow <comma-CSV-of-hosts>`.
The `--allow` value is built from `Limits.EgressAllowlist` (already in `sandbox.Limits`;
no new config field needed for the allowlist itself).

### Wiring in `runtime.Run`

When `AGENT_BUILDER_POLICY_BIN` is set:

1. Resolve the policy binary (fail loud before dispatch if unresolvable — same pattern
   as `resolveAuditBin`).
2. Start the `PolicyDaemon`.
3. Build the `DecideRequest`:
   ```go
   DecideRequest{
       Subject:  Subject{Type: "agent", ID: "agent-builder"},
       Action:   Action{Name: "run-task"},
       Resource: Resource{Type: "task", ID: task.ID, Properties: map[string]any{"egress_hosts": config.EgressAllowlist}},
       Context:  PolicyContext{Risk: policyRisk},  // policyRisk from AGENT_BUILDER_POLICY_RISK or "low"
   }
   ```
4. Call `PolicyClient.Decide(req)`.
5. Apply obligations:
   - `tier_select` → `sandbox.Request.Tier = obligation.Value`.
   - `vault_injection_floor` → raise `RunWiring.InjectionMode` if obligation value is
     stricter (`env → proxy`; never lower `proxy → env`).
6. Check the decision:
   - `deny` → write needs-human status (`"policy: decision denied"`); return without
     dispatching; box never starts.
   - `require_approval` → handled in task 073 (task 072 routes it the same as deny
     with a placeholder reason).
   - `allow` → proceed to `sandboxBox.Create`.
7. Defer `daemon.Stop()`.

**Critical ordering invariant:** `decide` is called AFTER vault handle resolution (so
vault handles are in `RunWiring` when the obligation applies `vault_injection_floor`)
but BEFORE `sandboxBox.Create` (so obligations can raise the floor on already-resolved
handles, and a deny stops the box from starting).

**Opt-in:** when `AGENT_BUILDER_POLICY_BIN` is unset, the policy gate is entirely
skipped; the run proceeds exactly as before (zero regression for existing deployments).

### New configuration env vars

| Env var | Description |
|---------|-------------|
| `AGENT_BUILDER_POLICY_BIN` | Path to the `policy-engine` binary. If unset, policy gate is disabled (old behavior). |
| `AGENT_BUILDER_POLICY_SOCKET` | Unix socket path for the policy daemon. Default: `/tmp/agent-builder-policy-<pid>.sock`. |
| `AGENT_BUILDER_POLICY_RISK` | Static risk level passed as `context.risk` in the AuthZEN request. Default: `"low"`. |

### L6 operator capstone (not a backlog task — operator-gated)

The full L6 evidence path for this task is: a live `policy-engine` binary gating a
real exec-sandbox run — deny blocks dispatch; allow with `tier_select` routes to the
correct tier; `vault_injection_floor` raises the real vault proxy floor. This requires
operator coordination (real creds + real policy-engine binary + real exec-sandbox) and
is tracked in `docs/plans/l6-operator-runbook.md`, mirroring TC-066-05/06 for vault.
The L5 fake-binary harness (TC-072-06) is the primary verification bar for task
completion; the L6 capstone is the follow-on evidence for the ✅ promotion.

## Requirements

| Req ID     | Description                                                                                                                                                                                                                                                       | Priority  |
|------------|-------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|-----------|
| REQ-072-01 | `internal/policy/lifecycle.go` implements `PolicyDaemon.Start(ctx)` and `Stop()`. `Start` execs `policy-engine serve --socket <path> --allow <csv>` and waits (up to 5 seconds) for `Ping` to succeed. Missing binary or non-executable path fails loud before exec. `Stop` kills the subprocess and removes the socket file. | must have |
| REQ-072-02 | `runtime.Run` calls `PolicyClient.Decide` before `sandboxBox.Create` when `AGENT_BUILDER_POLICY_BIN` is set. `deny` → write needs-human status; box never starts. `tier_select` → `sandbox.Request.Tier`. `vault_injection_floor` → raise `RunWiring.InjectionMode`, never lower. | must have |
| REQ-072-03 | `vault_injection_floor` obligation raise-only: `env → proxy` is a raise; `proxy → env` is silently ignored. Both `VaultSocket` and `SecretRefs` are preserved when `InjectionMode` is raised. | must have |
| REQ-072-04 | `docs/spec/configuration.md` documents `AGENT_BUILDER_POLICY_BIN`, `AGENT_BUILDER_POLICY_SOCKET`, `AGENT_BUILDER_POLICY_RISK`. `docs/spec/architecture.md` describes the policy-gate. `docs/architecture/diagrams.md` updated. `make check` exits 0. Unset `AGENT_BUILDER_POLICY_BIN` = unchanged behavior; `TestPhase0EndToEndAcceptance` still passes. | must have |
| REQ-072-05 | L5 fake-binary e2e: fake policy binary responds deny → box not started, needs-human status; fake binary responds allow + tier_select → box started with correct tier; fake binary responds allow + vault_injection_floor → InjectionMode raised. | must have |

## Readiness gate

- [x] Test spec `072-policy-daemon-lifecycle-and-decide-gate-test-spec.md` exists (written first)
- [ ] Task 070 (ADR-038) merged and accepted
- [ ] Task 071 (`internal/policy` client) merged and verified
- [ ] policy-engine binary built (`make build` in `~/Code/Public/policy-engine`) — needed for L5 live TC-072-01; not required for TC-072-02..06
- [ ] `make check` green on main before starting

## Acceptance criteria

- [ ] [REQ-072-01] TC-072-01: `PolicyDaemon` starts, becomes reachable, stops; missing binary errors loud
- [ ] [REQ-072-02] TC-072-02: deny blocks dispatch; needs-human status written; sandbox runner not called
- [ ] [REQ-072-02] TC-072-03: tier_select sets `sandbox.Request.Tier`; box is started
- [ ] [REQ-072-03] TC-072-04: vault_injection_floor raise-only; all five sub-cases correct
- [ ] [REQ-072-04] TC-072-05: 3 env vars in configuration.md; architecture.md + diagrams.md updated; `make check` green; Phase-0 capstone unaffected
- [ ] [REQ-072-05] TC-072-06: fake-binary e2e — deny/allow/tier/floor sub-cases all pass

## Verification plan

- **Highest level achievable in-repo (L5, fake binary):** TC-072-06 exercises all four
  obligation paths with a scripted fake binary, matching the fake-vault/Podman launcher
  pattern used in the Phase-0 capstone. No real `policy-engine` binary needed at L5.
- **L5 harness command (fake binary, default path):**
  ```
  go test -count=1 ./internal/policy/... ./internal/runtime/...
  go test -count=1 ./tests/e2e/... -run 'TestPhase0EndToEndAcceptance|TestPolicyGate'
  make check
  ```
  Expected: all `ok`; `make check` → `All checks passed.`
- **L5 with real policy-engine binary** (gated on `AGENT_BUILDER_LIVE_POLICY=1`):
  ```
  AGENT_BUILDER_LIVE_POLICY=1 \
  AGENT_BUILDER_POLICY_BIN=$HOME/Code/Public/policy-engine/bin/policy-engine \
  go test -count=1 -v ./internal/policy/... -run TestPolicyDaemonLifecycle
  ```
- **L6 capstone (operator-gated; not a backlog task):** recorded in
  `docs/plans/l6-operator-runbook.md`. Evidence path: live `policy-engine` + real
  exec-sandbox run; deny blocks dispatch; allow + tier_select routes to correct tier;
  `vault_injection_floor` raises real vault proxy floor.

## Out of scope

- `require_approval` distinct routing and status reason (task 073).
- `audit_emit` obligation wiring (task 073).
- The F-006 fitness check (task 074).
- Dynamic risk scoring (deferred per ADR 038).
- Rate limiting, decision caching (policy-engine v1 features; the daemon serves these
  transparently without agent-builder changes).

## Dependencies

- Task 070 (ADR-038) — must be merged and human-approved.
- Task 071 (`internal/policy` client) — `PolicyClient`, `DecideRequest`, `DecideResponse`
  types must exist; lifecycle.go uses `PolicyClient.Ping` for readiness detection.
