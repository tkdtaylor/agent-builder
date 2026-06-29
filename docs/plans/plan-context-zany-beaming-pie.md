# Plan Context

# IMMEDIATE — wire Gemini via your OAuth subscription (the next build)

**Why now:** the user has a paid Gemini subscription and wants it used ASAP. We have Claude + local
built; Gemini is the missing third brain in the multi-LLM router.

**Established state (verified):** task **090** built the `GeminiCLI` executor
(`internal/executor/gemini_cli.go`, wired in `buildExecutorForEntry`) — but it is **L6-deferred
(never run live)** and **API-key-only**: it always resolves `GEMINI_API_KEY` via
`SecretRef → NamedProviderToken` and injects it, erroring `ErrGeminiSecretNotFound` with no key.
The `gemini` CLI is **not installed** on this host. The user's subscription authenticates via
**OAuth login (Google account)** — so we mirror **ADR 033** (Claude subscription OAuth), where an
empty-`SecretRef` entry needs no cloud key and `ConfigFromEnv` (run.go:243–260) already exempts it.

**Operator prerequisites (user does these — never paste secrets in chat):**
1. Install the Gemini CLI (`gemini`) on this host.
2. Run `gemini` once and log in with the Google account so the subscription credentials cache in
   `~/.gemini`.

