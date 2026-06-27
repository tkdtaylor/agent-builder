# Roadmap

**Project:** agent-builder
**Last updated:** 2026-06-27

Derived from `autonomous-builder.md` §2, §8. This roadmap doubles as the agent's own work queue once it runs — but during bootstrap it is built by hand (supervised).

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

## Deferred (not bootstrap-critical)

- **Multi-provider router** (Claude + Gemini + local LLMs, quota/sensitivity/cost routing) — design the seam now, build as v1.
- **The "builder of purpose-built agents" product surface** — now the project's **primary forward arc** (ADR 040), with its shape decided in **ADR 041** (the agent-recipe seam) and **ADR 042** (the secure two-tier orchestrator). The foundational blocks have all shipped to v1 and are adopted, so the evolution from *the single autonomous coding agent* to *a tool that assembles any purpose-built secure agent from the blocks* is no longer gated on block readiness. **Decomposition into task clusters is now underway** (recipe seam + selectable IO seams; Telegram channel adapter + Ed25519 envelope + armor guard; orchestrator core; agent-builder worker recipe; agent-mesh + memory-guard adoption; orchestrator self-containment + policy + fleet audit; multi-worker dispatch). As a consequence, **memory-guard and agent-mesh have moved off Deferred to Targeted** in the block-adoption table above — agent-mesh becomes the orchestrator↔worker transport and memory-guard guards the orchestrator's goal/fleet state.

## Sequencing note

The agent is built against block *interfaces* (specced + contract-validated in the internal design hub), and its first task *implements* exec-sandbox behind that interface — foundations-before-agent is preserved, not skipped.
