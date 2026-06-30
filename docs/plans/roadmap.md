# Roadmap

**Project:** agent-builder
**Last updated:** 2026-06-30 (goal-alignment — north star + general-agent forward arc)

## North star

agent-builder is a **security-first general autonomous agent** — the Secure Agent Ecosystem's equivalent of OpenClaw / Hermes: persistent, extensible, self-improving, multi-LLM. It runs a **composed brain** (Claude Code first) inside the security envelope and owns the gateway, the multi-LLM router (the three brains are **local ollama**, **Claude**, and **`agy`/Antigravity** — the latter the successor to the `gemini` CLI, deprecated 2026-06-18), skills/memory governance, and security. **Coding is the first reference build, not the definition** — contributing to a repo and starting a project are skills among many. Self-improvement is **secure skill-writing**, never trusted-core self-modification; results and escalations return over the channel (CLI now, Telegram next) behind two human gates.

**Foundations (done).** Phases 0–2 below built and adopted the security envelope (exec-sandbox, audit-trail, vault, policy-engine, armor), the verification gate + supervised loop, the multi-LLM executor registry/router, and the conversational `orchestrate` front door. These are the proving ground; the forward arc builds *outward from* them toward the general agent — see **"Forward arc — the general agent"** below.

## Phase 0 — Bootstrap the loop (supervised, human-in-the-loop)

Goal: a runnable orchestrator that can take one task, route it to one executor, run the verification gate, and branch+PR — all inside rented isolation. This is the minimum to "flip the switch."

Acceptance status: Phase 0 is accepted at fake-provider L5 by the Task 032 end-to-end harness. Real Podman, `runsc`, real Claude, and real PR publication remain pending L6/operator evidence; local evidence still names the Podman/`runsc` blockers rather than implying live runtime acceptance. Note: `srt` (`@anthropic-ai/sandbox-runtime`) backed the Phase 0 run path as rented isolation but has been **removed** from the run pipeline in Phase 1 (ADR 021); it is historical for the run backend and is no longer a pending runtime — see the Phase 1 acceptance note below.

| # | Deliverable | Notes |
|---|-------------|-------|
| 0.1 | **Verification gate** (thin) | `go test` + `golangci-lint` + `dep-scan`/`code-scanner` as a blocking step. The one genuinely-missing necessary piece. |
| 0.2 | **Agent loop + escalation** | pick task → attempt → verify → retry-N → escalate → next. Stop condition is mandatory. |
| 0.3 | **Podman containment profile + egress allowlist** | rootless, read-only rootfs, no socket, default-deny egress. The outer box = exec-sandbox v0 Tier 1 minus orchestration. |
| 0.4 | **Supervisor** | dispatch one task, wall-clock kill, collect logs, teardown. Dumb by design. |
| 0.5 | **exec-sandbox adapter seam** | `run()` contract; backed by `@anthropic-ai/sandbox-runtime` today (rented). |
| 0.6 | **Single executor** (Claude Code CLI) | one executor first; router is later. |

## Phase 1 — First block: exec-sandbox v0

The agent's **first real task**. Build exec-sandbox v0 behind the adapter seam, then swap the rented isolation for it. Resolves the chicken-and-egg.

Acceptance status: Phase 1 is accepted at **fake-provider L5** by the Task 037 end-to-end harness. `agent-builder run` now dispatches through the repo-owned rootless **Podman** execution-box containment (`internal/sandbox/podman` behind the `sandbox.Runner` seam) instead of the rented `@anthropic-ai/sandbox-runtime` (`srt`) backend, which is **removed** from the run pipeline (ADR 021). The L5 harness drives the real `agent-builder` binary with a fake Podman launcher (`AGENT_BUILDER_EXEC_BOX_LAUNCHER`), fake Claude executor, a Gate-passing fixture worktree, and fake git/gh publication, asserting the run completes, the run record carries `containment=podman` launcher evidence, and zero `srt`/`sandbox-runtime`/`AGENT_BUILDER_SANDBOX_RUNTIME` references appear anywhere in stdout, stderr, or the run record.

