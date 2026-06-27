# ADR 042 — The secure orchestrator (two-tier purpose-built-agent model)

**Status:** Proposed — design-only. Defines the two-tier architecture that turns the
ADR 040/041 "builder of purpose-built agents" arc into a concrete shape; no code,
spec, or diagram changes land with this ADR.
**Date:** 2026-06-27
**Motivated by:** ADR 040 (reposition agent-builder as the ecosystem's assembly layer)
and ADR 041 (the agent-recipe seam). ADR 040 made "builder of purpose-built agents"
the primary forward arc; ADR 041 defined how *one* worker agent gets assembled (the
recipe seam). This ADR defines the layer that *drives* recipes: a long-lived
interactive orchestrator that decomposes a human goal into many contained workers.
**Amends:** ADR 041 on two points. (1) ADR 041 left open whether the generalized
surface is itself "just another recipe." It is not — the orchestrator is a **consumer
of the recipe seam**, standing *on* it to select, parameterize, and dispatch recipes;
it does not author them. (2) ADR 041 enforced gate-existence at *compile time* because
recipes were human-authored Go. This ADR allows recipes to be **authored by a
code-authoring worker**, not only by humans — which forfeits the compile-time guarantee
for generated recipes, so gate-existence moves to a **runtime assembly-time assertion**
(detailed under the safety model below).

## Context

ADR 041 settled how a single purpose-built secure agent is assembled: a Go-typed
recipe binds the four IO seams (goal source, executor+prompt, gate, result sink) plus
the shared block wiring, and `runtime` assembles a `supervisor` from it. The current
run pipeline (`internal/runtime/run.go` → `internal/supervisor`) dispatches **exactly
one task, one box, one branch** and exits. That is the right shape for a worker.

What is missing is the layer above it. A human today drives agent-builder by setting
env vars and invoking `agent-builder run` once per task, on a host they control. There
is no interactive front door, no goal decomposition, no fleet, and no remote channel.
The roadmap names two blocks — **agent-mesh** (Ed25519-signed envelopes + replay
prevention) and **memory-guard** (write-gate + delete-verify) — as **Deferred**,
precisely because "a multi-agent substrate does not exist yet" and there is no
"long-lived memory store worth guarding." Building the orchestrator is what creates
that substrate and that store, which is why those two blocks move onto the critical
path here.

The project owner has fixed three decisions for this layer (locked; this ADR records
rather than re-litigates them):

1. **The human↔orchestrator channel is an existing secure messenger** that is **free
   to set up**, not a local CLI or a bespoke endpoint.
2. **Multiple concurrent workers from the start**, not a single-worker proof of
   concept.
3. **Code-authoring is a worker-tier task, not an orchestrator capability.** When a
   goal needs new code — including the code/definition of a *new* purpose-built agent —
   the orchestrator spawns a dedicated code-authoring worker to produce it; the
   orchestrator itself authors nothing. Code-authoring thereby inherits the full worker
   safety model (set out below), and "build an agent to do X" decomposes into an
   ordinary, gated sub-task.

## Decision

Adopt a **two-tier architecture** for the purpose-built-agent surface.

**Tier 1 — the orchestrator.** A generic, long-lived, *interactive* agent the human
communicates with over a secure messenger. The human is the **author of the goal**;
the orchestrator decomposes it, proposes a plan, and — **only on human approval** —
assembles and dispatches purpose-built worker agents, monitors them, aggregates their
results, and reports back through the messenger. Its work product is *other agents and
their aggregated results*, never a branch of its own. It is a **consumer of the
recipe seam** (ADR 041): it selects and parameterizes existing recipes and dispatches
them; when a goal needs new code or a new agent, it spawns a **dedicated code-authoring
worker** to produce it (a worker task, gated like any other — see below). **The
orchestrator itself authors no code.** It is **not itself a recipe**.

**Tier 2 — workers.** Each sub-goal is handed to a purpose-built secure worker
assembled from a recipe (ADR 041), each contained, each behind its own verification
gate, each autonomously working its sub-goal. A worker is still exactly today's
`recipe → runtime → supervisor` run (one task, one repo, one branch). The
orchestrator coordinates **N** of them concurrently; `runtime`/`supervisor` are not
replaced — the orchestrator is a **new layer above** them.

### Security applies to both tiers

The orchestrator is itself privileged, network-connected, and long-lived, so it must
itself be contained, gated, and audited — not merely the workers:

- It runs **inside exec-sandbox** (containment is not a worker-only control).
- **policy-engine gates the orchestrator's own actions** — what recipes it may spawn,
  with what parameters, and what egress it has — in addition to gating each worker.
- **audit-trail records a tamper-evident, fleet-wide log** of every agent spawned and
  every action taken across both tiers.
- Secure-messenger input is **untrusted external input**: **armor** moves from
  optional to load-bearing on the human↔orchestrator channel (prompt-injection /
  exfil / tool-call guard), in addition to guarding each worker's web ingestion.

### The channel: Telegram bot + our own end-to-end envelope

The constraint is **free to set up and securable**. The recommendation is a **Telegram
bot as the transport, with the ecosystem's own end-to-end encryption layered on the
message payloads** — not Telegram's native crypto.

Telegram alone is the easiest, free-est, most reachable option (a bot is minutes of
BotFather setup, drivable from any phone), but its bot chats are *client–server*
encrypted, **not end-to-end** — Telegram's servers can read them, and the E2E "Secret
Chat" mode is unavailable to bots. That is a poor trust model for a control channel
*by itself*. The resolution is architectural: **the ecosystem already owns
authenticated-envelope crypto** (agent-mesh's Ed25519 signed envelopes + replay
prevention) and **armor already treats all ingested content as untrusted**. We encrypt
and sign the control-channel payloads ourselves with a key shared only between the
human's client and the orchestrator; Telegram then carries **ciphertext it cannot
read**. This reduces the messenger to a dumb, untrusted transport — exactly what armor
assumes — so the transport's own weak crypto stops being load-bearing.

Considered alternatives:

- **Signal (signal-cli)** — best-in-class *native* E2E (Signal Protocol), free. But no
  official bot API, a community CLI bridge, a dedicated phone number required at
  registration (VoIP numbers are often rejected), and ToS friction around automation.
  The pick if native E2E with no app-layer crypto is preferred over setup ease.
- **Matrix** — open, self-hostable, native E2E (Megolm), free on a public homeserver.
  The most principled "secure + open" option, but the heaviest setup (homeserver
  choice + bot E2E key management).
- **Simplex / XMPP+OMEMO** — strong free, private, E2E options with bot support, but
  niche ecosystems and a steeper learning curve.
- **Discord / Slack** — trivial bots but no E2E and corporate-owned servers; rejected
  for a security-first control channel.

**Telegram + app-layer envelope** wins the owner's free-and-securable constraint: zero
hosting cost, minimal setup, universal reach, and a trust model we *own* rather than
rent. Because the security lives in our envelope layer, the concrete messenger is a
**swappable transport** — Signal or Matrix can replace Telegram later without changing
the security model. A follow-on may finalize the transport; the position is Telegram.

### All seven blocks are now in play

| Block | Role in this build |
|---|---|
| **exec-sandbox** | Contain **each worker AND the orchestrator** — both tiers run in a box. |
| **policy-engine** | Gate orchestrator spawning + each worker's actions + orchestrator egress; carries the `require_approval` obligation that enforces the human-approval line. |
| **vault** | Broker each worker's secrets; never present in-box. |
| **audit-trail** | Tamper-evident, fleet-wide log of every agent spawned and every action across both tiers. |
| **armor** | Guard the messenger channel (untrusted human-side input) **and** each worker's web ingestion. |
| **memory-guard** | Guard the orchestrator's long-lived goal/fleet state (write-gate + delete-verify). |
| **agent-mesh** | Transport for orchestrator↔worker messaging (Ed25519-signed envelopes + replay prevention). |

### Code-authoring is a worker task; the safety model is the worker safety model

Code-authoring — including authoring the code/definition of a *new* purpose-built
agent — is **not a special orchestrator power**. It is a **task performed at the worker
tier**: either as a normal coding worker's output (the existing coding recipe already
writes code), or by a **dedicated "agent-builder" worker** — a first-party recipe whose
job is to author a new agent and hand it back. "Build an agent to do X" therefore
decomposes the same way any goal does: a code-authoring sub-task → then dispatch the
result. This is the Unix-philosophy factoring — the orchestrator keeps one
responsibility (decompose + dispatch + aggregate), and code generation lives in a
contained, single-purpose worker. (The base case is human-authored: the *first*
agent-builder worker is a first-party Go recipe per ADR 041; it can then author
further agents.)