**Task 132 — Gemini subscription/OAuth auth path (test-spec-first):**
- `internal/executor/gemini_cli.go`: add a **subscription mode** keyed on `entry.SecretRef == ""`
  — SKIP `NamedProviderToken`, SKIP injecting `GEMINI_API_KEY`; run the `gemini` subprocess
  inheriting `HOME` so it uses its cached OAuth login. **Preserve the existing API-key path** when
  `SecretRef != ""` (don't regress task 090). `geminiEnv` must not inject a blank/placeholder key
  in subscription mode.
- Registry/config: a Gemini subscription entry has `SecretRef == ""`; the existing "all enabled
  entries local → skip cloud-credential check" exemption already covers it — verify a gemini
  subscription entry registers, the router selects it, and no `GEMINI_API_KEY` is required.
- ADR: reference **ADR 033**; add a short ADR (or amendment) only if the gemini CLI's
  self-managed-credential mechanism differs materially enough to warrant its own record (it likely
  does — Claude injects a token env var; gemini relies on its own cached login). Executor decides.
- Spec: `configuration.md` (Gemini subscription entry, no key), `interfaces.md` (executor auth modes).

**Verification:**
- L2/L3: unit-test both branches — `SecretRef == ""` → no key resolution, no `GEMINI_API_KEY`
  injected, command built correctly; `SecretRef != ""` → existing API-key path unchanged
  (regression). `make check` green.
- **L6 (the point):** with the CLI installed + the user logged in, register a gemini subscription
  entry, route a scoped goal to it, and confirm a **gate-passing branch produced via the
  subscription** (not a metered key). This is what makes Gemini actually usable.

---

# North star — a security-first OpenClaw/Hermes (grounded 2026-06-29)

> **SUPERSEDES the front-door rework below.** After inspecting the create-project/autopilot
> skills and researching the named inspirations, the goal is bigger than a conversational
> front door. The front-door rework (124–131) is the wrong altitude and is subsumed by this.

**The agent:** a **persistent, extensible, self-improving, multi-LLM autonomous agent** in the
mold of **OpenClaw** (Gateway → Brain/ReAct loop → Memory → Skills → Heartbeat; runs continuously
on user hardware) and **Hermes** (self-hosted daemon; cross-session memory; cron; 16+ channels;
200+ LLM backends; **writes its own reusable skills from experience without modifying the model**).

**The differentiator:** those frameworks are local-first but not security-hardened. agent-builder
runs the same kind of agent **inside the Secure Agent Ecosystem envelope** — exec-sandbox,
policy-engine + egress allowlist, vault-brokered secrets, audit-trail, armor. Security from the
ground up is the whole point.

**General, not just coding:** contributing to a repo and starting a project are **two skills**
among many. The agent is general and extensible.

**Self-improvement = secure skill-writing, NOT self-modification.** Hermes-style: the agent
authors/refines *skills* (reviewable, sandboxed capabilities), never edits its own trusted
core/gate. This reconciles "self-improve securely" with the "no unattended self-modification"
invariant — improvement lives in the capability layer.

**Component map — current state:**
- Gateway/channels: control plane + Telegram/CLI intake — KEEP & extend.
- Multi-LLM routing: partial — the `(harness, model)→` executor seam + router (Claude/local;
  **Gemini routing is a stated gap**). Hermes-style provider breadth (Claude/Gemini/local) is wanted.
- Secure execution: exec-sandbox/policy/vault/audit/armor — largely built; the moat.
- Brain (ReAct loop): MISSING — today is single-shot dispatch, not an autonomous reasoning loop.
- Skills/plug-ins: partial (recipes) — not a general, self-extending skill system.
- Persistent cross-session memory: MISSING.
- Heartbeat/daemon: MISSING — invoked, not always-on.
- Self-improvement loop: MISSING.

**Disposition of in-flight work:**
- 118–122 (plan-derived authz, route-to-worker, real result, plan-scoped allow): valid secure
  control-plane plumbing — keep.
- 123 (blocked-action reevaluation wiring): valid; merge after re-verify.
- 124–131 (conversational front door): WRONG ALTITUDE — subsume. Keep approval/policy as security
  gates and the channel gateway; drop the rigid clarify→confirm→plan→approve Go state machine as
  "the agent." Do not execute 124–131 as written.

**OpenClaw/Hermes are INSPIRATION ONLY — never components.** They are insecure by design; building
a *secure* equivalent of what they do is the whole reason for this project. They define the
capability/vision target (persistent, extensible, self-improving, multi-LLM autonomous agent). We
never run, fork, or route to them.

**Brain — RESOLVED:** compose a capable agent inside the envelope. agent-builder owns the
gateway, multi-LLM router, skills/memory governance, and security; the composed agent does the
reasoning. The **brains/harnesses we route across are LLM CLIs**: **Claude** (built) + **local
ollama-native** (built) + **Gemini (NEXT — immediate priority; the user has a paid subscription)**.
The router chooses the brain/model by cost / sensitivity / capability / quota. Start the coding
skill with Claude (already the executor in the sandbox — a composed brain already exists there).

**How we work with the final result (recorded):** results and status flow back over the same
**channel/gateway** the request arrived on (CLI now, Telegram next). For the coding skill
specifically, the deliverable is a **gate-verified branch/PR on a target repo** for human review
before merge. Two human gates by design — plan/action approval (a *security* gate) and result
review — plus needs-human escalation over the channel; the agent never self-grants authorization
or edits its own trusted core.

## Context

The `orchestrate` subcommand is agent-builder's front door: accept a goal, plan it, assemble
the blocks to achieve it. Today it **plans immediately** on goal receipt and dispatches as soon
as policy says `Allow` — there is no conversation and no guaranteed human gate. That is not the
intended UX. The desired flow is:

1. **Conversational intake** — accept a goal *and* follow-up messages; the agent asks clarifying
   questions; the user replies; this continues until the **user explicitly confirms** the goal is
   ready.
2. **Plan** the clarified goal.
3. **Approval gate (default ON)** — the user approves the plan before anything executes; an
   operator opt-out restores auto-dispatch for trusted deployments.
4. **Execute** on approval.
5. All human touchpoints — clarifying questions, approval requests, **and** needs-human
   escalations — flow back over the **same channel/Reporter** (CLI stdin *and* Telegram).

Confirmed requirements: channel-abstract intake (both CLI + Telegram); agent asks questions /
user confirms; approval default-on but configurable.

This is mostly **rewiring on top of machinery that already exists** — the pause/resume approval
(ADR 046), the info-fold-at-checkpoint, and the one shared Reporter seam. It also **subsumes a
real defect** found this session: task 123's needs-human escalation writes to a task *file's*
Status line, which errors on the orchestrate path's synthetic goal IDs (`goal-N`) — the
redesign routes escalation over the Reporter instead.

**Relationship to the original ask** (run 119–122 end-to-end, promote 118–122 to ✅): deferred
behind this rework. The current flow can't reach a clean L6 (the escalation sink is broken and
the front door isn't the intended one). Once this lands, one end-to-end run validates the new
flow *and* the 118–123 stack, and the rows promote together.
