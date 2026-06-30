# IMMEDIATE — wire Gemini via your OAuth subscription (the next build)

> **SUPERSEDED (2026-06-30).** The `gemini` CLI was **deprecated 2026-06-18**; **`agy`/Antigravity is the
> live third brain** (its multi-model successor — tasks 133/134, L6-PASSED; ADR 057). This "wire Gemini"
> block is retained as history. Read the **"North star"** section below (still current) and substitute
> `agy` wherever this block says "Gemini." The third brain is **built**, not pending.

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

## Proposed next actions (lock in the redefined direction — NOT building the agent yet)

1. **Record** the grounded north star + result-handling in project memory (update
   `orchestrate-agent-goal-and-result-handling`).
2. **Draft ADR 056 — reposition to a security-first OpenClaw/Hermes** (composed brain in the
   envelope; general/extensible; self-improvement = secure skill-writing, not self-modification;
   multi-LLM routing). Relates to ADR 040 (prior repositioning), 046 (orchestrator v1), 055
   (dispatch loop). Records the first design decision (which brain first → Claude Code) and the
   component gaps (gateway, router+Gemini, memory, heartbeat/daemon, general skill system,
   skill-writing self-improvement).
3. **Re-scope the backlog:** keep 118–122 (secure control-plane plumbing); re-verify + merge 123;
   **park 124–131 as superseded** (preserve the channel-gateway + approval-as-security-gate ideas;
   drop the rigid clarify→confirm→plan→approve state machine as "the agent"). Don't delete — annotate.
4. **Stub a roadmap** for the new components, each to be planned as its own slice later.

This is a recording/repositioning step, not agent-building — so we stay aligned before any new code.

---

# (SUPERSEDED) Rework the orchestrate front door — conversational, human-gated flow

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

## What already exists (reuse — do not rebuild)

- **Message protocol** `internal/supervisor/message.go`: `MsgNewGoal/MsgStatus/MsgInfo/MsgCancel`.
- **Approval pause/resume** `internal/orchestrator/orchestrator.go`: `DecisionRequireApproval` →
  store plan, `StateAwaitingApproval`, `renderApprovalRequestWithInfo`, `Resume`/`ResumeWithFold`.
- **Actor linger + info-fold** `internal/cli/goal_actor.go`: `drainCommands` keeps the actor alive
  draining its mailbox during AwaitingApproval — the intake phase reuses this exact pattern.
- **One shared Reporter** (`Report(ctx, text) error`) flows to orchestrator core + control-loop
  router + (Telegram) ReplyAdapter — the channel-abstract outbound seam for all human touchpoints.
- **Planner seam** `plannerFromEnv` (StructuredPlanner default; LLM via `AGENT_BUILDER_PLANNER=llm`,
  ollama-native) — the clarifier mirrors this wiring shape.

## ADR 056 — Conversational human-gated orchestrate front door

One ADR (extends 054 control plane, 055 dispatch loop, 046 approval). Records five sub-decisions:
(1) `StateClarifying` precedes `StatePlanning`; `Handle` splits into intake + plan-onward. (2)
`MsgConfirm` is a first-class, channel-abstract message kind (not a magic string). (3) `Clarifier`
is a narrow seam — heuristic v1 default, LLM opt-in. (4) `AGENT_BUILDER_REQUIRE_APPROVAL` (default
true) is operator config orthogonal to policy risk. (5) all human touchpoints flow over the
Reporter; escalation moves off the file-backed `tasksource.StatusWriter`.

## Prerequisite — merge task 123 first

Task 123 (`dispatchPlan → ReevaluateBlockedSpawn`) is implemented + L2/L3-green on its worktree
branch but **unmerged**, sitting on a re-verified BLOCK fix (StatusWriter hard-assertion +
reevaluation-bound spec reconcile). **Re-run spec-verifier → merge it as-is**, then task 130
*replaces* its file-backed sink with the Reporter-backed one. Do not leave it half-redesigned.

## Task breakdown (test-spec-first, one responsibility each, own branch)