The payoff is that authoring inherits the **standard worker safety model** rather than
needing a bespoke privileged path:

1. **code-scanner** on the generated code (malware, backdoors, credential-harvesting,
   obfuscation) — a blocking release gate.
2. **dep-scan** on anything the generated code pulls in (supply-chain CVEs / malicious
   packages).
3. **A mandatory verification gate.** Because a generated agent's recipe is no longer
   human-authored Go, ADR 041's compile-time "no gate won't compile" guarantee no
   longer covers it, so the assembler applies a **runtime assembly-time assertion:
   reject any generated agent that does not bind a real, non-empty, blocking gate.**
   "A generated agent with no gate" stays unrepresentable.
4. **Containment.** The code-authoring worker, and anything it produces, only ever runs
   *inside exec-sandbox* under the default-deny egress allowlist — so even code that
   slips past the scanners is boxed, with no ambient network or host access.
5. **policy-engine** gates what the code-authoring worker and any generated agent may
   do and what egress they get.
6. **Human approval before a newly-authored agent is dispatched** (the line below) +
   **audit-trail** records the generated source and its provenance for forensics.

A scanner alone is **necessary but not sufficient** — it catches *known-bad*, not
novel-malicious or merely-wrong. Safety is the *stack*, and it applies automatically
because code-authoring is just a (gated, contained) worker.

