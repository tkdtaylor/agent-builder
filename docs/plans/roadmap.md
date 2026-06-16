# Roadmap

**Project:** agent-builder
**Last updated:** 2026-06-16

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

`audit-trail` → `policy-engine` → `vault`. Each removes a specific human checkpoint and improves the agent that builds the next.

**audit-trail v0** (ADR 025, Option B; tasks 038–042) ships a typed `audit.AuditEvent` taxonomy + `audit.Sink` seam, a hash-chained NDJSON `ChainWriter`, an `audit.Verify` tamper-detector, supervisor wiring behind `AGENT_BUILDER_AUDIT_RECORD`, and the F-005 `fitness-audit-isolation` check. v0 captures only the action events the run loop already emits (containment, pick, attempt, verify+verdict, publish, escalate, finish).

> **Deferred — egress-attempt audit events (conditional follow-up).** Per ADR 025 decision 2, capturing egress *attempts* as audit events is **not** in v0 and is **spike-gated**: it becomes a task only if a short spike confirms the execution-box egress proxy already exposes attempts host-side. v0 does not block on a new containment data path. This gap is recorded here intentionally so it is not silently dropped — it is a Phase 2 conditional follow-up, not part of tasks 038–042.

## Deferred (not bootstrap-critical)

- **Multi-provider router** (Claude + Gemini + local LLMs, quota/sensitivity/cost routing) — design the seam now, build as v1.
- **memory-guard / agent-mesh** — multi-agent / long-horizon memory, out of scope.
- **The "tool to build agents" product surface** — the north-star evolution, after the blocks are usable.

## Sequencing note

The agent is built against block *interfaces* (specced + contract-validated in the internal design hub), and its first task *implements* exec-sandbox behind that interface — foundations-before-agent is preserved, not skipped.
