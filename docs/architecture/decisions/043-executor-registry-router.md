# ADR 043 — The executor registry + model router

**Status:** Accepted — design-only. Defines the registry + router seam that promotes
the deferred multi-provider routing off the roadmap's Deferred list; no code, spec, or
diagram changes land with this ADR.
**Date:** 2026-06-27
**Motivated by:** the roadmap's Deferred bullet "Multi-provider router (Claude + Gemini
+ local LLMs, quota/sensitivity/cost routing) — design the seam now, build as v1." This
ADR is the "design the seam now" step. It also answers ADR 042's open question on how a
worker's executor is selected (OQ-3 — orchestrator decomposition executor selection):
the orchestrator's decomposition worker and its dispatched workers both route through
the registry, so an orchestrator can route a sub-goal to a cheap local executor.
**Amends:** ADR 041's executor IO seam. ADR 041 has a recipe bind **one**
`ExecutorFactory` returning a `supervisor.Executor`. This ADR replaces that single
binding with a **routing spec** the recipe declares and the router resolves to a
concrete executor at dispatch time. ADR 041's leaf-purity rule is preserved (the recipe
references a routing-spec value type only; it imports no registry/router/executor
concretes).

## Context

Today there is exactly one executor: `internal/executor/claude_cli.go`, a single
concrete `supervisor.Executor` constructed inline and injected into the supervisor. The
`(harness, model) → branch` seam (SPEC invariant 6) is already abstract, but nothing
selects *between* executors — the one Claude CLI is hardwired, and ADR 041 made a recipe
bind exactly one `ExecutorFactory`.

Three forces now make "pick an executor per dispatch" real:

1. **The deferred multi-provider need.** The roadmap has always intended Claude +
   Gemini + Codex + local LLMs behind one seam. The blocks have shipped; the deferral
   was about sequencing, not feasibility.
2. **Invariant 6 already promises uneven-quality mixing made safe by the gate** — "fail
   → escalate to a stronger executor." There is no component today that *holds* a set of
   executors to escalate across, nor one that picks the first (cheapest) one. The
   invariant describes a capability the code does not yet have.
3. **Cost.** A weak local LLM costs near-zero per dispatch; a frontier cloud model does
   not. If the gate makes weak-first safe, then trying the cheapest plausible executor
   first and only escalating on gate failure is a direct, large cost saving with no
   correctness loss.

The project owner has fixed several decisions for this seam (locked; this ADR records
rather than re-litigates them):

1. **Routing policy is capability/cost-first**, not sensitivity-first: pick the
   weakest/cheapest executor that can plausibly do the task; escalate to a stronger one
   on gate failure.
2. **Sensitivity is a soft preference**, never a hard gate — modelled as a router weight,
   not a constraint. A clean policy-engine hook is left so a future ADR could *harden*
   it (e.g. pin a sensitive task to a local-only executor) without redesign. Hardening
   is explicitly not built here.
3. **The concrete executors to support as registrable entries** are: Claude
   (subscription / OAuth token), a local LLM, Codex, and Google Gemini.

## Decision

Introduce two new components on the **executor side** of the seam:

### 1. The executor registry

A catalog of available LLM executors. The key factoring is that an entry separates the
**harness driver** (the thing that runs the agentic loop and emits a branch) from the
**(model, endpoint, auth) config** it points at — so **one harness backs many entries**:

- **A harness is decoupled from its model.** The Claude Code CLI is a harness, not a
  model binding: it honors `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`, so pointing it
  at a different endpoint makes the **existing `claude_cli.go` harness drive a different
  model**. A local LLM is therefore **not a new harness** — it is the *same* Claude CLI
  harness configured with a local endpoint and no cloud auth.
- **Translation-proxy seam for local entries.** Claude Code speaks the Anthropic Messages
  API; most local servers (llama.cpp, vLLM, Ollama, LM Studio) speak the OpenAI API. So a
  local entry typically fronts the model with a small **translation proxy** (the
  LiteLLM / claude-code-router pattern: Anthropic-shape in → local model → Anthropic-shape
  out) that presents an Anthropic-compatible endpoint. The entry's `Endpoint` points at
  that proxy. This is a named seam, not bespoke per-model code.
