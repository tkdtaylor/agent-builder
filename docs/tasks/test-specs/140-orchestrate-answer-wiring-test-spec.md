# Test spec — Task 140: orchestrate answer wiring (analyzer + answerer)

**Task:** `docs/tasks/backlog/140-orchestrate-answer-wiring.md`
**Relates to:** ADR 060, tasks 138/139, ADR 059 (Completer).

## Context

Wires the answer route live: injects a `GoalAnalyzer` and a Completer-backed `Answerer` into the
orchestrate assembly. Opt-in via `AGENT_BUILDER_GOAL_ANALYSIS` (unset/false = pre-060 coding behavior,
preserving the existing coding pipeline + its tests). `cliAnswerer` selects a brain by complexity
(simple→cap 1, complex→cap 2) and answers via `executor.CompleterForEntry`.

## Requirements

- **REQ-140-01** — `goalAnalyzerFromEnv(getenv)` returns a `*HeuristicGoalAnalyzer` when
  `AGENT_BUILDER_GOAL_ANALYSIS` ∈ {true,1,yes,heuristic,on} (case-insensitive), else `nil`.
- **REQ-140-02** — `cliAnswerer` satisfies `orchestrator.Answerer`; `Answer` derives the routing
  `MinCapability` from complexity (simple→1, complex→2), selects an entry via the router over the brain
  catalog (`buildBrainCatalog`), and returns `CompleterForEntry(entry).Complete(...)`.
- **REQ-140-03** — The orchestrate assembly passes `WithGoalAnalyzer(goalAnalyzerFromEnv(getenv))` and
  `WithAnswerer(cliAnswerer{})`; with the flag off the analyzer is nil (coding-only) and the full
  existing orchestrate suite stays green.
- **REQ-140-04 (L6)** — Live: with `AGENT_BUILDER_GOAL_ANALYSIS=true` and a local brain, driving
  `orchestrate` with `GOAL_SPEC="What is the capital of France?…"` prints an answer containing "Paris"
  over the channel (not a "which repository?" prompt).

## Test cases

- **TC-140-01** (`TestGoalAnalyzerFromEnv`) — REQ-140-01: table of env values → heuristic-or-nil.
- **TC-140-02** (`TestCliAnswererSatisfiesSeam`) — REQ-140-02: compile-time `var _
  orchestrator.Answerer = cliAnswerer{}` + non-nil.
- **TC-140-03 (L3)** — `make check` green with the flag off (existing orchestrate tests unaffected).
- **TC-140-04 (L6, operator)** — the live orchestrate answer described in REQ-140-04.

## Non-vacuous / negative controls

- TC-140-01 asserts BOTH an enabling value → non-nil heuristic AND an empty/false value → nil (so the
  default-off gate is proven, not just the on path).
