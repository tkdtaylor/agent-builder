# Orchestrator decomposition — sequencing plan

**Created:** 2026-06-27
**Motivated by:** ADR 041 (agent-recipe seam), ADR 042 (secure two-tier orchestrator),
ADR 043 (executor registry + model router)
**Branch:** `plan/orchestrator-decomposition`

## Summary

This plan decomposes two forward arcs:

1. **The recipe seam + orchestrator arc** (ADR 040/041/042): 11 tasks (076–086).
   Cluster A (recipe seam, 076–079) is the critical path that unblocks everything.
2. **The executor registry + model router arc** (ADR 043): 9 tasks (087–095).
   ADR 043 amends ADR 041 — tasks 076 and 077 are updated at source to use `RoutingSpec`
   instead of `ExecutorFactory`. Task 095 is the final integration point that replaces
   the stub resolver from 077 with the real registry+router.

## Task ID map

### Arc 1 — Recipe seam + orchestrator (ADR 041/042)

| Cluster | Letter | Task IDs | Title | Status |
|---------|--------|----------|-------|--------|
| A | Recipe seam + selectable IO seams | 076 | Recipe type + in-process selector (RoutingSpec — ADR 043 amended) | backlog |
| A | Recipe seam + selectable IO seams | 077 | runtime.Run assembles from a recipe (stub resolver) | backlog |
| A | Recipe seam + selectable IO seams | 078 | Runtime gate-existence assertion for generated recipes | backlog |
| A | Recipe seam + selectable IO seams | 079 | Docs-fix recipe (second proof recipe) | backlog |
| B | Telegram channel adapter | 080 | Telegram channel adapter + Ed25519 envelope + armor guard | backlog |
| C | Orchestrator core | 081 | Orchestrator core | backlog |
| D | agent-builder worker recipe | 082 | agent-builder worker recipe (code-authoring worker) | backlog |
| E | agent-mesh adoption | 083 | agent-mesh adoption (orchestrator↔worker transport) | backlog |
| F | memory-guard adoption | 084 | memory-guard adoption (orchestrator goal/fleet state) | backlog |
| G | Orchestrator containment + policy + audit | 085 | Orchestrator self-containment + policy gating + fleet audit | backlog |
| H | Multi-worker concurrent dispatch | 086 | Multi-worker concurrent dispatch | backlog |

### Arc 2 — Executor registry + model router (ADR 043)

| Cluster | Sub | Task IDs | Title | Status |
|---------|-----|----------|-------|--------|
| I | Registry + entry config | 087 | Executor registry type + entry config | backlog |
| I | Vault per-provider auth | 088 | Vault-brokered per-provider auth | backlog |
| I | Codex harness | 089 | Codex harness adapter | backlog |
| I | Gemini harness | 090 | Gemini harness adapter | backlog |
| I | Local entry | 091 | Local entry + translation-proxy seam | backlog |
| J | Router | 092 | Router + capability/cost model + escalation | backlog |
| J | Quota tracking | 093 | Usage/quota tracking | backlog |
| K | Local eval | 094 | Local-model evaluation (operator-run, hardware-specific) | backlog |
| L | Integration | 095 | Recipe RoutingSpec wired to real router (replaces 077 stub) | backlog |

## Dependency DAG

### Arc 1 — Recipe seam + orchestrator

```
076 (recipe type + RoutingSpec)
 └─ 077 (runtime assembles from recipe — stub resolver)
     └─ 078 (gate-existence assertion)
         └─ 079 (docs-fix proof recipe)
             ├─ 080 (Telegram channel adapter)      [Cluster B]
             │   └─ 081 (orchestrator core)          [Cluster C]
             │       ├─ 082 (agent-builder worker)   [Cluster D — also needs 078]
             │       ├─ 083 (agent-mesh adoption)    [Cluster E]
             │       ├─ 084 (memory-guard adoption)  [Cluster F]
             │       └─ 085 (containment+policy+audit) [Cluster G — needs 081,083,084]
             │           └─ 086 (multi-worker dispatch) [Cluster H — needs 081,083]
             └─ 082 (agent-builder worker)           [also needs 076,077,078]
```