**The bright line that stays non-negotiable: no agent at any tier edits
agent-builder's own repo** — not the orchestrator, not a coding worker, not the
agent-builder worker. They author *other* agents and *target* repos; the orchestrator
core, the verification gate, the escalation path, and this safety stack are never
self-modified. Relaxing this would require its own ADR.

**The human remains the AUTHOR of the goal, and approves the plan before any worker
spawns — and specifically before any newly-authored agent is run.** The orchestrator
surfaces its decomposition and any generated agent code and obtains explicit human
approval; the mechanism is policy-engine's `require_approval` obligation gating the
spawn action.

## Why this framing and not the alternatives

- **Why multi-worker, not a single-worker POC.** The owner chose concurrent workers
  from the start. State the cost honestly: this makes **agent-mesh and memory-guard
  prerequisites**, not later niceties — a single-worker POC could have shipped with
  neither (one box, no inter-agent transport, no persistent fleet state). Multi-worker
  is the decision; the consequence is that two Deferred blocks become adoption targets
  now. The upside is that the substrate is real from day one rather than retrofitted
  onto a single-worker design that assumed neither.
- **Why an existing messenger + our own crypto, not native E2E or a bespoke API.** A
  messenger gives authenticated identity, presence, and phone-reachability for free
  from widely-reviewed software; layering our own Ed25519 envelope on top gives E2E we
  *own*, so we get Telegram's zero-cost ubiquity without trusting its servers. A bespoke
  secure endpoint would reinvent secure transport — the cross-cutting primitive this
  ecosystem composes rather than rebuilds. Relying on a messenger's *native* E2E
  (Signal/Matrix) would couple the security model to one vendor's crypto and setup
  friction; the envelope layer keeps the transport swappable.