| Task | Responsibility | Key files |
|------|----------------|-----------|
| **124** | `MsgConfirm` message kind (protocol only; append to enum, preserve iota) | `internal/supervisor/message.go`, `docs/spec/interfaces.md`, `data-model.md` |
| **125** | CLI grammar: `confirm <goalID>` → `MsgConfirm` | `internal/cli/router.go`, `docs/spec/interfaces.md` |
| **126** | Telegram derivation: `confirm`/`go`/`proceed` (reply-to threads goalID) → `MsgConfirm` | `internal/channel/telegram/adapter.go`, `docs/spec/interfaces.md` |
| **127** | `StateClarifying` registry state + route `MsgConfirm` to the goal mailbox (widen `routeCommand`/`handleCommand`) | `internal/orchestrator/registry.go`, `internal/cli/orchestrate.go`, `goal_actor.go`, `docs/spec/behaviors.md` |
| **128** | **Core:** `Clarifier` seam + `HeuristicClarifier` v1 + intake state machine (split `Handle` → `BeginGoal` + `ConfirmAndPlan`; actor lingers in Clarifying, folds `MsgInfo`, plans on `MsgConfirm`). Includes `AGENT_BUILDER_INTAKE=auto` escape hatch for non-interactive L5. | `internal/orchestrator/clarifier.go` (new), `orchestrator.go`, `internal/cli/goal_actor.go`, `docs/spec/{behaviors,interfaces}.md`, `docs/architecture/diagrams.md` |
| **129** | Approval-default: `AGENT_BUILDER_REQUIRE_APPROVAL` (default true) forces the pause on policy-ALLOW plans; opt-out auto-dispatches. Extract existing pause body into `pauseForApproval`. | `internal/orchestrator/orchestrator.go` (+`WithRequireApproval`), `internal/cli/orchestrate.go`, `docs/spec/{configuration,behaviors}.md` |
| **130** | Escalation over the channel: `reporterStatusWriter` implements `loop.StatusWriter` via `Reporter.Report` — replaces task 123's file sink (closes the synthetic-goal-ID gap). | `internal/cli/orchestrate_seams.go` (new type), `orchestrate.go`, `docs/spec/{behaviors,configuration}.md` |
| **131** *(deferred)* | LLM clarifier behind `AGENT_BUILDER_CLARIFIER=llm`, reusing the planner's resolver/invoker seam | `internal/orchestrator/clarifier_llm.go` (new), `orchestrate.go`, `docs/spec/configuration.md` |

124–127 are small protocol/routing steps; 128 is the substantive rework. Recommend implementing
124–127 as a quick batch, then 128, 129, 130 in order.

## Intake state machine (task 128)

Split the single `Handle` call by **extraction, not rewrite** (the risk is regressing the gated
paths, so move existing code verbatim):

- **`orch.BeginGoal(ctx, goal)`** — sets `StateClarifying`, runs `Clarifier.Clarify(goal)`. If
  questions → `Reporter.Report(questions)` and linger. If ready → prompt "reply `confirm` when
  ready" (still waits for explicit confirm, per requirement).
- **Clarifying-linger loop** (mirrors the AwaitingApproval drain): `MsgInfo` → fold into goal text
  (existing `EnqueueInfo`/`FoldGoalText`) + re-clarify; `MsgConfirm` → `orch.ConfirmAndPlan`;
  `MsgCancel` → existing teardown (no plan yet, cleanly had=false); `ctx.Done` → sweep + exit.
- **`orch.ConfirmAndPlan(ctx, goal)`** — the **current body of `Handle` from `planner.Plan`
  onward**, byte-for-byte. Approval / cancel / info-fold / dispatch / escalation all live inside or
  after this and stay untouched.

`Clarifier` interface: `Clarify(goal) (Clarification{Ready bool, Questions []string}, error)`.
`HeuristicClarifier` v1 (no LLM): empty/very-short spec → ask what to build; no repo/target named →
ask which repo; else `Ready`. Small, deterministic, unit-testable. The interface is the seam; the
heuristic is replaceable by 131.

## Approval-default (task 129)

`requireApproval bool` field (default **true** in `New`) + `WithRequireApproval` option, read from
`AGENT_BUILDER_REQUIRE_APPROVAL` (lenient `false`/`0`/`no` → opt-out). Interpose in the
`DecisionAllow` branch: if `requireApproval`, take the **same** AwaitingApproval pause as
`DecisionRequireApproval` (extract into `pauseForApproval(ctx, plan)`); else `dispatchPlan`. Approve/
reject resumes via the existing `Resume`/`ResumeWithFold`. One pause, two triggers.

## Escalation over the channel (task 130)