### Arc 2 — Executor registry + router

```
087 (registry type + entry config)
 ├─ 088 (vault per-provider auth)
 │   ├─ 089 (Codex harness adapter)    ─┐
 │   ├─ 090 (Gemini harness adapter)   ─┤─ 092 (router + capability/cost)
 │   └─ 091 (local entry + proxy seam) ─┘      └─ 093 (quota tracking)
 │                                                     └─ 095 (wire real router — depends also on 077)
 └─ 094 (local-model evaluation — operator-run; parallel with 092,093)
```

### Cross-arc dependency

Task 095 (wire real router) depends on both arcs:
- From Arc 1: task 077 (stub resolver it replaces)
- From Arc 2: tasks 092 + 093 (the real router + quota tracking)

### Linear ordering (one possible sequencing for a single executor)

**Arc 1:** 076 → 077 → 078 → 079 → 080 → 082 → 081 → 083 → 084 → 085 → 086

**Arc 2:** 087 → 088 → 089 → 090 → 091 → 092 → 093 → 095
(094 can run in parallel with 092/093 once 091 is done)

**Parallel opportunities within Arc 2:**
- 089, 090, 091 can all be worked in parallel after 087+088 land.
- 094 can be worked in parallel with 092+093 once 091 lands.
- Arc 1 (076–079) and Arc 2 (087–091) can be worked in parallel from the start.

Note: 095 is the final integration point and must wait for both arcs (077 done + all
of 087–093 done).

## Cluster descriptions and rationale

### Cluster A — Recipe seam + selectable IO seams (076–079) [CRITICAL PATH]

Four tasks in strict dependency order. This is the unblocker for every subsequent
cluster and is fully specified for ready-to-execute delivery:

- **076** — the `Recipe` type, the `GoalSource`/`ResultSink` interface definitions,
  and the registry mechanism (`Register`/`SelectRecipe`/`ListRecipes`). Leaf package:
  imports only `internal/supervisor` (for the existing `Executor`/`Gate` interfaces)
  plus stdlib. No concrete seam registration; the coding-agent recipe is NOT
  registered here. Tests use fake recipes only.
- **077** — two coupled things: (1) register the `"coding-agent"` recipe (the
  binding of `tasksource`/`executor`/`gate`/`publisher` concretes, which lives in
  `internal/runtime` — the only package that already imports them); (2) make
  `runtime.Run` a thin assembler that calls `SelectRecipe` instead of hardwiring
  concretes inline. Pure refactor; all existing tests must pass without modification.
  `SelectRecipe("coding-agent")` resolving is a task-077 assertion. Depends on 076.
- **078** — the runtime gate-existence assertion for generated recipes (ADR 042
  amendment to ADR 041). Defensive pre-flight check before dispatch. Depends on 077.
- **079** — the docs-fix second proof recipe. ADR 041 self-test: if adding it requires
  changing `internal/runtime`, the seam design failed. Depends on 076, 077, 078.

**Why split 076/077 rather than one task?** Task 076 must be a leaf (no concrete
imports). But the coding-agent registration requires importing all four concretes.
Those two constraints are mutually exclusive if both live in one task. Splitting puts
the leaf-pure type/registry in 076 and the concrete-binding registration in 077
(where `internal/runtime` already imports all concretes). The test isolation also
means that if the type design needs revision, 077 is unaffected, and vice versa.

**Why split 078 (gate assertion) from 077 (runtime refactor)?** The gate assertion
is a security hardening step motivated by ADR 042; the refactor (077) is a behavior-
preserving structural change. Mixing them in one task would make the behavioral
regression guard (077) harder to read and the security property harder to verify.

### Cluster B — Telegram channel adapter (080) [depends on Cluster A]

One task: the Telegram bot + Ed25519 envelope + armor guard, satisfying the
`GoalSource` interface. Detailed test cases are stubs until Cluster A lands (the
`GoalSource` seam interface shape is not yet defined). Three open questions in the
test spec must be resolved before implementation: key distribution model, replay
prevention window, and whether agent-mesh exposes a Go library or requires binary IPC.

### Cluster C — Orchestrator core (081) [depends on Cluster A + B]

