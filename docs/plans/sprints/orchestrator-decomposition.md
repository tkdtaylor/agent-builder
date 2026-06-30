# Orchestrator decomposition — sequencing plan

**Created:** 2026-06-27
**Last refreshed:** 2026-06-27 (after Cluster A + channel + envelope landed; execution reordered)
**Motivated by:** ADR 041 (agent-recipe seam), ADR 042 (secure two-tier orchestrator),
ADR 043 (executor registry + model router)
**Also governed by:** ADR 044 (recipe-seam binding correction), ADR 045 (secure channel:
Ed25519 envelope + X25519/AEAD + replay + armor channel-mode), ADR 046 (orchestrator core:
decomposition, reporting, persistence, approval, dispatch)

---

## Current state — RESUME HERE (2026-06-27)

**Done (✅ verified + merged to `main`):**

| Task | What | Notes |
|------|------|-------|
| 076 | Recipe type + registry (leaf) | ADR 044 corrected to config-taking factories |
| 077 | `runtime.Run` assembles all 4 IO seams from recipe | `stubResolveExecutor` placeholder (replaced by 095) |
| 078 | Runtime gate-existence assertion | `gate.Blocker` marker + assembly-time check |
| 079 | Docs-fix proof recipe | ADR 041 self-test passed (zero runtime/supervisor change) |
| 096 | `internal/envelope` crypto leaf | **new** (ADR 045): Ed25519 sign/verify + X25519/AEAD + ReplayCache; security-audited |
| 080 | Telegram channel adapter + armor | consumes `internal/envelope`; security-audited |
| 097 | Telegram channel hardening | **new**: SEC-001 size caps, SEC-002 guard timeout, SEC-003 typed sentinels |
| 094 | Local-model evaluation | 🟡 eval done (qwen3:8b generalist + qwen2.5-coder:7b coder retained); claude-CLI-via-proxy round-trip still unproven → confirm in **091** |

**ADRs accepted this arc:** 041, 042, 043, 044, 045, 046 (all merged).

**THE REORDER (owner decision, ADR 046):** the **executor-registry + router cluster
(087–095) now runs BEFORE the orchestrator (081–086).** Rationale: ADR 046's
LLM-assisted goal decomposition must reach the model through the ADR 043 router (never
`internal/executor`), but that router is still `stubResolveExecutor`. Building the
router first lets task 081 ship the **LLM planner from the start** instead of a
rule-based stopgap, and closes the 077 stub. (The rule-based `StructuredPlanner` from
ADR 046 §1 remains a valid fallback behind the `Planner` interface.)

**▶ NEXT ACTION — start the router cluster at task 087 (dependency-free root):**

```
use task-executor — task: docs/tasks/backlog/087-executor-registry-type.md, spec: docs/tasks/test-specs/087-executor-registry-type-test-spec.md
```

The whole router cluster (087–093, 095) is `Status: ready` (written against ADR 043 —
no architect-prep detour needed). After it lands, return to the orchestrator (081),
which by then has a real router to plan with.

**Process notes for the new session:**
- Each task: `scripts/start-task.sh` → 🟡 feat commit → **spec-verifier APPROVE (the gate)** →
  separate `verify:` commit (🟡→✅) → merge. For security-sensitive surfaces, also run the
  **security-auditor** before merge (it caught a real HIGH in 096 and mediums in 080).
- **Known infra bug:** the `no-commit-on-main` hook false-blocks commits made inside a
  worktree (it reads `CLAUDE_PROJECT_DIR` = main checkout, not the worktree branch).
  Workaround: `git -C <main> checkout --detach`, commit in the worktree, reattach. A
  proper fix to `.claude/scripts/no-commit-on-main.py` is a human-reviewed self-edit, not
  yet done.

**Pending design work before the orchestrator (081) can be executed:**
- **D-1 (from ADR 046): the channel is inbound-only.** `internal/channel/telegram`
  implements only `GoalSource.Next()`; there is no outbound/reply path. Approval
  solicitation (REQ-081-02) and result reporting (REQ-081-04) both need an **outbound
  `Reporter`/`sendMessage` seam** (with ADR-045 envelope encrypt+sign on replies). This
  needs its own task (next free ID **098**) before/with 081.