`reporterStatusWriter{reporter}` satisfies `loop.StatusWriter.WriteStatus(taskID, status)` by
calling `Reporter.Report("needs-human: goal … escalated (…)")` — no filesystem, so it works for
synthetic goal IDs. Assemble it in `orchestrate.go` and pass via `WithStatusWriter`, replacing the
`tasksource.NewStatusWriter(baseConfig.TaskRoot, …)` from task 123.

## Verification

- **Per-task L2/L3:** unit tests for each protocol/state/clarifier piece; `make check` green.
- **L5 (scripted, deterministic, default heuristic clarifier):** drive env/stdin `MessageSource`
  with a scripted conversation — vague goal → bot asks a question → `info <id> <answer>` → "reply
  confirm" → `confirm <id>` → approval pause (default-on) → approval → dispatch. Assert (hard, not
  smoke) the three Reporter lines: the clarifying question, the approval solicitation, the
  `RenderPlanResult` summary. StructuredPlanner + real policy-engine binary
  (`~/Code/Public/policy-engine/policy-engine`) with a plan-covering allow set. The
  `AGENT_BUILDER_INTAKE=auto` hatch (task 128) lets CI run a no-human variant. New
  `scripts/validate-orchestrate-intake.sh` pipes the conversation and greps the assertions.
- **L6 (operator-observed):** Telegram round-trip — vague goal → question → reply → `go` →
  approval → approve → plan result (proves the channel-abstract claim). Plus a policy-deny scenario
  so reevaluation exhausts and the `needs-human` line arrives over the channel for a synthetic goal
  ID (proves task 130). qwen3:8b available for a live executor if dispatching to completion.
- **Then** promote rows 118–123 to ✅ off this end-to-end run, in separate `verify:` commits.

## Deferred for the minimal first cut

LLM clarifier (131); bare `go`/`proceed` no-goalID convenience (require `confirm <goalID>` first);
keep `ConfirmAndPlan` byte-identical to today's post-plan body (extraction, not rewrite).

---

## Gap analysis — what 124–131 does and does NOT deliver (verified 2026-06-29)

Three read-only investigations confirmed what a completed 118–131 backlog gets us, against the
north-star goal (a secure agent you give a goal and it works toward it autonomously).

**Delivered by 118–131:** a secure, conversational, human-gated **single-shot dispatcher** — talk
to it locally (and via Telegram), clarify, confirm, get a plan, approve it, and it securely
dispatches a worker that does real gate-verified work and reports back over the channel. Good
enough to validate the *communication + secure-dispatch* milestone.

**NOT delivered — three substantive gaps, all OUTSIDE this backlog:**

1. **Planning is a pass-through, not decomposition.** The default `StructuredPlanner` collapses a
   free-text goal into ONE sub-goal and hands the raw prose to a single worker (asserted by
   `TestTC081_01_FreeFormGoalCollapsesToSingleSubGoal`). The `LLMPlanner`
   (`AGENT_BUILDER_PLANNER=llm`) does real decomposition but is **ollama-only** (cloud harnesses
   fail closed), single-shot, and tested only on trivial goals. *Mitigant:* with a capable
   executor (Claude CLI), the **worker decomposes inside the box** — so a scoped goal still works;
   the orchestrator just isn't doing the planning. Per ADR 046 this is the deliberate v1 choice.
2. **No sustained-autonomy loop.** `Handle` → `dispatchPlan` → report → **stop**. No re-plan, no
   retry, no iterate-to-completion at the orchestrator layer; a failed sub-goal is recorded
   best-effort and dropped. "Keep working until the goal is met" lives only in the `/autopilot`
   **skills** layer (Claude Code agents), a different execution model from this secure binary.
3. **Conversational target-repo + delivery setup.** Bare-stdin and Telegram goals **cannot name a
   target repo** today (only the `AGENT_BUILDER_GOAL_REPO` env path sets `Task.Repo`). Real
   delivery (push + `gh pr create --fill`) is wired and live-proven on the single-task `run` path,
   but needs a **real cloned target repo + remote + gh/GitHub auth + provisioned gate-tools**, and
   the orchestrate path also needs `AGENT_BUILDER_WORKER_SIGNING_KEY`. The self-repo bright line
   (refuses to target agent-builder itself) works.

**Smallest gap that directly blocks the local validation milestone:** conversational target-repo
selection (gap 3) — a `repo <goalID> <url>` style command or a goal-line convention, plus
populating `Task.Repo` on the stdin/Telegram paths. Worth a task (132) before the validation run.
Gaps 1 and 2 are the "real autonomy" builds and are larger, separate epics (deferred by ADR 046).
