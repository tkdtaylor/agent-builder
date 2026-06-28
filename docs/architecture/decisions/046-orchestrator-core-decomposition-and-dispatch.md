# ADR 046 — Orchestrator core: decomposition strategy, reporting, persistence, approval, and dispatch

**Status:** Accepted (2026-06-27) — design-only. Resolves the open design questions
blocking task 081 (orchestrator core) so its stub test spec can be expanded into real
assertions. No code, spec, or diagram changes land with this ADR.
**Date:** 2026-06-27

**Owner decisions (sign-off 2026-06-27):**
- **The design is ACCEPTED** as recommended: a `Planner` seam with a rule-based
  `StructuredPlanner` as the v1 implementation; typed `PlanResult` rendered to text at
  the channel edge; in-memory plan state (durable + memory-guard at task 084); approval
  as pause-and-resume over the envelope-verified + armor-guarded channel; one worker per
  sub-goal, sequential, reusing `runtime.Run`'s per-worker assembly.
- **Execution is REORDERED:** rather than ship the rule-based v1 first and add the LLM
  planner later, the **executor-registry + quota-aware router cluster (tasks 087–095) is
  pulled forward to run BEFORE the orchestrator (task 081)**. This lets task 081 ship the
  **LLM-assisted `Planner`** from the start (Option B in §1) instead of the rule-based
  stopgap, because the router it depends on will already exist — and it closes the
  `stubResolveExecutor` loose end from task 077. The `Planner` seam abstraction from §1
  still stands; the rule-based `StructuredPlanner` remains a valid fallback/first
  implementation behind it, but the LLM planner is no longer deferred to a separate
  post-081 task — it is unblocked by the reordered router cluster.
- **Drift D-1 (no outbound channel/Reporter seam) and D-2 (TC-081-05 must assert DIRECT
  imports, not the transitive graph) are accepted as binding** and must be addressed when
  task 081 is expanded/executed.
**Motivated by:** task 081's stub test spec, whose "Open questions" defer three
load-bearing decisions (goal-decomposition strategy, report format, plan-state
persistence) plus two wiring decisions implied by the requirements (how the
`require_approval` gate is presented + how approval returns, and the worker-dispatch
shape). This ADR is the design-prep that resolves all five before any code is written.
**Extends ADR 042 (does not contradict it).** ADR 042 defines the orchestrator as Tier 1
— receive a human goal, decompose, gate on `require_approval`, dispatch purpose-built
workers over the recipe seam, aggregate, report; **the orchestrator authors no code**,
and **no agent at any tier edits agent-builder's own repo**. ADR 042 left *how* the
orchestrator decomposes a goal and *how* it selects an executor for that step as open
questions; ADR 043 answered the executor-selection half (route through the registry).
This ADR answers the decomposition-strategy half and the four concrete wiring questions
081 must settle. It **supersedes one narrow point of ADR 042's implied flow**: ADR 042
says "the orchestrator decomposes [the goal]" without committing to *how*; this ADR
commits v1 (task 081) to a **structured / rule-based decomposition with an LLM-assisted
seam designed but not wired**, and explicitly defers the autonomous-LLM-planner build to
a separate task (§1, §6). ADR 042's two-tier model, its bright lines, and all seven
load-bearing invariants are preserved unchanged.

## Context

Tasks 076–079 made the recipe seam real and stable (`recipe.SelectRecipe`,
`recipe.Recipe` with config-taking factories per ADR 044). Task 080 built the inbound
channel (`internal/channel/telegram`) as a `supervisor.GoalSource` over the
`internal/envelope` crypto primitive (ADR 045). What does not yet exist is the layer
*above* `runtime`/`supervisor` that ADR 042 named Tier 1: a component that takes a goal,
turns it into a multi-step plan, gates that plan on human approval, and dispatches one
worker per sub-goal.

Reading the code as it stands today surfaces five concrete facts that shape this design:

1. **`supervisor.Task` carries `ID`/`Repo`/`Spec` — there is no `Goal` field.** The
   Telegram adapter (`internal/channel/telegram/adapter.go:182`) maps the human's goal
   text into `Task.Spec` and leaves `Repo` empty. A "goal" today *is* a `supervisor.Task`
   with the goal text in `Spec`. The orchestrator's "plan" must therefore be a list of
   *sub-goals*, each ultimately expressible as a `supervisor.Task` for a worker.

