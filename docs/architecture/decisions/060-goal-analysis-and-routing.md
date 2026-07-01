# ADR 060 — Goal analysis & routing in the orchestrator

**Status:** Accepted
**Date:** 2026-06-30
**Relates to:** ADR 058 (conversational front door / Clarifier), ADR 046 (planner/dispatch), ADR 043 (capability/cost router), ADR 053/059 (single-shot Completer + `ask`). Extends the general (non-coding) execution path onto the `orchestrate` front door.

## Context

The `orchestrate` front door treats **every** goal as a coding task. A goal flows
intake → clarify → plan → policy gate → approval → dispatch a worker that edits a repo
and returns a branch. Even the clarifier is coding-shaped: `HeuristicClarifier` asks
*"Which repository should I work on?"* for any goal without a repo — so a plain question
like *"what is the capital of France?"* is met with a demand for a repository, not an
answer.

ADR 059 built the non-coding path (single-shot `Completer` for all three brains) and the
`ask` subcommand, but `orchestrate` — the unified goal-intake channel (CLI now, Telegram
next) — cannot reach it. The general agent's front door should take *any* goal and figure
out what to do with it, not require the operator to pre-classify it or use a separate
subcommand.

## Decision

Add a **generic goal-analysis step in the orchestrator** that classifies an incoming goal
and routes it, before the coding-centric clarify/plan flow runs.

**1. `GoalAnalyzer` seam.** A new orchestrator seam:

```go
type GoalAnalysis struct {
    Kind       GoalKind       // KindAnswer (non-coding question) | KindCoding (build/act)
    Complexity GoalComplexity // ComplexitySimple | ComplexityComplex
    Rationale  string         // short human-readable why (for audit/report)
}
type GoalAnalyzer interface {
    Analyze(goal supervisor.Task) (GoalAnalysis, error)
}
```

**2. Route on `Kind` at intake** (`BeginGoal`, before `ClarifyAndReport`):

- **`KindAnswer`** → the **general-answer route**: select a brain and call the single-shot
  `Answerer` (a Completer-backed seam), then `Reporter.Report(answer)`; `StateDone`. It
  **skips the coding clarifier, planning, the spawn-policy gate, and approval** — it is
  read-only single-shot inference (no worker, no tools, no repo mutation), so there is no
  *action* to authorize. It is audited (`ActionCompletion`) and the brain is chosen by the
  router (sensitivity/quota/cost) at a capability tier derived from `Complexity`.
- **`KindCoding`** → the existing flow unchanged (clarify → plan → policy gate → approval →
  dispatch). `Complexity` raises the plan's routing-spec `MinCapability` floor, so a complex
  build routes to a stronger brain and a simple one can use the local/cheap backstop.

**3. `Complexity` → brain capability.** `ComplexitySimple → MinCapability 1` (local/cheap
eligible), `ComplexityComplex → a higher floor`. This is the "route based on the goal and its
complexity" control: cheap brains answer easy things; strong brains handle hard ones. The
router (ADR 043) still makes the final cost/quota/availability choice within that floor.

**4. Two implementations, env-selectable** (mirrors the Clarifier, ADR 058):

- **`HeuristicGoalAnalyzer`** (default, deterministic, no IO): a question that names no
  repo/path and reads as an information request (interrogative, no build/imperative verb) →
  `KindAnswer`; a goal naming a repo/path or an imperative build/change verb → `KindCoding`.
  Length/scope heuristics set `Complexity`. Small and unit-testable; it is the seam's floor.
- **`LLMGoalAnalyzer`** (`AGENT_BUILDER_ANALYZER=llm`): reuses the `Invoker`/`Completer` seam
  (as the LLM clarifier/planner do) to classify with a model, parsing a small structured
  response into `GoalAnalysis`. F-010/F-014 stay green (the orchestrator never imports
  `internal/executor`; the Invoker is wired in `internal/cli`).

**5. `Answerer` seam.** `Answer(ctx, prompt) (string, error)`, injected via `WithAnswerer`,
built in `internal/cli` from the completer seams (router-selected entry → `CompleterForEntry`
→ `Complete`). Keeps `internal/executor` out of `internal/orchestrator`.

## Why this shape

- **The agent decides, not the operator.** A generic analyzer at the front door is what makes
  it a *general* agent: one channel, any goal, routed by what the goal actually is and how
  hard it is — not a coding pipeline with a manual `ask:` escape hatch.
- **Reuses every existing seam** — Clarifier pattern for the analyzer, Invoker/Completer for
  the LLM path and the answer, the router for capability/cost, the Reporter for the channel.
  No new transport, no reimplemented reasoning.
- **Heuristic floor keeps it deterministic and testable**; the LLM path makes it robust.

## Consequences

- `orchestrate` now answers general questions over the channel (the L6 target: send
  *"what is the capital of France?"* with no repo → an answer, not *"which repository?"*).
- **Approval bypass for the answer route** is a deliberate, documented security decision: the
  route takes no action (single-shot, no tools, no worker, no repo write), so the action-approval
  gate has nothing to authorize. It remains audited and sensitivity-routed. The two human gates
  still apply to every `KindCoding` goal (which is where actions happen). If an operator wants to
  gate answers too, that is a future opt-in flag; the default is answer-freely (read-only).
- Misclassification risk: a coding goal wrongly classified `KindAnswer` would get a text
  reply instead of a build. Mitigations: the heuristic is conservative (repo/path or build-verb
  → coding), the rationale is reported, and the LLM analyzer improves accuracy. A wrong answer
  is low-blast-radius (no repo mutation); the operator can re-issue.

## Alternatives considered

- **Explicit `ask:` recipe prefix** (operator marks the goal kind). Rejected as the primary
  mechanism — it pushes classification onto the human and isn't a *general* agent. (It could
  remain as an explicit override; not built in this ADR.)
- **Fold analysis into the Clarifier.** Rejected — the clarifier answers "is this goal clear
  enough to act on?"; analysis answers "what kind of goal is it and how hard?". Distinct
  responsibilities; a separate seam keeps each single-purpose (Unix philosophy).