- **D-2 (from ADR 046): TC-081-05 must assert DIRECT imports**, not the transitive graph
  — `internal/runtime` legitimately imports `internal/executor`, and the orchestrator
  dispatching *through* `runtime.Run` is the blessed path. The task-planner must correct
  both the task AC and the TC when 081 is expanded.
- **081's stub spec must be expanded** per ADR 046 (Planner seam + LLM planner as v1
  target now that the router precedes it; typed `PlanResult`→text; in-memory state;
  pause-and-resume approval; dispatch reusing `runtime.Run`).

---

## Arc summary

1. **Recipe seam + orchestrator arc** (ADR 040/041/042/044/045/046): tasks 076–086
   plus new 096/097 (envelope + hardening) and a pending 098 (outbound Reporter seam).
   Cluster A (076–079) ✅; channel (080, 096, 097) ✅; orchestrator (081–086) backlog,
   now scheduled AFTER the router arc.
2. **Executor registry + model router arc** (ADR 043): tasks 087–095. **Now the active
   front.** 095 is the integration point that replaces the 077 stub with the real
   registry+router.

## Task ID map

### Arc 1 — Recipe seam, channel, orchestrator (ADR 041/042/044/045/046)

| Cluster | Task | Title | Status |
|---------|------|-------|--------|
| A | 076 | Recipe type + selector (config-taking factories — ADR 044) | ✅ |
| A | 077 | runtime.Run assembles from recipe (stub resolver) | ✅ |
| A | 078 | Runtime gate-existence assertion | ✅ |
| A | 079 | Docs-fix proof recipe (seam self-test) | ✅ |
| — | 096 | `internal/envelope` leaf (Ed25519 + X25519/AEAD + ReplayCache) — ADR 045 | ✅ |
| B | 080 | Telegram channel adapter + armor guard (consumes 096) | ✅ |
| — | 097 | Telegram channel security hardening (SEC-001/002/003) | ✅ |
| — | 098 | **(to create)** Outbound channel Reporter seam (ADR 046 D-1) | not yet a task |
| C | 081 | Orchestrator core (expand per ADR 046; LLM planner via router) | backlog — after Arc 2 |
| D | 082 | agent-builder worker recipe (code-authoring worker) | backlog (needs only Cluster A) |
| E | 083 | agent-mesh adoption (orchestrator↔worker transport) | backlog (reframed by ADR 045: consumes `internal/envelope`, not an `internal/agentmesh` IPC client) |
| F | 084 | memory-guard adoption (orchestrator goal/fleet state) | backlog |
| G | 085 | Orchestrator self-containment + policy gating + fleet audit | backlog |
| H | 086 | Multi-worker concurrent dispatch | backlog |

### Arc 2 — Executor registry + model router (ADR 043) — ACTIVE