2. **`require_approval` already has a concrete mechanism, but the existing wiring is a
   terminal halt, not a resumable gate.** `policy.DecisionRequireApproval` exists
   (`internal/policy/client.go:49`), and `runtime.decideGate`
   (`internal/runtime/run.go:697`) handles it by writing a `needs-human` task status and
   returning — the box never starts and the run *ends*. The orchestrator needs a
   *different* shape: present the plan, **pause**, and resume dispatch when approval
   arrives. The `policy.Decide` call and the `DecisionRequireApproval` enum are reusable;
   the terminal-halt control flow in `runtime.Run` is not.

3. **The channel is inbound-only today — there is no outbound/report path.** The Telegram
   adapter implements `supervisor.GoalSource.Next()` (pull a goal in); it has **no**
   method to send a message *back* to the human. REQ-081-04 ("summary reported back
   through the channel") and REQ-081-02 ("approval solicited via the channel") both
   require an outbound seam that **does not exist yet**. This is a real gap, not an
   oversight in 081 — see §2 and §6 (the planner must add an outbound `ResultSink`-style
   seam to the channel, likely as a slice of 081 or a small predecessor task).

4. **The executor router (ADR 043) is still a stub.** `runtime.stubResolveExecutor`
   (`internal/runtime/run.go:353`) always returns the Claude CLI executor and is marked
   "replaced by the real registry+router in task 095." So today the *only* path to an
   executor runs through `internal/executor` — which REQ-081-05 **forbids the orchestrator
   from importing.** This is the crux of the decomposition fork (§1): if the orchestrator
   needs an LLM to plan, it cannot reach one through the existing executor wiring without
   importing the forbidden package, and the router that *would* give it a clean path does
   not exist yet.

5. **`runtime.Run` is the existing per-worker assembly.** Everything ADR 042 calls a
   "worker" — recipe → four IO seams → containment box → supervisor → gate → publish — is
   exactly `runtime.Run(config, stdout)` today. The orchestrator dispatching a worker
   *is* invoking this assembly once per sub-goal. The orchestrator must **reuse**
   `runtime.Run` (or a thin function it already exposes), not reimplement supervisor
   assembly (REQ-081-06: `internal/supervisor` unchanged).

## Decision

Five decisions for the orchestrator core, each grounded in the facts above.

### 1. Goal-decomposition strategy — **rule-based / structured decomposition for v1, behind a `Planner` seam that an LLM-assisted planner can implement later without changing the orchestrator or touching its import invariant**

This is the key fork. Two genuine options:

#### Option A — Rule-based / structured decomposition (no LLM in the orchestrator)

**One-sentence description.** The orchestrator decomposes a goal into sub-goals by
deterministic logic — splitting on a structured plan the human supplies, or pattern-rules
over the goal text — and imports no model seam at all.

**Implementation sketch.** Define a leaf `Planner` interface in `internal/orchestrator`:
`Plan(goal supervisor.Task) (Plan, error)`, where `Plan` is an ordered
`[]SubGoal` and each `SubGoal` names a recipe (e.g. `"coding-agent"`, `"docs-fix"`) plus
the per-sub-goal `supervisor.Task` payload. The v1 concrete is a `StructuredPlanner`:
if the goal text is already a structured plan (e.g. a small JSON/line list the human's
companion client emitted), it parses it into sub-goals 1:1; otherwise it produces a
single-sub-goal plan (the whole goal → one worker on the default recipe). No LLM call,
no network, no executor import. The orchestrator depends only on
`internal/recipe`, `internal/supervisor`, `internal/policy`, the channel's outbound seam,
and `internal/audit`.

- **Pros**
  - REQ-081-05 holds **trivially and by construction** — there is no executor/model
    import anywhere in the decomposition path; the import-graph assertion is easy to keep
    green.
  - Fully deterministic → every TC-081-0x becomes a hard assertion (exact sub-goal count,
    exact recipe names) with no LLM stubbing or non-determinism.
  - Ships now: it depends on nothing that doesn't already exist (recipe seam, policy,
    audit). It does **not** block on the router (task 095) or any planning-model work.
  - Smallest attack surface — no second LLM path, no second place untrusted text steers
    control flow (armor already guards the inbound goal text at the channel per ADR 045).
- **Cons**
  - It is an agent *launcher* more than an agent *builder* for genuinely novel goals: a
    free-form goal that isn't already structured collapses to "one worker, default
    recipe." The "intelligent orchestrator that autonomously works toward a goal" vision
    is not realized by A alone.
  - Pushes real intelligence onto the human (who must pre-structure the plan) or onto the
    worker (a single coding worker does all the reasoning). The orchestrator's
    decompose-and-coordinate value is thin in v1.