- Harness command: `go test -count=1 -v ./tests/e2e -run TestPhase1EndToEndAcceptance` → `TC-037-01 Phase 1 accepted: task selected, Podman containment used, no srt invocation, run record clean`.
- Outstanding L6 blockers: live Podman with the `runsc` (gVisor) OCI runtime and a provisioned execution-box Gate-toolchain directory. The live harness `AGENT_BUILDER_LIVE_PODMAN=1 go test -count=1 -v ./tests/e2e -run TestPhase1LivePodman` skips when Podman/`runsc` are unavailable and fails (configuration error) when the Gate-toolchain directory is absent; on the current host Podman is installed but `runsc` and the Gate-toolchain directory are not provisioned, so L6 observation remains pending.

## Phase 2+ — Remaining blocks (self-leveraging order)

`audit-trail` → `policy-engine` → `vault` was the original self-leveraging order; the actual adoption order has been driven by which block shipped first. Each removes a specific human checkpoint and improves the agent that builds the next.

### Block-adoption status (as-built — read this before assuming a block is un-adopted)

This table is the single source of truth for *what agent-builder already consumes*. "Adopted" means wired into the run path behind a seam; "block ready" means the standalone block exists and is consumable but agent-builder does not use it yet.

| Block | Block state | Adoption into agent-builder | Evidence |
|---|---|---|---|
| **exec-sandbox** | shipped | **✅ Adopted** — default run backend | ADR 035; tasks 062–063; ran a full Phase-0 capstone on the real block backend (2026-06-18) |
| **audit-trail** | shipped (signed checkpoints, Rekor anchoring, rotation) | **✅ Adopted** — `emit` + `verify` gate (v0) **plus Ed25519 signed-checkpoint upgrade** (create at supervisor seal + `verify-checkpoint` CLI), opt-in via `AGENT_BUILDER_AUDIT_CHECKPOINT_*`; Rekor anchoring still deferred | ADR 026 + ADR 037; tasks 038–042 + 067–069 (merged 2026-06-19; checkpoint create/verify proven against the real `audit-trail` binary) |
| **vault** | shipped (v1 complete) | **✅ Adopted (L5)** — `internal/vault` client+lifecycle, `VaultSecretSource`, `sandbox.Request.Wiring` → exec-sandbox proxy mode; opt-in via `AGENT_BUILDER_VAULT_BIN`; brokers **git/GitHub tokens** (provider/Claude token deferred pending feasibility probe TC-066-07). ⏳ L6 in-box brokering capstone (TC-066-05/06) operator-pending — needs real creds | ADR 036; tasks 064–066 (merged 2026-06-19; client put/resolve proven live vs real vault daemon) |
| **policy-engine** | shipped (through block task 007, published remote, green gate) | **✅ Adopted (L5)** — host-side fail-closed `decide` gate: `internal/runtime` starts `policy-engine serve` as a Unix-socket daemon and calls AuthZEN `decide` after vault resolution and **before** `sandboxBox.Create`; `deny`/`require_approval` → needs-human (box never starts), `allow` applies `tier_select` + raise-only `vault_injection_floor` obligations. `internal/policy` is a stdlib-only leaf reached IPC-only (F-006 fitness). Opt-in via `AGENT_BUILDER_POLICY_BIN`; unset = no gate. ⏳ live decide-gate against the real binary is operator-gated (`AGENT_BUILDER_LIVE_POLICY=1`). ⚠️ The `RetryPolicy`/escalation-policy code in `internal/loop` (task 013) is the agent-loop retry policy and is **unrelated** to the policy-engine block. | ADR 038; tasks 070–074 (decision, decide client, daemon lifecycle + decide-gate, require_approval/audit_emit obligations, F-006 isolation fitness) — all ✅ verified |
| **armor** | shipped (LLM-guard block) | **✅ Adopted** — fail-closed web-ingestion + tool-call guard boundary (ADR 024): `internal/executorharness` routes executor-facing web content and tool-call candidates through the ingestion broker, exposing only broker-reviewed values to the executor; `internal/armor` adapts the external armor process into allow/block/quarantine decisions before release. The armor-backed guard is constructed when configured; the boundary itself is always present. | ADR 024; tasks 024–029 (boundary seam, guard adapter, ingestion wiring, executor harness, default-run wiring, Claude ingestion control) — all ✅ verified |
| **memory-guard** | v0 (single commit; write-gate + delete-verify deltas built) | 🎯 **Targeted (ADR 042)** — guards the orchestrator's long-lived goal/fleet state (write-gate + delete-verify). Promoted off Deferred by the secure-orchestrator decision; adoption is a follow-on task cluster. | block exists at `~/Code/Public/memory-guard` |
| **agent-mesh** | v0 (single commit; Ed25519 envelopes + replay prevention) | 🎯 **Targeted (ADR 042)** — orchestrator↔worker transport (Ed25519 signed envelopes + replay prevention). The two-tier orchestrator creates the multi-agent substrate it needs, so this moves off Deferred; adoption is a follow-on task cluster. | block exists at `~/Code/Public/agent-mesh` |

