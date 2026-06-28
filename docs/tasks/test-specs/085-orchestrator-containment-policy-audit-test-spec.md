# Test spec — Task 085: Orchestrator self-containment + policy gating + fleet audit

**Linked task:** `docs/tasks/backlog/085-orchestrator-containment-policy-audit.md`
**Written:** 2026-06-27 (stub); **expanded:** 2026-06-28
**Status:** active — prereqs 081/083/084 merged; expanded per ADR 050.
**Governing ADR:** ADR 050 (orchestrator policy schema + scope of the three controls);
extends ADR 042 (self-repo bright line), ADR 038 (policy decide seam), ADR 026 (audit chain).

## Context

The orchestrator is privileged, network-connected, and long-lived — so it must
itself be **contained**, **gated**, and **audited**, not merely the workers. ADR 050
scopes the three coupled controls and resolves OQ-7 (the orchestrator policy schema):

1. **Containment (REQ-085-01/04):** the orchestrator's run config/record carries
   `containment=exec-sandbox` (same profile as workers — rootless, read-only rootfs,
   resource limits) and a `default-deny` egress posture. Asserted at **L2** (config/
   record field); live Podman+runsc / nftables enforcement is **L6 operator-deferred**
   (exactly as tasks 014/015/016).
2. **Policy gating (REQ-085-02):** the orchestrator issues a per-sub-goal
   `spawn-worker` policy decision in `dispatchPlan` BEFORE dispatching each worker,
   additive to 081's plan-level `spawn-plan`. deny → that worker is NOT spawned, a
   denied outcome is recorded, and the denial is reported via the Reporter.
3. **Fleet audit (REQ-085-03):** orchestrator events (goal-intake, plan-decided,
   per-worker spawn-decided, completion) AND all worker events append to ONE
   `audit.Sink` chain. Verified via FakeSink ordering/coverage at L2 and (when the
   `audit-trail` binary is present) `audit-trail verify` → `valid=true` at L5.
4. **Self-repo bright line (REQ-085-05):** belt-and-suspenders — (a) RUNTIME: the
   `spawn-worker` decision denies any worker whose `target_repo`/sink is
   `github.com/tkdtaylor/agent-builder`, fail-closed, regardless of the policy file;
   (b) STATIC: a fitness check (**F-013**) asserts no registered recipe targets the
   own-repo as a result sink.

## Requirements coverage

| Req ID     | Description                                                                    | Test cases |
|------------|--------------------------------------------------------------------------------|------------|
| REQ-085-01 | Orchestrator run record carries `containment=exec-sandbox`                      | TC-085-01  |
| REQ-085-02 | `spawn-worker` per-sub-goal gate: deny → no worker + denial reported            | TC-085-02  |
| REQ-085-03 | Fleet-wide audit chain covers orchestrator + all worker events in one chain     | TC-085-03  |
| REQ-085-04 | Orchestrator's own egress posture is default-deny                               | TC-085-04  |
| REQ-085-05 | Self-repo bright line: runtime deny + static fitness check both fire             | TC-085-05  |

---

## Test cases

### TC-085-01 — Orchestrator run record carries `containment=exec-sandbox` (L2)

- **Requirement:** REQ-085-01
- **Level:** L2 (unit) + L6 (live Podman+runsc enforcement) operator-deferred.

**Input:** Construct an Orchestrator (default options).

**Expected output (assertions):**
- `o.Containment()` returns a `Containment` value with:
  - `Profile == "exec-sandbox"`
  - `Rootless == true`
  - `ReadOnlyRootfs == true`
  - `ResourceLimited == true`
- The same profile constant the workers use (`ContainmentProfileExecSandbox`) is the
  value; the test asserts string-equality against that exported constant, not a literal.

**L6 deferred (not claimed in CI):** a real rootless Podman+runsc launch of the
orchestrator process with read-only rootfs + resource limits is operator-run on a
provisioned host.

---

### TC-085-02 — `spawn-worker` per-sub-goal gate: deny → no worker, denial reported (L2)

- **Requirement:** REQ-085-02
- **Level:** L2 (stub policy)

**Input:** A 2-sub-goal plan (`coding-agent: A`, `docs-fix: B`). A stub PolicyClient
that returns `allow` for `spawn-plan` but `deny` for the `spawn-worker` action on the
SECOND sub-goal's recipe (`docs-fix`) and `allow` on the first.

**Expected output (assertions):**
- The dispatch spy is called exactly ONCE (`spy.count() == 1`) — only `coding-agent`.
- `spy.recipeNames` does NOT contain `docs-fix` (the denied worker was NOT spawned).
- The PlanResult has 2 outcomes; the `docs-fix` outcome has `Success == false` and
  `Detail` contains `"policy"`/`"denied"` (denied outcome recorded).
- The Reporter received a denial message naming the denied recipe (`docs-fix`).
- The `spawn-worker` action name is issued with `Action.Name == "spawn-worker"` and
  `Resource.Type == "recipe"`, `Resource.ID == recipeName` (asserted via a recording
  policy stub that captures the requests).

