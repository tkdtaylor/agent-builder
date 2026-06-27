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
of the recipe seam**, standing *on* it to select, parameterize, and (now) generate
recipes; it is not assembled *by* it. (2) ADR 041 enforced gate-existence at *compile
time* because recipes were human-authored Go. This ADR lets the orchestrator **author
recipe code**, which forfeits that compile-time guarantee, so gate-existence moves to a
**runtime assembly-time assertion** (detailed under the safety model below). Recipes
are no longer exclusively human-authored.

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
3. **The orchestrator MAY author new agent/recipe code** — not merely select from a
   fixed first-party menu — with automated scanning as part of a layered release gate.
   This is the defining capability of a *builder*; the safety model for it is set out
   below.

## Decision

Adopt a **two-tier architecture** for the purpose-built-agent surface.

**Tier 1 — the orchestrator.** A generic, long-lived, *interactive* agent the human
communicates with over a secure messenger. The human is the **author of the goal**;
the orchestrator decomposes it, proposes a plan, and — **only on human approval** —
assembles and dispatches purpose-built worker agents, monitors them, aggregates their
results, and reports back through the messenger. Its work product is *other agents and
their aggregated results*, never a branch of its own. It is a **consumer of the
recipe seam** (ADR 041): it selects and parameterizes existing first-party recipes and
**may author new ones** (gated by the safety stack below) per sub-goal. It is **not
itself a recipe**.

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

### Safety model for code-authoring, and the bright line that stays

The orchestrator **may author new agent/recipe code** — the defining power of a
builder, chosen deliberately by the owner. Authoring is made safe by a **layered gate
on every generated artifact, not by any single check**:

1. **code-scanner** on all generated code (malware, backdoors, credential-harvesting,
   obfuscation) — a blocking release gate.
2. **dep-scan** on anything the generated code pulls in (supply-chain CVEs / malicious
   packages).
3. **A mandatory verification gate on the generated agent.** ADR 041 enforced
   gate-existence at *compile time* because recipes were human-authored Go. Generated
   code forfeits that guarantee, so the check moves to a **runtime assembly-time
   assertion: the assembler REJECTS any generated agent that does not bind a real,
   non-empty, blocking gate.** "A generated agent with no gate" stays unrepresentable —
   the guarantee is preserved, enforced one layer down.
4. **Containment.** Generated code only ever runs *inside exec-sandbox* under the
   default-deny egress allowlist — so even code that slips past the scanners is boxed,
   with no ambient network or host access.
5. **policy-engine** gates what a generated agent may do and what egress it gets.
6. **Human approval before first run** (the line below) + **audit-trail** records the
   generated source and its provenance for forensics.

The owner's instruction must not be read to mean code-scanner is the whole control:
**a scanner is necessary but not sufficient** — it catches *known-bad*, not
novel-malicious or merely-wrong. The safety is the *stack*: scan + dep-scan +
verify-gate + containment + policy + human approval + audit. Code-authoring rides on
all of it.

**The one bright line that stays non-negotiable: the orchestrator authors *worker /
recipe* code — it NEVER edits agent-builder's own repo** (its orchestrator core, its
verification gate, its escalation, or this safety stack). Authoring a worker is the
job; editing the thing that judges and contains the workers is self-modification and
remains forbidden. The earlier "no autonomous code authoring at all" line conflated
the two; only the self-modification half was ever load-bearing. Relaxing even this
would require its own ADR.

**The human remains the AUTHOR of the goal, and approves the plan before any worker
spawns.** The orchestrator surfaces its decomposition — and any agent code it generated
— and obtains explicit human approval; the mechanism is policy-engine's
`require_approval` obligation gating the spawn action.

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
- **Why allow code-authoring, gated, rather than selection-only.** A builder that can
  only pick from a fixed human-written menu is an agent *launcher*, not an agent
  *builder* — it cannot produce a genuinely novel purpose-built agent. The owner chose
  the builder. The risk an authoring agent introduces is contained by the layered stack
  above (scan + dep-scan + mandatory runtime gate + containment + policy + human
  approval + audit) and bounded by the one line it can never cross (its own repo).
  Selection-only is the safer-but-weaker design; gated authoring is the chosen trade.

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
- **ADR 041 is amended (two points):** the orchestrator is a recipe-seam *consumer*,
  not a recipe; and recipes may now be **orchestrator-authored**, not exclusively
  human-authored Go — so ADR 041's compile-time gate-existence guarantee becomes a
  **runtime assembly-time assertion** that rejects any gate-less generated agent.
- **All seven load-bearing invariants survive:**
  - *Verification gate is the definition of done* — every worker is gated exactly as
    today; the orchestrator's own actions are additionally gated by policy-engine
    before any spawn or egress.
  - *No unattended self-modification* — the orchestrator may author *worker* code but
    **never edits agent-builder's own repo** (orchestrator core, gate, escalation, or
    the safety stack); generated worker code is scanned, gated, contained, and
    human-approved before it runs.
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
  - code-authoring pipeline: generate recipe/agent code → code-scanner + dep-scan →
    runtime gate-existence assertion → human approval, before any generated agent runs;
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
