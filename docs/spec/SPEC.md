# agent-builder — Authoritative Spec

**Project:** agent-builder
**Last updated:** 2026-06-05
**Status:** Phase 0 implementation snapshot. The core Gate, loop, supervisor lifecycle, CLI, containment launcher contracts, sandbox-runtime adapter, ingestion/armor harness seams, and branch/PR artifact publication are implemented as described here. Phase 0 end-to-end acceptance is recorded at fake-provider L5 by the Task 032 harness; real Podman, `runsc`, real `srt`, real Claude, and real PR publication remain pending L6/operator evidence where local tooling is unavailable.

> Authoritative design source: `autonomous-builder.md`. This SPEC is the in-repo snapshot; where they disagree, reconcile in the same change.

## System summary

agent-builder is a Go orchestrator that runs an autonomous coding agent unattended to build the secure-agent ecosystem blocks. In the current Phase 0 implementation it can read ready task metadata, route one task through the Claude CLI executor seam, run the attempt through Podman execution-box-backed containment wiring, verify the result with the machine-checkable Gate, publish the verified non-empty branch as a PR artifact through the git/GitHub CLI publisher seam, record run evidence, and escalate exhausted attempts through the constrained task-status writer.

## Spec index

| Doc | Covers |
|-----|--------|
| [behaviors.md](behaviors.md) | What the system does — the loop, routing, escalation |
| [architecture.md](architecture.md) | Components: supervisor, agent loop, executor seam, containment, gate |
| [data-model.md](data-model.md) | Task, executor, verdict, run-record shapes |
| [interfaces.md](interfaces.md) | CLI surface; executor `(harness, model) → branch` contract; exec-sandbox `run()` seam |
| [configuration.md](configuration.md) | Egress allowlist, executor auth/token handling, resource limits |
| [fitness-functions.md](fitness-functions.md) | Executable invariants (see below) |

## Invariants (load-bearing)

1. **Verification gate is the definition of done.** No task completes unattended without the gate (tests + build + lint + scanners) passing. The gate is the only ground truth.
2. **No unattended self-modification.** The agent reads its own repo but never edits it autonomously.
3. **the internal planning hub is read-mostly.** Roadmap is input; the agent may flip task status, never author/reprioritize.
4. **One task = one repo = one branch.** No cross-repo sprawl within a task.
5. **Containment is rootless Podman + tiered runtime + default-deny egress allowlist.** The allowlist is the load-bearing control for the accepted token-in-box risk.
6. **Executor seam is `(harness, model) → branch`.** Pluggable; mixing uneven-quality executors is made safe by the gate (fail → escalate to a stronger executor).
7. **Secrets:** executor auth tokens may live in the box (accepted risk — flat-rate/no-overage + tight allowlist + revocability + scanners). vault is for *task* secrets, not executor auth.

## Fitness functions

- **F-003 — supervisor has no LLM/untrusted-content dependency:** implemented by `make fitness-supervisor-isolation`; the supervisor package import graph contains no executor/LLM/web-fetch packages.
- **F-001 — no Docker dev-environment references:** implemented by `make fitness-no-docker`; working-tree files contain no `docker`/`docker-compose`/`Dockerfile` dev-environment references outside the allowed product-container path.
- **F-002 — gate is blocking:** implemented by `make fitness-gate-blocking`; production gate and CLI source expose no `--no-verify`/skip route around `dep-scan`/`code-scanner`.

See [fitness-functions.md](fitness-functions.md) for executable rule definitions and commands.