### Immediate order (decided 2026-06-19; updated as-built same day)

1. **vault adoption (tasks 064–066)** — ✅ **done (L5, merged 2026-06-19).** Closed the token-in-box risk for git/GitHub tokens (ADR 036). L6 in-box brokering capstone (TC-066-05/06) + provider-token feasibility probe (TC-066-07) remain operator-gated — see the L6 operator runbook.
2. **audit-trail signed-checkpoint upgrade (tasks 067–069)** — ✅ **done (merged 2026-06-19).** ADR 037 + signer seam/config + `verify-checkpoint` CLI; create/verify proven against the real `audit-trail` binary.
3. **policy-engine adoption (tasks 070–074)** — ✅ **done (L5, merged).** Host-side out-of-process fail-closed `decide` gate before box dispatch (ADR 038): `internal/policy` leaf client + `PolicyDaemon` lifecycle, `tier_select` + raise-only `vault_injection_floor` obligations, F-006 isolation fitness. Live decide-gate against the real binary is operator-gated (`AGENT_BUILDER_LIVE_POLICY=1`). With this adopted, the only un-adopted blocks left are **memory-guard + agent-mesh** (both deferred — see the table). A provider-token vault-brokering follow-on (gated on TC-066-07's outcome) also remains.

### audit-trail — signed-checkpoint upgrade (tasks 067–069) — ✅ done 2026-06-19

v0 (below) wired `emit` + `verify`. The block has since shipped **Ed25519 signed checkpoints** and Rekor anchoring — the forensic guarantee that turns "tamper-evident *if you trust the file on disk*" into "a signed attestation a third party can verify offline after the agent box is gone." ADR 026 explicitly deferred this ("Surfacing the block's signed-checkpoint / Rekor-anchor verbs … is a later integration"). Tasks 067–069 land it, sequenced **after the vault tasks (064–066)** by decision on 2026-06-19. Shape mirrors the vault adoption arc: ADR → signer seam + config → verify-checkpoint surface. Key management is a file-path signing key for this upgrade; brokering that key through vault is a forward-link to the (now-prior) vault tasks, not a prerequisite.

### audit-trail v0 (done)

**audit-trail v0** (ADR 026, supersedes ADR 025; tasks 038–042) **consumes the shipped `audit-trail` block** (`github.com/tkdtaylor/audit-trail`) rather than reimplementing it — agent-builder's role is the *first concrete consumer* of the blocks, not a re-builder. v0 ships a typed `audit.AuditEvent` taxonomy + `audit.Sink` seam (task 038), a `BlockSink` adapter that maps each event onto the block's `audit-trail emit` CLI (task 039), a `VerifyChain` helper surfacing the block's `audit-trail verify` as a block-severity gate (task 040), supervisor wiring behind `AGENT_BUILDER_AUDIT_RECORD` + `AGENT_BUILDER_AUDIT_BIN` (task 041), and the F-005 `fitness-audit-isolation` check keeping `internal/audit` a leaf that reaches the block over `os/exec` (task 042). v0 captures only the action events the run loop already emits (containment, pick, attempt, verify+verdict, publish, escalate, finish). The block owns the hash chain, RFC 8785 canonicalization, tamper-detection, and the already-shipped signing / Rekor anchoring / rotation.

> **Why consume, not build (ADR 026).** ADR 025 originally planned an in-repo hash-chained `ChainWriter` + `Verify`. A survey found the `audit-trail` block already ships that exact capability — frozen v1 emit/verify, RFC 8785, plus signed checkpoints, Rekor anchoring, and rotation that ADR 025 deferred. Building `internal/audit` from scratch would have duplicated a shipped, fitness-covered block and inverted agent-builder's "consumer of the blocks" thesis. ADR 026 reverses that to a consumer integration over the block's frozen CLI/IPC seam.

> **Deferred — egress-attempt audit events (conditional follow-up).** Per ADR 026 decision 2 (carried over from ADR 025), capturing egress *attempts* as audit events is **not** in v0 and is **spike-gated**: it becomes a task only if a short spike confirms the execution-box egress proxy already exposes attempts host-side. v0 does not block on a new containment data path. This gap is recorded here intentionally so it is not silently dropped — it is a Phase 2 conditional follow-up, not part of tasks 038–042.

> **Deferred — IPC-socket transport (upgrade path).** v0 uses the block's `audit-trail emit` CLI per action event (~7/run, action layer only). The block's Unix-socket IPC (`audit-trail serve`) is the throughput upgrade; the `BlockSink` seam is shaped so CLI→socket is an adapter-internal swap (ADR 026 Option B).

## Multi-LLM router (done — foundation for the brains)

**Multi-provider executor registry + router** (ADR 043) is **built and adopted (L5).** A registry of heterogeneous executors (one harness driver backs many model/endpoint/auth entries; the local LLM is either the Claude CLI harness via a translation proxy or the native Ollama harness) plus a quota-aware, capability/cost-first router (availability as a hard filter, gate-failure escalation up the capability ladder, quota-exhaustion fallback sideways, local model as the quota-free backstop). The **three live brains** are **local ollama**, **Claude**, and **`agy`/Antigravity** (tasks 133/134, L6-PASSED; the multi-model successor to the **`gemini` CLI, deprecated 2026-06-18** — `GeminiCLI` is retained only as a deprecated reference). Codex is an additional adapter, not one of the three canonical brains.

## Forward arc — the general agent

The foundations above are the coding reference build. The arc from here is **outward to the general agent** — the OpenClaw/Hermes capabilities not yet built. Each is its own slice (test-spec-first, ADR where it's a real decision); the live planning is tracked in `plan-full-zany-beaming-pie.md`.

1. **Composed-brain-as-general-executor** — a **non-coding execution path** for the cloud brains. Today only the Ollama-native single-shot `Completer` (ADR 053) answers without editing a repo; Claude and `agy` fail closed (`ErrSingleShotUnsupported`). This is the nearest slice: extend the `Completer` seam to the cloud brains + a general entrypoint, so a goal can be *answered* (not only turned into a branch). *(Foundation verified 2026-06-30: the local Completer answers live through the agent's own code path.)*
2. **General self-extending skill system** — coding (contribute-to-repo, start-a-project) becomes one skill among many; the agent selects/loads skills per goal.
3. **Secure skill-writing loop** — the agent authors and refines **reviewable, sandboxed skills** (Hermes-style self-improvement), never editing its own trusted core/gate/escalation. Reconciles with invariant 2.
4. **Persistent cross-session memory** — durable goal/skill/context memory across runs, guarded by **memory-guard** (write-gate + delete-verify); memory-guard moves off Deferred for this.
5. **Heartbeat / daemon (always-on)** — a persistent self-hosted daemon so the agent runs continuously, not only when invoked.
6. **Router breadth + channel result-handling** — broaden routing across the three brains by sensitivity/quota/cost, and complete the channel/gateway path (CLI now → **Telegram next**) so results and escalations return over the same channel, behind the two human gates. **agent-mesh** (Ed25519-signed orchestrator↔worker transport) moves off Deferred where multi-worker dispatch needs it.

> **Historical note (ADR 040/041/042).** An earlier framing cast the forward arc as a *"builder of purpose-built agents"* product surface (recipe seam, two-tier orchestrator). That decomposition shipped the control-plane plumbing the general agent reuses (recipe/IO seams, channel adapter, multi-goal orchestrator), but the **north star is the general agent itself, not an agent factory** — the components above, not "assemble many agents," are the arc. The two-tier orchestrator and recipe seams are foundations for it.

## Sequencing note

The agent was built against block *interfaces* (specced + contract-validated in the internal design hub), and its first task *implemented* exec-sandbox behind that interface — foundations-before-agent was preserved, not skipped. The forward arc continues that discipline: each general-agent slice composes existing blocks/seams behind a thin new seam, never grows the assembler per goal.