**Fail-closed sub-case:** a policy stub returning an error for `spawn-worker` → that
worker is NOT spawned (deny on error).

---

### TC-085-03 — Fleet-wide audit chain covers both tiers in one chain (L2 + L5)

- **Requirement:** REQ-085-03
- **Level:** L2 (FakeSink coverage/ordering) + L5 (`audit-trail verify` real binary)

**Input (L2):** A 2-sub-goal plan, policy `allow` for plan and both workers. The
orchestrator is constructed `WithAuditSink(fakeSink)`. The dispatch seam is a spy that
ALSO appends two worker events (`containment`, `finish`) per dispatched worker to the
SAME `fakeSink` — modelling both worker tiers writing to the one chain.

**Expected output (assertions, L2):**
- The single `fakeSink` chain contains, in order:
  1. one `goal-intake` event (orchestrator)
  2. one `plan-decided` event (orchestrator)
  3. per worker (×2): a `spawn-decided` (orchestrator) then the worker's `containment`
     + `finish` events
  4. one `completion` event (orchestrator)
- Assert each expected action is present by `Action` value AND the counts:
  `goal-intake==1`, `plan-decided==1`, `spawn-decided==2`, `containment==2`,
  `finish==2`, `completion==1` — total ≥ 9 events in the ONE chain.
- The orchestrator `spawn-decided` for worker *i* appears BEFORE that worker's
  `containment` event (orchestrator decides, then the worker runs).

**Expected output (assertions, L5):** when `AGENT_BUILDER_AUDIT_BIN` resolves a real
`audit-trail` binary, replay the same fleet event sequence through a `BlockSink` to a
temp logfile, then `audit.VerifyChain` → `Valid == true`. When the binary is
unavailable, `t.Skip` with an explicit "L5 binary-deferred" log (the L2 FakeSink
coverage still asserts the single-chain property).

---

### TC-085-04 — Orchestrator egress posture is default-deny (L2)

- **Requirement:** REQ-085-04
- **Level:** L2 (unit) + L6 (live nftables probe) operator-deferred.

**Input:** Construct an Orchestrator (default options).

**Expected output (assertions):**
- `o.Containment().EgressPolicy == EgressDefaultDeny` (the exported constant
  `"default-deny"`).
- The same default-deny posture the worker boxes use (string-equality against the
  exported constant).

**L6 deferred (not claimed in CI):** a real connection from the orchestrator box to a
non-allowlisted host being blocked by nftables under rootless Podman is operator-run.

---

### TC-085-05 — Self-repo bright line: runtime deny + static fitness check both fire

- **Requirement:** REQ-085-05
- **Level:** L2 (runtime deny) + L3 (static fitness check)

**Input (runtime, L2):** A plan whose sub-goal carries `TargetRepo ==
"github.com/tkdtaylor/agent-builder"` (the own-repo). Policy stub returns `allow` for
everything (proving the guard is independent of the policy file).

**Expected output (runtime, assertions):**
- The own-repo worker is NOT dispatched (`spy.count() == 0` for that sub-goal).
- The outcome for that sub-goal has `Success == false` and `Detail` contains
  `"self-repo"` / names the own-repo (the orchestrator refused, fail-closed).
- A sub-case with `Sink == own-repo` (instead of `TargetRepo`) is also denied.
- A control sub-goal with a non-own-repo target IS dispatched (the guard is targeted,
  not a blanket deny).

**Input (static, L3):** Run the new `make fitness-no-self-repo-sink` check
(a) on the real registered recipes (clean) → exits 0; (b) on a violation fixture
(a recipe-source file declaring the own-repo as a result sink) → exits non-zero.

**Expected output (static, assertions):**
- The check passes on the current tree (no registered recipe targets the own-repo).
- The check FAILS (non-zero exit) on the violation fixture — proven by a Go test that
  invokes the detection logic against a fixture string containing the own-repo as a
  sink target and asserts a violation is reported.

---

## Verification plan

- **Highest level achievable in CI:** L2 (unit) + L3 (fitness). L5 (`audit-trail
  verify`) runs only when `AGENT_BUILDER_AUDIT_BIN` is present; otherwise it is
  recorded as L5-binary-deferred. L6 (live Podman/nftables) is operator-deferred for
  the containment/egress requirements (ADR 050 §3).
- **L2/L3 harness commands:**
  ```
  go test -count=1 ./internal/orchestrator/... ./internal/runtime/... ./internal/audit/...
  make fitness-supervisor-isolation
  make fitness-orchestrator-no-executor
  make fitness-no-self-repo-sink
  make check
  ```
  Expected: `ok`; all fitness `PASS`; `All fitness checks passed.`

## Out of scope

- Multi-worker concurrent dispatch (task 086) — this task secures the single,
  sequential-dispatch orchestrator.
- Key rotation for orchestrator signing keys.
- Live L6 Podman/nftables enforcement (operator-run, recorded as deferred).
</content>
</invoke>