One task: `internal/orchestrator`. Goal intake → plan → approval → dispatch →
aggregate → report. The orchestrator is purely additive (`internal/supervisor`
unchanged). Two open questions in the test spec must be resolved: goal decomposition
strategy (LLM-assisted vs rule-based) and report format. These are the decisions
that most affect downstream cluster D/E/F/G scope.

### Cluster D — agent-builder worker recipe (082) [depends on Cluster A; can parallel with B,C]

One task: the "agent-builder-worker" recipe. Depends on Cluster A (recipe seam +
gate-existence assertion) but does NOT depend on Cluster B or C — it can be developed
in parallel with 080 and 081. Two open questions: generated output format and delivery
mechanism (PR vs agent-mesh return).

### Cluster E — agent-mesh adoption (083) [depends on Cluster C]

One task: `internal/agentmesh` leaf adapter + fitness check. Depends on orchestrator
core (081); the block API must be surveyed before the task spec can be fully filled in.
An ADR may be needed.

### Cluster F — memory-guard adoption (084) [depends on Cluster C, can parallel with E]

One task: `internal/memoryguard` leaf adapter + fitness check. Depends on
orchestrator core (081); can be developed in parallel with 083. Block API survey
needed; ADR may be needed.

### Cluster G — Orchestrator containment + policy + audit (085) [depends on C, E, F]

One task combining three tightly-coupled concerns (exec-sandbox containment,
policy-engine gating, fleet-wide audit) that all touch the orchestrator's run config.
Depends on 081, 083, 084. The policy schema for orchestrator actions must be defined
before implementation.

### Cluster H — Multi-worker concurrent dispatch (086) [depends on C, E]

One task: concurrency layer on top of the sequential dispatch from 081 and the
signed-envelope transport from 083. The concurrency model (goroutines + channels vs
worker-pool) and partial-failure policy must be decided before implementation.

### Cluster I — Registry + adapters (087–091) [first tasks in Arc 2]

Five tasks, mostly parallelizable after the first two land:

- **087** — the `RegistryEntry` struct, `HarnessDriver` discriminator, `QuotaBudget`,
  `Availability` types, the in-process catalog, and `LoadFromEnv()`. Leaf package
  (`internal/registry`): stdlib-only. Critical-path root for Arc 2.
- **088** — extend `secrets.SecretSource` with `NamedProviderToken(ref string)` so
  each entry's `SecretRef` can be resolved independently. Additive — existing
  `ProviderToken()` unchanged. Depends on 087.
- **089** — `executor.CodexCLI` adapter (new harness). Depends on 087+088.
- **090** — `executor.GeminiCLI` adapter (new harness). Depends on 087+088.
  Parallel with 089.
- **091** — local entry config + translation-proxy seam documentation. Not a new
  harness: uses the existing `ClaudeCLI` pointed at a local proxy endpoint. Extends
  `ClaudeCLI` to accept a `RegistryEntry`. Depends on 087+088. Parallel with 089/090.

### Cluster J — Router + quota tracking (092–093)

Two tasks in dependency order:

- **092** — the `router.Router` with capability/cost-first selection, the two-axis
  fallback (gate failure = climb quality ladder; quota exhaustion = route sideways),
  in-memory state only. Depends on 087+089+090+091 (needs adapters to construct).
- **093** — adds persistence (file), the injected `Clock` seam for deterministic
  testing, reactive exhaustion (429/`Retry-After`), proactive budget checks, and the
  rolling-window auto-recovery. Depends on 092.

### Cluster K — Local-model evaluation (094) [parallel with J]

One operator-run task: empirical benchmark on the target hardware
(Intel Core Ultra 9 185H / 62 GiB RAM / RTX 4060 Laptop 8 GB VRAM / Ubuntu 26.04
/ CUDA). Finds the highest-capability model that fits in VRAM for the local entry.
Output is config (`ModelID`, `Endpoint`), not code. Verification is L5/L6 operator-
observed (recorded in the verify commit). Can be worked in parallel with 092/093
once 091 lands.

### Cluster L — Integration (095)

