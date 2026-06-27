# Orchestrator decomposition — sequencing plan

**Created:** 2026-06-27
**Motivated by:** ADR 041 (agent-recipe seam) and ADR 042 (secure two-tier orchestrator)
**Branch:** `plan/orchestrator-decomposition`

## Summary

This plan decomposes the "builder of purpose-built agents" forward arc (ADR 040/041/042)
into 11 sequenced tasks (076–086). The eight task clusters map to a dependency DAG
rooted at Cluster A (the recipe seam), which is the critical path that unblocks
everything else.

## Task ID map

| Cluster | Letter | Task IDs | Title | Status |
|---------|--------|----------|-------|--------|
| A | Recipe seam + selectable IO seams | 076 | Recipe type + in-process selector | backlog |
| A | Recipe seam + selectable IO seams | 077 | runtime.Run assembles from a recipe | backlog |
| A | Recipe seam + selectable IO seams | 078 | Runtime gate-existence assertion for generated recipes | backlog |
| A | Recipe seam + selectable IO seams | 079 | Docs-fix recipe (second proof recipe) | backlog |
| B | Telegram channel adapter | 080 | Telegram channel adapter + Ed25519 envelope + armor guard | backlog |
| C | Orchestrator core | 081 | Orchestrator core | backlog |
| D | agent-builder worker recipe | 082 | agent-builder worker recipe (code-authoring worker) | backlog |
| E | agent-mesh adoption | 083 | agent-mesh adoption (orchestrator↔worker transport) | backlog |
| F | memory-guard adoption | 084 | memory-guard adoption (orchestrator goal/fleet state) | backlog |
| G | Orchestrator containment + policy + audit | 085 | Orchestrator self-containment + policy gating + fleet audit | backlog |
| H | Multi-worker concurrent dispatch | 086 | Multi-worker concurrent dispatch | backlog |

## Dependency DAG

```
076 (recipe type)
 └─ 077 (runtime assembles from recipe)
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

### Linear ordering (one possible sequencing for a single executor)

1. 076 → 2. 077 → 3. 078 → 4. 079 → 5. 080 → 6. 082 → 7. 081 → 8. 083 → 9. 084 → 10. 085 → 11. 086

Note: 080 (channel adapter) and 082 (worker recipe) can be worked in parallel after
079 (both need only Cluster A, not each other). 083 and 084 can likewise be worked
in parallel after 081.

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