| Cluster | Task | Title | Status |
|---------|------|-------|--------|
| I | 087 | Executor registry type + entry config (leaf) | ready ◀ NEXT |
| I | 088 | Vault-brokered per-provider auth | ready |
| I | 089 | Codex harness adapter | ready |
| I | 090 | Gemini harness adapter (**superseded: `gemini` CLI deprecated 2026-06-18 → `agy`/Antigravity, ADR 057; tasks 133/134**) | ready |
| I | 091 | Local entry + translation-proxy seam (proves claude-CLI-via-proxy — closes 094's gap) | ready |
| J | 092 | Router + capability/cost model + escalation | ready |
| J | 093 | Usage/quota tracking | ready |
| K | 094 | Local-model evaluation (operator-run) | 🟡 done (pending 091) |
| L | 095 | Recipe RoutingSpec wired to real router (replaces 077 stub) | ready |

## Dependency DAG

### Arc 2 — Executor registry + router (active)

```
087 (registry type + entry config)  ◀ NEXT
 ├─ 088 (vault per-provider auth)
 │   ├─ 089 (Codex harness adapter)    ─┐
 │   ├─ 090 (Gemini harness adapter)   ─┤─ 092 (router + capability/cost)
 │   └─ 091 (local entry + proxy seam) ─┘      └─ 093 (quota tracking)
 │        └─ (091 also closes 094's unproven claude-CLI-via-proxy round-trip)
 │                                                    └─ 095 (wire real router; also needs 077 ✅)
 └─ 094 (local-model eval — done; 091 confirms its proxy assumption)
```

### Arc 1 — remaining (orchestrator), scheduled AFTER Arc 2

```
[Cluster A ✅] + [080 ✅ / 096 ✅ / 097 ✅] + [Arc 2 router ✅ when done]
 ├─ 098 (outbound Reporter seam — ADR 046 D-1)   [create first]
 │   └─ 081 (orchestrator core — LLM planner via router; expand per ADR 046)
 │       ├─ 082 (agent-builder worker)   [also only needs Cluster A — can start anytime]
 │       ├─ 083 (agent-mesh adoption — consumes internal/envelope)
 │       ├─ 084 (memory-guard adoption)
 │       └─ 085 (containment+policy+audit) [needs 081,083,084]
 │           └─ 086 (multi-worker dispatch) [needs 081,083]
 └─ 082 (agent-builder worker) — independent of 081; needs Cluster A only
```

### Cross-arc dependency

Task 095 (wire real router) depends on Arc 1 task 077 (✅, the stub it replaces) and Arc 2
tasks 092 + 093. Task 081's LLM planner depends on the router being operational (092, and
ideally 095 wiring it into the assembly) — this is why Arc 2 was pulled ahead.

### Linear ordering (reordered — single executor)

**Now (Arc 2 first):** 087 → 088 → (089 ∥ 090 ∥ 091) → 092 → 093 → 095
  (094 eval already done; 091 confirms its proxy assumption.)

**Then (Arc 1 orchestrator):** 098 (Reporter seam) → 081 → 082 → 083 → 084 → 085 → 086
  (082 needs only Cluster A, so it can be pulled forward and run any time.)

**Parallel opportunities:**
- 089, 090, 091 can all run in parallel after 087+088 land.
- 082 (worker recipe) needs only Cluster A (✅) — it can be worked at any point, including
  in parallel with the router cluster.

## Open questions — status

| # | Question | Affects | Status |
|---|----------|---------|--------|
| OQ-1 | agent-mesh: Go library or binary IPC? | 080, 083, 096 | ✅ RESOLVED (ADR 045): agent-mesh is `package main`, signing-only, no sign/verify filter. Reimplemented as `internal/envelope` leaf (task 096), wire-compatible with agent-mesh's Envelope shape. 083 reframed to consume `internal/envelope`. |
| OQ-2 | Key distribution model for the channel | 080 | ✅ RESOLVED (ADR 045): two keypairs/side (Ed25519 sign + X25519 encrypt); static trusted-key set; manual out-of-band provisioning for v1. |
| OQ-3 | Goal decomposition: rule-based or LLM? | 081 | ✅ RESOLVED (ADR 046): `Planner` seam; LLM-assisted via the ADR-043 router is the v1 target (enabled by the reorder); rule-based `StructuredPlanner` is the fallback impl. Reaches the model through the router, never `internal/executor`. |
| OQ-4 | Report format back through channel | 081, 086 | ✅ RESOLVED (ADR 046): typed `PlanResult` aggregate in core, rendered to plain text at the channel edge. |
| OQ-5 | Generated recipe output format (`.go` vs struct literal) | 082 | OPEN — decide when 082 is expanded. |
| OQ-6 | memory-guard Go API vs binary IPC | 084 | OPEN — survey the memory-guard block (`~/Code/Public/memory-guard`) before 084; may need an ADR (same shape as OQ-1). |
| OQ-7 | Policy schema for orchestrator actions | 085 | OPEN — ADR 046 names two actions (`spawn-plan`/`spawn-worker` distinct from worker `run-task`); full schema TBD before 085. |
| OQ-8 | Codex CLI flag/output format | 089 | Near-blocking — stub for L2; confirm real CLI before L6. |
| OQ-9 | Gemini CLI flag/output format | 090 | Near-blocking — same as OQ-8. |
| OQ-10 | Translation-proxy choice (LiteLLM vs claude-code-router) | 091, 094 | 094 used LiteLLM (curl-validated only); 091 picks + proves the real claude-CLI round-trip. |
| OQ-11 | **NEW** — outbound channel transport (Telegram `sendMessage`) + reply-envelope wiring | 098, 081 | OPEN (ADR 046 D-1): the channel is inbound-only today; the Reporter seam (task 098) must add the outbound path with ADR-045 encrypt+sign on replies. |