#### Option B — LLM-assisted decomposition (orchestrator calls a model to plan)

**One-sentence description.** The orchestrator calls a model to turn a free-form goal
into sub-goals and recipe selections, via a **separate narrow "planning" seam** that
decomposes/selects only and **never authors code**, so the no-code-authoring invariant
and REQ-081-05 still hold.

**Implementation sketch.** Same `Planner` interface as A, but with an `LLMPlanner`
concrete that calls a model behind a **new, narrow planning seam** — *not*
`supervisor.Executor` and *not* `internal/executor`. The seam is something like
`Decompose(ctx, goal string, recipes []string) ([]SubGoal, error)`: it takes the goal
plus the catalog of available recipe names (`recipe.ListRecipes()`) and returns an
ordered sub-goal list with a recipe chosen per sub-goal. Crucially this seam's output is
**a plan (data), never a branch (code)** — it cannot author, by type. To satisfy
REQ-081-05, the planning seam must resolve its model **through the ADR 043
registry/router**, not through `internal/executor`: the orchestrator imports the router
(or a planning-specific facade over it), the router resolves a cheap local executor for
the decomposition step (ADR 043 explicitly answered this — OQ-3 — "an orchestrator can
route a sub-goal *or its own decomposition step* to a cheap local executor"). The leaf
purity rule from ADR 043 carries over: the orchestrator references a routing-spec value
type and the router interface, not the Claude CLI concrete.

- **Pros**
  - This is the "intelligent orchestrator" the owner's vision names — free-form goals
    decompose into real multi-step plans without the human pre-structuring them.
  - Reuses ADR 043's registry/router exactly as ADR 043 intended (decomposition is just
    another cheap-local-first dispatch), so it doesn't invent a second model path.
  - The planning seam being decompose-only (returns data, not a branch) keeps the
    no-code-authoring bright line intact at the type level.