- **Why code-authoring is a worker task, not an orchestrator capability.** A builder
  that can only pick from a fixed human-written menu is an agent *launcher*, not an
  agent *builder*. But putting code-generation *in the orchestrator* would bloat its
  single responsibility and create a privileged authoring path needing its own bespoke
  safeguards. Pushing authoring down to a dedicated worker keeps the orchestrator thin
  and makes authoring inherit the existing worker safety model (scan + dep-scan +
  runtime gate + containment + policy + approval + audit) for free. The alternative —
  an orchestrator that writes code directly — is both a wider responsibility and a
  wider attack surface for no gain.

## Consequences

- **Design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or
  `docs/architecture/diagrams.md` lands with this ADR.
- **The spec stays present-tense.** Per ADR 040/041, `docs/spec/` continues to describe
  the single autonomous *coding* agent. The orchestrator surface enters the spec only
  when it actually ships; it must not be written into the spec in the present tense
  before then.
- **agent-mesh and memory-guard are promoted from Deferred to active adoption
  targets.** This is the direct consequence of the multi-worker decision: agent-mesh
  becomes the orchestrator↔worker transport and memory-guard becomes the guard on the
  orchestrator's long-lived goal/fleet state. The roadmap edit (moving both rows off
  Deferred in the block-adoption table) is a **separate change**, not part of this ADR.
- **ADR 041 is amended (two points):** the orchestrator is a recipe-seam *consumer*
  (it selects, parameterizes, and dispatches; it does not author); and recipes may now
  be **authored by a code-authoring worker**, not exclusively human-authored Go — so
  ADR 041's compile-time gate-existence guarantee becomes a **runtime assembly-time
  assertion** that rejects any gate-less generated agent.
- **All seven load-bearing invariants survive:**
  - *Verification gate is the definition of done* — every worker is gated exactly as
    today; the orchestrator's own actions are additionally gated by policy-engine
    before any spawn or egress.
  - *No unattended self-modification* — **no agent at any tier edits agent-builder's
    own repo** (orchestrator core, gate, escalation, or the safety stack). Code-
    authoring workers author *other* agents and *target* repos; generated code is
    scanned, gated, contained, and human-approved before it runs.
  - *the internal planning hub is read-mostly / human-authored goal* — the human authors and
    approves the goal; the orchestrator decomposes and proposes, it does not set its
    own objectives.
  - *One task = one repo = one branch* — preserved per worker; each worker is one
    recipe→supervisor run. The orchestrator coordinates many such units; it does not
    sprawl one unit across repos.
  - *Containment* — both tiers run in exec-sandbox; the default-deny egress allowlist
    stays the load-bearing token-in-box control on each box.
  - *Executor seam `(harness, model) → branch`* — unchanged; workers use it via their
    recipes.
  - *Secrets brokering* — vault brokers each worker's secrets exactly as today.
- **Decomposition into tasks is the immediate follow-on** (task-planner's job; not
  enumerated here). The major clusters, at a high level:
  - messenger channel adapter (Telegram bot) + app-layer Ed25519 envelope + armor guard
    on that channel;
  - orchestrator core: goal intake → plan → human-approval gate → dispatch → aggregate
    → report;
  - the **agent-builder worker recipe**: a first-party code-authoring worker (generate
    recipe/agent code → code-scanner + dep-scan → runtime gate-existence assertion →
    human approval) that runs contained and gated like any worker, before any generated
    agent is dispatched;
  - agent-mesh adoption for orchestrator↔worker transport;
  - memory-guard adoption for the orchestrator's goal/fleet state;
  - orchestrator self-containment + policy gating + fleet-wide audit;
  - multi-worker concurrent dispatch over the recipe seam.
- **What becomes harder.** The system gains a long-lived, network-facing, stateful
  component — a larger attack surface and an operational process to keep alive,
  whereas today every run is short-lived and host-local. Concurrency introduces fleet
  coordination, partial-failure handling, and aggregation logic that a single run
  never needed. These costs are accepted as the price of the interactive,
  multi-worker front door the owner chose.
- **`autonomous-builder.md` (internal planning hub)** still frames agent-builder around
  the single coding agent; the two-tier orchestrator model should be reconciled there
  separately, as ADR 040 and ADR 041 already noted for the broader repositioning.