- **Some CLIs are their own harness.** Codex and Gemini ship their own CLIs that are their
  own harnesses (own wire format, own auth), so each is its own harness driver — they do
  not reuse the Claude CLI harness.

So the old "cloud-cli bundles harness+model vs. local-model needs a harness" split
collapses into a single axis: **which harness driver + which endpoint/auth config.** The
registry stores provider **config** (never secrets) plus per-entry **quota/usage state**:

```
RegistryEntry {
  ID             string          // stable handle, e.g. "claude-oauth", "local-qwen", "codex", "gemini"
  Harness        HarnessDriver   // which harness runs the loop: claude-cli | codex-cli | gemini-cli
  CapabilityTier int             // ordered: higher = stronger
  CostWeight     int             // relative cost per dispatch; lower = cheaper
  // model/endpoint/auth config (one harness, many entries):
  ModelID        string          // model identifier
  Endpoint       string          // base URL the harness points at (cloud API, or a local translation proxy)
  SecretRef      string          // which vault secret to resolve (NOT the secret); empty for a no-cloud-auth local entry
  // quota config (optional; a local entry leaves Budget zero = unlimited):
  Budget         QuotaBudget     // configured cap over a rolling window (e.g. a subscription limit), or zero for none
  // quota/usage state (router-owned, persisted across dispatches — see below):
  Usage          int             // running tally against Budget over the current window
  Availability   Availability    // available | exhausted-until <ResetAt>
}

QuotaBudget   { Limit int; Window Duration }       // e.g. {Limit: N, Window: 5h}
Availability  { Status AvailStatus; ResetAt Time } // Status: available | exhausted
```

Worked examples of the one-harness-many-entries factoring:

| Entry | Harness | Endpoint | Auth (`SecretRef`) |
|---|---|---|---|
| `claude-oauth` | claude-cli | Anthropic API | Claude OAuth/subscription token |
| `local-qwen` | **same claude-cli** | local model via translation proxy | none (no cloud auth) |
| `codex` | codex-cli | OpenAI API | Codex/OpenAI key |
| `gemini` | gemini-cli | Google Gemini API | Gemini API key |

A **local entry** leaves `Budget` zero — it has no subscription cap, so it is never marked
exhausted (the quota-free backstop, below). A local entry also means **local-only egress**
(no cloud traffic leaves the box), which reinforces "local = the privacy/quota backstop"
and is exactly what the soft sensitivity hint should bias toward. The trade-off: a weak
local model may drive Claude's tool-calling format imperfectly — that **agentic-fidelity**
risk is absorbed by the capability tier (a local entry sits at a low tier), the verify
gate, and escalation; it is not a blocker. `Usage` and `Availability` are **not static
config**: they are mutable state the router owns and updates as dispatches land and quota
signals arrive.

The registry is a Go-typed, in-process catalog (consistent with ADR 041's Go-typed
recipe form): entries are first-party Go values, not a runtime-parsed dispatch table.
The per-deployment tuning that *is* data (which entries are enabled, their endpoints,
their secret refs) stays plain-text env, matching the block-wiring convention.

### 2. The model router

Selects a registry entry per dispatch and drives escalation. The capability/cost model
is concrete:

- Every **registry entry** declares a **capability tier** (ordered) and a **cost
  weight** (relative).