- **Cons**
  - **It cannot ship in 081 as scoped today.** The registry/router (ADR 043) is a stub —
    the only working executor path is `internal/executor`, which REQ-081-05 forbids the
    orchestrator from importing. Building B *now* would force either (i) importing the
    forbidden package (invariant violation) or (ii) building the router first (task 095
    pulled ahead, a large dependency 081 doesn't list).
  - A second place untrusted text drives control flow: the plan the LLM emits steers
    which recipes run and with what parameters. That plan must itself be surfaced for
    human approval (it is — §4) and the model output treated as untrusted, but it widens
    the trust surface versus A.
  - Non-deterministic decomposition makes the TCs harder — they must stub the planning
    seam, and the live behavior is only assertable at L5 (which 081 can't reach anyway).

#### Recommendation: **Option A now, with the `Planner` seam shaped so B drops in later.**

The deciding factor is **invariant blast radius under the current dependency state.**
REQ-081-05 forbids `internal/executor`, and the clean alternative path (the ADR 043
router) does not exist yet. Option B today therefore forces a choice between violating the
invariant and pulling task 095 forward — neither acceptable for a task scoped as "L2 unit
tests, additive package." Option A satisfies every 081 requirement *by construction*,
ships against only what exists, and keeps the import-graph assertion (TC-081-05) trivially
green.

The vision is not abandoned — it is **sequenced.** By defining decomposition behind a
`Planner` interface in v1 (A's `StructuredPlanner` as the only concrete), the
LLM-assisted `LLMPlanner` (B) becomes a *drop-in* once the router lands: a new concrete
satisfying the same interface, reaching its model through the registry/router (never
`internal/executor`), surfacing its plan through the same approval gate. This is the
project's "defer premature decisions / no abstraction until the 2nd concrete use case"
rule applied honestly: the seam is justified *now* because we can already name the second
implementation and the exact reason it can't be built yet (the router). The orchestrator's
import invariant is identical under both concretes — it never imports `internal/executor`;
B reaches a model only via the router. **The LLM planner is its own follow-on task, gated
behind task 095 (the router), and should be planned as such (§6).**

### 2. Report format back through the channel — **a typed `Result` aggregate, rendered to a human-readable plain-text summary at the channel boundary**

The orchestrator aggregates worker outcomes into a **typed value** (e.g.
`PlanResult { Goal string; Outcomes []SubGoalOutcome }`, where each `SubGoalOutcome`
carries the sub-goal, the recipe used, a success/failure status, and a short detail —
branch/PR on success, failure reason on failure). The orchestrator works in the typed
shape (composable, testable, assertable per-sub-goal at L2). **Rendering to text happens
at the outbound channel seam**, not in the orchestrator's core logic.

The deciding factor is the consumer: the human reads this over **Telegram**, where the
payload is a chat message — ultimately a string. A typed result internally + a
plain-text render at the edge gives both: the orchestrator stays Unix-composable (typed
contract, "plain text at the boundary" per the design principles), and the human gets a
legible summary ("Goal: …; 2 sub-goals; ✓ docs-fix → PR #12; ✗ coding-agent → gate
failed: go test"). A pure-plain-text-in-the-core design would forfeit per-sub-goal
assertability and bake presentation into logic; a structured-JSON-to-the-human design
would dump machine output into a chat. Typed core + text render at the edge is the
correct factoring.

**This requires an outbound channel seam that does not exist today (fact 3).** Define it
as a small interface the orchestrator depends on — e.g.
`Reporter { Report(ctx, text string) error }` (or a typed-result variant rendered by the
concrete) — implemented by a Telegram *outbound* adapter (bot `sendMessage`, with the
**same envelope encrypt+sign** as inbound per ADR 045, so replies are confidential too).
For 081's L2 tests this seam is a fake that captures the reported text. The concrete
Telegram outbound adapter is channel-side work (§6).

### 3. Plan-state persistence — **in-memory for v1; durable + memory-guarded deferred to task 084**

Confirmed against ADR 042 and the task's own out-of-scope list: **v1 holds plan/fleet
state in memory.** The orchestrator keeps the current plan, the approval state, and the
accumulating worker outcomes in process memory for the duration of one goal's lifecycle.
Durable persistence + the memory-guard write-gate/delete-verify (ADR 042's guard on the
orchestrator's long-lived goal/fleet store) is **task 084's** concern and is explicitly
out of 081's scope.

The deciding factor is reversibility + sequencing: in-memory is the cheapest reversible
default, and ADR 042 already names memory-guard as the *later* guard on this exact state.
Building durability now would (a) pre-empt task 084's design and (b) add persistence
correctness concerns to a task that can't even reach L5. The seam shape should not
*preclude* durability — keep plan state behind a small store interface so task 084 can
swap an in-memory backend for a memory-guarded one — but the v1 backend is in-memory.
(This mirrors ADR 045 §3's treatment of the replay cache: in-memory v1, durable backend a
named follow-on, same seam.)

### 4. Approval-gate wiring — **the orchestrator calls `policy.Decide` for the spawn action; on `require_approval` it pauses, reports the plan over the outbound channel, and resumes dispatch only when an explicit approval message returns over the inbound channel; the approval token is itself an envelope-verified, armor-guarded inbound message**

The mechanism reuses task 073's `policy.Decide` + the `DecisionRequireApproval` enum, but
**not** the terminal-halt control flow in `runtime.decideGate` (fact 2). The sequence:

```
goal arrives (inbound channel → GoalSource)
  → Planner.Plan(goal) → Plan{ ordered sub-goals }
  → policy.Decide(action="spawn-plan", resource=plan summary)   [ADR 038/042 gate]
        ├─ allow            → dispatch (§5)
        ├─ deny             → report "plan denied" + stop (no dispatch)
        └─ require_approval → PAUSE:
              · report the plan over the outbound channel (§2) — "Approve? <plan>"
              · hold plan in memory (§3), dispatch NOTHING
              · wait for an inbound approval message (same channel, envelope-verified +
                armor-guarded — an approval is untrusted external input exactly like a
                goal)
              · on approval → dispatch (§5);  on rejection/timeout → report + drop plan
```

The deciding factor is the bright line ADR 042 makes non-negotiable: **no worker spawns
before human approval, and specifically before any newly-authored agent runs.** Wiring
the gate as a *pause-and-resume* (not a halt) is what lets the orchestrator be the
long-lived interactive front door ADR 042 describes while still enforcing that line. The
approval message returning over the **same envelope-verified + armor-guarded inbound
channel** is essential — the approval is as security-sensitive as the goal (an attacker
who could forge an approval bypasses the human gate entirely), so it gets the same ADR 045
crypto + armor treatment, not a side channel.

For 081's L2 tests this is fully exercisable with a fake policy client and a fake
inbound/outbound channel: `require_approval` → assert no dispatch + assert plan reported
(TC-081-02); a subsequent approval message → assert dispatch proceeds. **Note:** the
spawn-action decide request needs an action name distinct from the worker's `"run-task"`
(e.g. `"spawn-plan"` / `"spawn-worker"`); this is a new policy action the orchestrator
issues, additive to the existing `run-task` gate.

### 5. Dispatch shape — **one worker per sub-goal, sequential, by reusing the existing `runtime` per-worker assembly (task 077); the orchestrator selects the recipe via `recipe.SelectRecipe` and hands the sub-goal's `Task` to `runtime.Run` (or the function 077 exposes), never reimplementing supervisor assembly**

On approval, the orchestrator iterates the plan's sub-goals **sequentially** (concurrency
is task 086) and for each:

1. calls `recipe.SelectRecipe(subGoal.RecipeName)` to confirm the recipe exists (and to
   get the `Recipe` value), and
2. dispatches a worker by invoking the **existing `runtime` assembly** — the same
   recipe → four-IO-seams → containment box → supervisor → gate → publish path that
   `runtime.Run` already performs for one task. The orchestrator supplies the
   sub-goal's `supervisor.Task` and the recipe name (via config); it does **not**
   construct a `supervisor.Supervisor` itself.

The deciding factor is REQ-081-06 + the Unix-composability principle: `runtime.Run` *is*
the worker. Reimplementing supervisor assembly in the orchestrator would duplicate the
seam wiring, the gate-existence assertion, the policy/vault/audit/checkpoint wiring, and
the containment lifecycle — accidental monolithic drift, and a second place those
load-bearing controls could silently diverge. The orchestrator's one job is
decompose → gate → dispatch → aggregate; "dispatch" means "invoke the existing
per-worker assembly," not "build a supervisor."

**Import consequence (REQ-081-05 / TC-081-05):** the orchestrator imports
`internal/recipe` (for `SelectRecipe`) and `internal/runtime` (to invoke the assembly).
`internal/runtime` *does* import `internal/executor` (via `stubResolveExecutor`) — so the
import-graph assertion must be read precisely: **`internal/orchestrator`'s own package
sources must not import `internal/executor`**, which holds under this design (the
orchestrator names `recipe` and `runtime`, never `executor`). TC-081-05 should assert
"`internal/executor` does not appear as a **direct** import of `internal/orchestrator`,"
not "absent from the full transitive graph" — because `runtime` legitimately depends on
`executor` today, and the orchestrator dispatching *through* `runtime` is the intended,
ADR-042-blessed path (the orchestrator authors no code; the *worker* it dispatches runs
the executor inside its box). See §6 for the exact TC shape. *(If a stricter "fully
absent from the transitive graph" reading is wanted, the orchestrator would need to
dispatch through a narrower runtime entrypoint that doesn't pull `executor` into the
orchestrator's graph — but that is over-engineering for v1 and conflicts with reusing the
existing assembly; the direct-import reading is the correct one and matches the invariant's
intent: the orchestrator doesn't author code, it dispatches workers that do.)*

## Why this framing and not the alternatives

- **Why rule-based now instead of the LLM planner the vision wants.** Not because the LLM
  planner is wrong — it is the destination — but because its clean dependency (the ADR 043
  router) does not exist yet, and the only working model path (`internal/executor`) is the
  one REQ-081-05 forbids. Shipping the seam now and the LLM concrete later (behind task
  095) gets the architecture right without forcing an invariant violation or a large
  pulled-forward dependency into an L2 task.
- **Why a typed result rendered to text at the edge, not plain text throughout.** Plain
  text in the core would forfeit per-sub-goal L2 assertability and bake presentation into
  logic; structured JSON to the human would dump machine output into a Telegram chat. The
  split keeps the core composable and the human-facing output legible — exactly the
  "plain text at the boundary" reading of the design principles.
- **Why pause-and-resume approval, not the existing terminal halt.** `runtime.decideGate`
  treats `require_approval` as "write needs-human, end the run" because a single
  `agent-builder run` is short-lived and host-local. The orchestrator is the long-lived
  interactive front door ADR 042 describes; its approval gate must pause and resume over
  the channel, or the human-in-the-loop control line can't coexist with an interactive
  orchestrator. Reusing the *decision primitive* while replacing the *control flow* is the
  right reuse boundary.
- **Why reuse `runtime.Run` instead of building dispatch in the orchestrator.**
  `runtime.Run` already is the gated, contained, audited per-worker assembly. A second
  assembly path in the orchestrator would be the accidental monolith ADR 041/042 warn
  against and a second place the verification gate, containment, and policy wiring could
  drift. Dispatch = invoke the existing worker.

## Consequences

- **Design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or
  `docs/architecture/diagrams.md` lands with this ADR. The orchestrator surface enters the
  spec only when it ships (per ADR 040/041/042 — spec stays present-tense on the coding
  agent).
- **ADR 042 is extended, not contradicted:** its two-tier model, bright lines, and
  invariants all stand. The one narrow commitment this ADR adds is *how* decomposition
  works in v1 (rule-based behind a seam) and that the autonomous-LLM-planner is a
  separate, router-gated follow-on — ADR 042 left that open.
- **A new package `internal/orchestrator` is introduced** (additive; `internal/supervisor`
  unchanged per REQ-081-06). Its direct imports are `internal/recipe`,
  `internal/runtime`, `internal/policy`, `internal/audit`, and the channel's outbound
  seam — **never `internal/executor`** (REQ-081-05).
- **A new outbound channel seam is required and does not exist today.** The Telegram
  adapter is inbound-only (`GoalSource`). REQ-081-02 (solicit approval) and REQ-081-04
  (report summary) both need an outbound `Reporter` seam. The planner must slot the
  outbound seam (interface + Telegram `sendMessage` concrete with ADR 045 envelope) ahead
  of or inside 081 — see §6. 081's L2 tests use a fake outbound seam; the live Telegram
  outbound concrete can be a thin follow-on.
- **The LLM-assisted planner is a named follow-on gated behind task 095 (the router).**
  When the registry/router lands, an `LLMPlanner` implementing the same `Planner`
  interface, reaching its model through the router (never `internal/executor`), drops in
  behind the same approval gate. This should be its own task + test spec.
- **A new policy action name (`spawn-plan` / `spawn-worker`) is issued by the
  orchestrator**, additive to the existing `run-task` gate. The policy-engine config that
  returns `require_approval` for spawns is operator-side (ADR 038).
- **All load-bearing invariants survive:**
  - *Verification gate is the definition of done* — every dispatched worker is gated
    exactly as today (the orchestrator dispatches through `runtime`, which runs the gate);
    the orchestrator's own spawn action is additionally gated by `policy.Decide`.
  - *No unattended self-modification / no agent edits agent-builder's own repo* — the
    orchestrator authors nothing (rule-based v1 imports no model; the LLM follow-on
    decomposes-only, returning a plan not a branch). It dispatches workers that target
    *other* repos.
  - *the internal planning hub is read-mostly / human authors the goal* — the human
    authors the goal and **approves the plan before any worker spawns** (§4); the
    orchestrator decomposes and proposes, it does not set its own objectives.
  - *One task = one repo = one branch* — preserved per worker; each sub-goal is one
    `runtime` assembly. The orchestrator coordinates N units sequentially (v1); it does
    not sprawl one unit across repos.
  - *Containment + executor seam + secrets brokering* — untouched; workers run in
    exec-sandbox via `runtime`, route the executor seam as today (stub now, router later),
    and broker secrets via vault.
  - *Supervisor isolation (F-003) + the leaf isolations (F-005/F-006/F-007)* — preserved;
    the orchestrator sits *above* the supervisor and imports none of the crypto/LLM leaves
    into the supervisor's graph. A new `make fitness-orchestrator-no-executor` check
    (proposed for the planner, not built here) should assert `internal/executor` is not a
    **direct** import of `internal/orchestrator`.
- **What becomes harder.** The system gains a long-lived, stateful coordination layer with
  pause/resume control flow and an approval round-trip over the channel — more moving parts
  than a single short-lived `run`. The approval message is a new untrusted-input path that
  must get the full ADR 045 crypto + armor treatment. And the import-graph invariant now
  needs the precise "direct import" reading (§5) rather than a blunt transitive check,
  because dispatching through `runtime` legitimately reaches `executor` for the *worker*.
  These costs are accepted as the price of the interactive, gated, multi-worker
  orchestrator ADR 042 chose.
- **`autonomous-builder.md` (internal planning hub)** still frames agent-builder around the
  single coding agent; the orchestrator core should be reconciled there separately, as ADR
  040/041/042 already noted for the broader repositioning.