## Cluster descriptions (current)

### Arc 2 — the active front

- **Cluster I (087–091) — Registry + adapters.** 087 is the leaf root (`RegistryEntry`,
  `HarnessDriver`, `QuotaBudget`, `Availability`, in-process catalog, `LoadFromEnv`,
  stdlib-only). 088 extends `secrets.SecretSource` with per-provider token resolution.
  089/090/091 are harness adapters (Codex, Gemini, local-via-proxy) parallelizable after
  087+088. 091 also closes task 094's unproven claude-CLI-via-proxy assumption.
- **Cluster J (092–093) — Router + quota.** 092 is the capability/cost-first router with
  the two-axis fallback (gate-fail → climb quality; quota-out → route sideways). 093 adds
  persistence, the injected `Clock` seam, reactive (429/Retry-After) + proactive budget
  exhaustion, and rolling-window recovery.
- **Cluster K (094) — Local eval.** ✅ Done (operator-run; two local entries retained).
  Re-runnable; see `docs/plans/sprints/094-local-model-benchmark.md`.
- **Cluster L (095) — Integration.** Replaces `stubResolveExecutor` (077) with the real
  registry+router. Zero-drift e2e check. Final Arc 2 task; unblocks 081's LLM planner.

### Arc 1 — orchestrator (after Arc 2)

- **098 (to create) — Outbound Reporter seam.** ADR 046 D-1. The channel's reply path
  (Telegram `sendMessage` + ADR-045 reply envelope). Prereq for 081's approval + report.
- **081 — Orchestrator core.** Expand the stub per ADR 046: `Planner` seam (LLM via
  router), typed `PlanResult`→text, in-memory state (memory-guard at 084), pause-and-resume
  approval over the verified channel, sequential dispatch reusing `runtime.Run`. Honor D-2
  (direct-import invariant check).
- **082 — agent-builder worker recipe.** Code-authoring as a worker task. Needs only
  Cluster A (✅) — can run any time. OQ-5 (output format) to resolve.
- **083 — agent-mesh adoption.** Reframed (ADR 045): consumes `internal/envelope` for
  orchestrator↔worker signed transport; if live A2A transport between worker processes is
  wanted, that's a distinct concern. Needs 081.
- **084 — memory-guard adoption.** Durable orchestrator goal/fleet state. OQ-6 survey first.
- **085 — containment + policy + audit.** Needs 081, 083, 084. OQ-7 (policy schema) first.
- **086 — multi-worker concurrent dispatch.** Concurrency over 081's sequential dispatch +
  083's transport.

## Scoping decisions (still in force)

- **Each cluster is one task file**, split further only when prerequisites land (the B–H
  stubs were deliberately coarse; 080 and 081 were expanded once their prerequisites
  merged — 080 via ADR 045 + planner, 081 via ADR 046 + a pending planner expansion).
- **Block API surveys + ADRs precede implementation**, not during (proved its worth: the
  agent-mesh survey for OQ-1 caught the non-importability before any code was written).
- **094 is operator-run, not CI-automatable**; code tasks 091/092/093 do not block on it.
- **The translation proxy is an external tool** (LiteLLM / claude-code-router); task 091
  names the seam and proves the round-trip; the operator runs the proxy.