- Every **dispatch** (carried from the recipe's routing spec, see below) declares a
  **minimum capability requirement** and an optional **soft sensitivity hint**.
- An entry is **eligible** for a dispatch when it (a) meets the minimum capability
  requirement (`CapabilityTier ≥ minimum`) **and** (b) is **currently available** (not
  exhausted, or past its reset time). Availability is a **hard filter**, exactly like
  capability — an exhausted entry is skipped until its reset, then re-enters the eligible
  set automatically. Sensitivity remains a **soft weight** and never filters.
- The router **picks the cheapest eligible entry** (lowest cost weight among eligible
  entries), breaking ties and nudging the choice with the soft sensitivity hint (e.g. a
  sensitive hint biases toward a local entry when one qualifies) — but the hint **never
  excludes** an otherwise-eligible entry.

#### Quota / usage awareness — exhaustion detection

The router must know when a provider's subscription/quota is spent and when it becomes
usable again, and route around it meanwhile. It tracks this two ways:

- **Reactive.** When a provider returns a rate-limit / quota-exceeded signal (HTTP 429 /
  quota-exceeded from the CLI or API), the router marks the entry `exhausted` and derives
  `ResetAt` from a `Retry-After` / reset hint when the provider supplies one, else from a
  configured cooldown. The entry is filtered out of selection until `ResetAt`.
- **Proactive.** The router maintains a local `Usage` tally against the entry's `Budget`
  over the configured `Window`, and **pre-emptively skips** an entry that is over budget
  *before* sending — avoiding a wasted dispatch that would only earn a 429.

Once the clock passes `ResetAt` (reactive) or the rolling window rolls over (proactive),
the entry's availability flips back to `available` and it re-enters the eligible set with
no manual intervention.

#### Two distinct fallback axes

The router handles two **different** kinds of fallback, on two axes — keeping them
distinct is the point:

- **Gate-failure escalation → walk UP the capability ladder (quality axis).** On gate
  failure, the router escalates: it walks eligible entries in **ascending
  capability-tier** order and hands the loop the **next-stronger** entry. This is what
  invariant 6 has always promised; the router is the component that realizes it.
- **Quota exhaustion → fall to the next currently-AVAILABLE eligible entry (availability
  axis).** When the chosen entry is (or becomes) exhausted, the router does **not** climb
  the quality ladder — it picks the next cheapest entry that is still available at the
  same-or-sufficient capability. Exhaustion is an availability problem, not a quality
  problem, so it is solved on the availability axis.

The two compose: a dispatch may fall sideways for quota *and* climb for quality across
its attempts, and the eligible set (capability ∧ availability) is recomputed each time.

#### Local LLM is the quota-free backstop

Because a local model leaves `Budget` zero, it is **never** marked exhausted — so under
capability/cost-first + quota-aware routing it emerges as the **always-available
fallback** when every cloud provider is exhausted. This is a desirable emergent property,
not a special case: the same "cheapest eligible entry" rule that prefers a cheap cloud
provider when quota remains naturally falls through to the local model when the cloud
entries are filtered out, so work keeps flowing (at the local model's capability) instead
of stalling on a spent subscription.

#### Usage/quota state is router-owned, persisted, and clock-driven

A single short run never observes exhaustion meaningfully — quota is spent over many
dispatches. So `Usage` and `Availability` are **router-owned state that must persist
across dispatches** (and, for the orchestrator, across the whole fleet). The natural
guard for this state when it lives in the orchestrator's long-lived store is
**memory-guard** (ADR 042's write-gate + delete-verify), so a corrupted or rolled-back
quota tally cannot silently let the router over-spend a provider. For a single host-local
run the state can persist to a plain-text file; the seam is the same.

The router takes an **injected clock seam** (a `Clock`/`now()` time source) rather than
calling the wall clock directly, so reset-window and cooldown logic is deterministically
testable — a test can advance the clock past `ResetAt` and assert the entry re-enters the
eligible set, without sleeping.

### The routing spec replaces the hardwired ExecutorFactory (amends ADR 041)

A recipe stops binding one `ExecutorFactory`. Instead it declares a **routing spec** —
a small value type:

```
RoutingSpec {
  MinCapability   int             // minimum capability tier this purpose needs
  SensitivityHint Sensitivity     // soft: none | sensitive (a weight, not a gate)
}
```

`runtime` (the assembler) wires the registry and router and resolves the recipe's
routing spec to a concrete executor at dispatch. **Leaf-purity is preserved exactly as
ADR 041 requires:** the leaf `internal/recipe` imports only the `RoutingSpec` value type
— it does not import the registry, the router, or any executor concrete. The registry
and router are separate components the assembler owns; the recipe describes *what
capability it needs*, not *which executor runs*.

### Where the router lives, and supervisor isolation (F-003)

The router lives on the **executor side** — `internal/router` (a sibling of
`internal/executor`), or equivalently inside `internal/executor` — and is reached the
**same way the single executor is injected today**: `runtime` constructs it and hands
the supervisor a `supervisor.Executor`. The router *is* a `supervisor.Executor` from the
supervisor's point of view (or hands one back per dispatch); the supervisor sees a
seam, not a router.

This keeps **F-003 intact**: `internal/supervisor` gains no import of the router, the
registry, or any LLM/web-fetch package. The router introduces no LLM/untrusted-content
dependency into the supervisor's import graph — it sits entirely on the executor side of
the existing injection boundary, exactly where `claude_cli.go` sits today. The
`make fitness-supervisor-isolation` check (F-003) continues to pass unchanged, and
should be re-run when the router lands to prove it.

### Per-provider auth is a vault concern

The registry holds provider **config** (binary path, model id, capability tier, cost
weight, endpoint) and a **`SecretRef`** naming which secret to resolve — never the
secret itself. **vault brokers the per-provider secret** at dispatch time: Claude
OAuth/subscription token, Gemini API key, Codex/OpenAI key, and (where applicable) a
local-model endpoint credential. This extends the existing `secrets.SecretSource` seam
(`ProviderToken()` today returns one provider's pair) to resolve a *named* provider
secret per registry entry.

Each provider's token is **independently revocable** (SPEC invariant 5) because each is
a distinct vault-brokered credential keyed by `SecretRef` — revoking the Gemini key does
not touch the Claude token. The accepted "token-in-box" risk and the egress allowlist as
its load-bearing control are unchanged; the registry just makes the set of in-box tokens
a per-entry, per-dispatch set rather than a single hardwired one, each independently
rotatable.

## Why this framing and not the alternatives

- **Why capability/cost-first, not sensitivity-first.** The owner's optimization target
  is *getting the job done cheaply*, and the verification gate is precisely what makes
  weak-first safe — a cheap executor that produces a wrong branch fails the gate and
  costs only a wasted (cheap) attempt before escalation. Sensitivity-first would route
  the *majority* of non-sensitive work through stronger/costlier executors by default,
  forfeiting the cost win for a constraint most dispatches do not need. Sensitivity
  still matters, so it is kept as a **soft weight** and a **policy-engine hook** is left
  so a future ADR can harden it (pin sensitive tasks to local-only) *without* redesign —
  the routing spec already carries the hint, and policy-engine already gates dispatch
  (ADR 038/042), so hardening is a new obligation on an existing seam, not new plumbing.
- **Why a registry + router, not one hardwired executor per recipe.** Hardwiring is what
  exists today and what ADR 041 codified — fine for one executor, but it cannot express
  the multi-provider need, cannot mix uneven-quality executors, and cannot escalate
  (invariant 6 has no component to realize it against). A registry makes the *set* of
  executors a first-class catalog; a router makes *selection + escalation* a single
  responsibility instead of a conditional smeared across recipes. It is also where the
  local-first cost saving lives: without a router, "try cheap, escalate on failure"
  has nowhere to live.
- **Why availability is a hard filter but sensitivity stays soft.** They answer
  different questions. An exhausted provider *cannot run the dispatch at all* — sending to
  it earns a 429 and wastes an attempt — so it must be removed from the eligible set, a
  hard filter exactly like capability. Sensitivity, by contrast, is a *preference* about
  *which* of several capable, available executors is nicer to use; downgrading it to a
  filter would needlessly strand work (the owner's locked decision). So quota-availability
  is layered on as a hard filter **without** changing the optimization goal:
  capability/cost-first still picks among the entries that survive the filter.
- **Why two fallback axes rather than one ladder.** Collapsing quota fallback into the
  escalation ladder would mean "ran out of Claude quota" climbs to a *stronger, costlier*
  executor — paying more for a problem that is about availability, not quality. Keeping
  the axes separate means a quota miss falls **sideways** to the next cheap available
  entry (often the free local model), and only a *gate* miss climbs for quality. Conflating
  them would silently inflate cost and muddy why a stronger executor was chosen.
- **Why per-provider secrets via vault, not env-forwarding all of them.** Env-forwarding
  every provider's token into every box would put *all* credentials in *every* dispatch
  regardless of which executor runs — widening the in-box token surface and coupling
  revocation (you cannot cheaply rotate one provider's key without touching the shared
  env path). vault brokering keyed by `SecretRef` resolves *only the chosen entry's*
  secret per dispatch, keeps each independently revocable (invariant 5), and reuses the
  brokering seam the publication tokens already use (ADR 036) rather than inventing a
  second auth path.

## Consequences

- **Design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or
  `docs/architecture/diagrams.md` lands with this ADR. The registry, router, and the new
  adapters are follow-on implementation tasks.
- **The spec stays present-tense — one Claude executor today.** Per ADR 040/041,
  `docs/spec/` describes what *is*: a single Claude CLI executor behind the
  `(harness, model) → branch` seam. The registry/router surface enters the spec only
  when it ships; it must not be written into the spec in the present tense before then.
- **The roadmap's Deferred "Multi-provider router" bullet is promoted to a
  Targeted/active item.** This ADR covers all three dimensions that bullet names —
  **quota** (the usage-aware availability filter), **sensitivity** (the soft hint), and
  **cost** (capability/cost-first). The roadmap edit (moving that bullet off Deferred) is
  a **separate change**, not part of this ADR.
- **ADR 041 is amended:** its executor IO seam changes from "a recipe binds one
  `ExecutorFactory`" to "a recipe declares a `RoutingSpec` the router resolves." ADR
  041's leaf-purity is **preserved** — the leaf recipe imports only the `RoutingSpec`
  value type; the registry and router are assembler-owned components the recipe never
  imports.
- **Tool registry is explicitly out of scope.** A registry of *tools* the executor may
  call is a sibling concern with its own trust boundary; it is deferred to a follow-on
  **ADR 044** and must not be conflated with this executor registry.
- **ADR 042's executor-selection open question (OQ-3) is answered:** the orchestrator's
  decomposition worker and its dispatched workers route through the registry like any
  other dispatch, so an orchestrator *can* route a sub-goal (or its own decomposition
  step) to a cheap local executor under the same capability/cost-first policy.
- **All load-bearing invariants survive:**
  - *Verification gate is the definition of done* — unchanged, and now **more** load-
    bearing: escalation rides entirely on gate pass/fail, so the gate is what makes
    weak-first routing safe. Nothing about the gate's blocking, no-skip character
    (F-002) changes.
  - *Executor seam `(harness, model) → branch`* — shape unchanged, and made literal: an
    entry is a **harness driver + (model, endpoint, auth) config**, so one harness (e.g.
    the Claude CLI) backs many entries, including a local model behind a translation
    proxy. This ADR additionally realizes the **quota**
    dimension of invariant 6's "mixing uneven-quality executors made safe by the gate":
    the router now routes *around* an exhausted provider (availability axis) as well as
    *up* from a failed one (quality axis).
  - *Secrets brokering* — extended, not weakened: vault brokers a *per-provider* secret
    keyed by `SecretRef`; each provider token is independently revocable (invariant 5).
  - *Containment + default-deny egress allowlist* — unchanged; the allowlist stays the
    load-bearing token-in-box control. The router changes *which* executor runs in the
    box, not the box's containment. A local entry additionally has **local-only egress**
    (no cloud API traffic), which the soft sensitivity hint can bias toward.
  - *Supervisor isolation (F-003)* — preserved: the router lives on the executor side of
    the injection boundary; `internal/supervisor` gains no LLM/router import. Re-run
    `make fitness-supervisor-isolation` when the router lands.
  - *No unattended self-modification* and *the internal planning hub is read-mostly* —
    untouched; the router selects executors, it does not author or reprioritize.
- **What becomes harder.** Reading a dispatch now means reading the routing spec → the
  registry → the router's selection, not a single inline executor construction. There is
  a new capability-tier/cost-weight model to keep calibrated (a mis-tiered entry routes
  work to the wrong executor). The router also gains **persistent, mutable quota state**
  with its own correctness concerns — a stale or rolled-back tally can over- or
  under-spend a provider — which is why it is memory-guarded in the orchestrator and
  clock-seam-tested. And the in-box token, while still a single token per dispatch, is
  now one of several possible provider tokens — operationally there are more credentials
  to provision and rotate. These costs are accepted as the price of multi-provider,
  quota-aware routing and the local-first cost saving.
- **Decomposition into tasks is the immediate follow-on** (each its own task + test
  spec; not enumerated here). The major clusters, at a high level:
  - the registry type + entry config (capability tier, cost weight, secret ref, provider
    config) and its in-process loader;
  - vault-brokered per-provider auth (extend `secrets.SecretSource` to resolve a named
    provider secret per `SecretRef`);
  - the harness adapters behind the seam — **smaller than four**: Claude CLI already
    exists, and the **local entry reuses it** (a Claude-CLI config variant pointed at a
    local endpoint, plus the **translation-proxy seam** that presents an
    Anthropic-compatible endpoint over an OpenAI-API local server), so only **Codex and
    Gemini** are genuinely new harness adapters;
  - **local-model selection / evaluation** — an empirical, **hardware-specific** task:
    benchmark locally-runnable LLMs against the host's RAM / VRAM / CPU / accelerator and
    pick the **most capable model that stays responsive** (no perceptible slowdown) for
    the local entry. The choice is **config** (the local entry's `ModelID`/`Endpoint`),
    not a hardcoded constant. Because local models improve constantly, this is
    **re-runnable** and worth periodically revisiting — it could be wired as a recurring
    eval on a cadence (a note, not a mandate).

    **Target hardware (probed 2026-06-27):**
    - **CPU:** Intel Core Ultra 9 185H (Meteor Lake) — 16 cores / 22 threads, up to 5.1 GHz
    - **RAM:** 62 GiB total (≈45 GiB available)
    - **GPU:** NVIDIA RTX 4060 Laptop (Max-Q/Mobile), **8 GB VRAM** (driver 595.71.05) + Intel Arc iGPU (Meteor Lake)
    - **Disk:** ~2.7 TB free; **OS:** Ubuntu 26.04 LTS, x86_64

    The **8 GB VRAM is the responsiveness ceiling** — models (or quantizations) that fit
    fully in VRAM run fast; the 62 GiB system RAM is capacity headroom for larger models
    via CPU/GPU offload, but offloaded layers degrade responsiveness. The eval optimizes
    "most capable that still fits the responsiveness budget," so the VRAM-resident sweet
    spot (≈7–14B at 4-bit, or a small MoE) is the likely region — but the actual pick is
    the benchmark's output, not assumed here. CUDA-capable (NVIDIA) backend is available;
  - the router + capability/cost model + escalation-ladder integration with the agent
    loop's existing retry→escalate path;
  - **usage/quota tracking** — the per-entry `Usage`/`Budget`/`Availability` state, its
    persistence (file for a single host run; memory-guarded store for the orchestrator),
    reset-window / cooldown handling, 429/`Retry-After` parsing, the injected clock seam,
    and the availability-axis fallback (route around an exhausted entry);
  - the recipe `RoutingSpec` field replacing the hardwired `ExecutorFactory` (the ADR
    041 amendment), with a behavior-preservation check that recipe #1 still routes to
    Claude with zero drift.