One task: replaces the `stubResolveExecutor` from task 077 with the real
registry+router. Zero-drift check: existing e2e tests pass. Depends on 077 (the stub)
and 087+092+093 (the real router). Final task in Arc 2.

## Open questions (blocking or near-blocking)

| # | Question | Affects | Blocking? |
|---|----------|---------|-----------|
| OQ-1 | Does agent-mesh expose a Go library or require binary IPC? | 080, 083 | Yes — determines whether `internal/envelope` and `internal/agentmesh` wrap a binary (like `internal/audit`) or link a Go package |
| OQ-2 | Key distribution model for Telegram↔orchestrator Ed25519 keys | 080 | Yes — must be decided before the adapter can be tested |
| OQ-3 | Goal decomposition strategy: rule-based or LLM-assisted? | 081 | Yes — if LLM-assisted, the orchestrator imports an executor interface, which may affect the import-graph invariant |
| OQ-4 | Report format back through channel (plain text / structured JSON / typed Result) | 081, 086 | Near-blocking — affects aggregation and channel serialization |
| OQ-5 | Generated recipe output format: `.go` source file or struct literal? | 082 | Yes — determines how the gate-existence assertion inspects the output |
| OQ-6 | memory-guard Go API vs binary IPC | 084 | Yes — same concern as OQ-1 for agent-mesh |
| OQ-7 | Policy schema for orchestrator actions (what subjects/actions does policy-engine gate?) | 085 | Near-blocking — the policy schema must be specified before the decide-gate can be wired |
| OQ-8 | Codex CLI exact flag/output format (argv, branch output convention) | 089 | Near-blocking — the adapter must know the subprocess protocol; the test uses a stub, but the real CLI interface must be confirmed before L6 |
| OQ-9 | Gemini CLI exact flag/output format | 090 | Near-blocking — same as OQ-8 for Gemini |
| OQ-10 | Translation-proxy choice: LiteLLM vs claude-code-router vs other | 091, 094 | Near-blocking for 094 operator run — both are known to work; pick one and document in the verify commit |

## Scoping decisions

- **Cluster A is fully specified; B–H are coarse-grained stubs.** This is deliberate:
  the shape of B–H depends on how A lands (which seam interfaces it defines). Over-
  specifying B–H now would create fictional requirements that conflict with the actual
  A interfaces. Each cluster gets split further when its prerequisites merge.

- **Each cluster is one task file.** The ADR-040/041/042 arc is large, but each
  cluster has a clear, single responsibility. Breaking each cluster into sub-tasks
  now would be premature: the sub-task boundaries depend on implementation choices
  (e.g. whether agent-mesh is library or binary IPC) that are open questions.

- **Cluster D (agent-builder worker recipe) can parallel with B and C.** It only
  needs Cluster A (076–079). This is the fastest path to proving code-authoring as
  a worker-tier operation.

- **ADR decisions before starting B–H.** Each cluster's "Readiness gate" lists the
  open questions and surveys needed. Block API surveys (agent-mesh, memory-guard) and
  any resulting ADRs should precede implementation, not be done during it.

- **Arc 2 (087–095) is parallel with Arc 1 (076–079).** 087 and 088 depend on
  nothing but stdlib (087 is a leaf; 088 depends on 087). Arc 1 and Arc 2 can be
  worked in parallel from the start. Task 095 is the only cross-arc dependency.

- **Tasks 076 and 077 are amended at source, not superseded.** ADR 043 changes the
  executor seam field from `ExecutorFactory` to `RoutingSpec`. Since neither 076 nor
  077 is implemented yet, the task files and test specs are updated in place to reflect
  the ADR 043 amendment. This avoids the waste of building `ExecutorFactory` only to
  remove it in task 095.

- **Task 094 (local-model evaluation) is operator-run, not CI-automatable.** The
  hardware is specific (RTX 4060 Laptop) and the benchmark is empirical. It is
  deliberately separated from the code tasks so CI does not block on it, and the
  code tasks (091, 092, 093) do not block on 094 either.

- **The translation proxy is an external tool.** LiteLLM, claude-code-router, and
  similar are not built or shipped by this repo. Task 091 names the seam and the
  pattern; the operator chooses and operates the proxy.
