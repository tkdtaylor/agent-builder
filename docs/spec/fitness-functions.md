# Fitness functions

**Project:** agent-builder
**Last updated:** 2026-06-05

## What this file is

Fitness functions are **executable architectural invariants** — automated checks that verify the code still obeys the rules this project commits to. Layering, coupling, dependency direction, performance budgets, security thresholds, complexity limits.

This file is the **declarative spec** for those checks. The implementation lives in the runner the rules point to (a Makefile target, a tool config, a pytest file). This file does not describe how the checks are coded — it describes which invariants the code must satisfy.

## Why this is separate from the rest of the spec

Three things in this project enforce alignment between the code and what the spec claims. They have different jobs and run at different times:

| Mechanism | What it guards | When it runs |
|-----------|---------------|--------------|
| `spec-coverage-check` hook | Active task's TC markers must have test references before commit | Pre-commit (git commit) |
| `architect` drift-audit mode | Spec docs and diagrams still describe what the code does | On demand, periodically |
| **Fitness functions (this file)** | **Architectural invariants the code must always satisfy** | **Continuously — `make fitness` locally, also at Stop in `strict` profile** |

The drift-audit asks *"do the docs still describe the code?"* — semantic, agent-driven, episodic. Fitness functions ask *"does the code still obey the rules?"* — mechanical, executable, continuous. Both matter; neither replaces the other.

## How to run

```bash
make fitness          # run all fitness functions
make fitness-<rule>   # run one rule by name (see table below)
```

Add new rules by:
1. Append a row to the **Rules** table below
2. Add a `fitness-<rule>` target to the Makefile that runs the underlying tool
3. Add `fitness-<rule>` to the `fitness` umbrella target's prerequisites

If a rule starts failing intentionally (e.g. the rule was wrong, or the constraint has been deliberately relaxed), update or delete the row in the same commit as the relaxed code — don't leave a dead rule in the table.

For tool selection per language, see `references/fitness-functions.md` in the create-project skill.

## Rules

Keep entries concrete: the rule must be checkable by a tool, and the threshold must be a number or a yes/no, not a vibe. Delete rules that are no longer load-bearing. Each row should be earnable — write a one-line *why* in the row's description so a future reader (or future-you) can tell whether the rule is still load-bearing.

| ID | Rule | Category | Asserts | Threshold | Check command | Severity | Why this rule earns its row |
|----|------|----------|---------|-----------|---------------|----------|----------------------------|
| F-001 | No Docker dev-environment references | structural/security | Working-tree scan reports no `docker`, `docker-compose`, or `Dockerfile` dev-environment references outside `containment/` and excluded narrative/tooling paths | 0 hits | `make fitness-no-docker` | block | The containment substrate is rootless Podman. Docker/devcontainer references outside the product-container artifact path would undermine the promised execution environment. |
| F-002 | Verification gate has no scanner bypass route | security | Production source under `cmd/agent-builder` and `internal/gate` exposes no `--no-verify`/skip flag, scanner-skip environment variable, or conditional early-return bypass around `dep-scan`/`code-scanner` | 0 bypass affordances | `make fitness-gate-blocking` | block | The verification gate is the definition of done. A silent scanner bypass would let unattended work complete without the security checks the gate promises. |
| F-003 | Supervisor import graph has no executor/LLM/web-fetch dependency | structural | `go list -deps ./internal/supervisor/...` reports no package path segment named `executor`, `executors`, `llm`, `llms`, `web`, `webfetch`, or `web-fetch` | 0 violations | `make fitness-supervisor-isolation` | block | The supervisor is trusted host-side control code. Keeping executor, LLM, and untrusted-content fetch code out of its transitive imports preserves the "dumb supervisor" boundary. |
| F-004 | Default run pipeline has no sandbox-runtime dependency | structural | `go list -deps ./internal/runtime/...` reports no package path containing `sandboxruntime` | 0 violations | `make fitness-no-srt` | block | ADR 021 swapped the rented `@anthropic-ai/sandbox-runtime` backend for the repo-owned Podman execution-box. Keeping `sandboxruntime` out of the production run pipeline's transitive imports is what makes the rented isolation a non-dependency; the package stays in the tree for reference, so this check, not deletion, enforces the swap. |

Categories: `structural` (cycles, layering, dependency direction), `hygiene` (logging, leftovers, debug code), `performance` (latency, throughput, memory), `complexity` (cyclomatic, file size, fan-out), `security` (deps, surface, secrets), `coverage` (test coverage thresholds).

Severity:
- `block` — fitness check exits non-zero; the runner reports a failure. Fix the violation or relax the rule deliberately.
- `warn` — surfaces in output but does not fail the runner. Use for budgets that may have a temporary justified excursion.

## Rules considered but rejected

> Negative space matters as much as positive space. When a fitness rule is *proposed* and rejected, record it here so the same rule isn't re-proposed every six months. Keep this section short — if it grows long, the project is rejecting too many rules and the bar may be too high.

None recorded.

## Source-of-truth links

> List which other spec files or ADRs each rule traces back to, so a reader can find the *why*.

- F-001 (no Docker dev-environment references) ← [SPEC.md](SPEC.md) §Fitness functions, [`configuration.md`](configuration.md) §Deployment configuration, [`../architecture/overview.md`](../architecture/overview.md) §The shape of a run.
- F-002 (verification gate has no scanner bypass route) ← [SPEC.md](SPEC.md) §Invariants and §Fitness functions, [`behaviors.md`](behaviors.md) §B-001 and §Implementation constraints, [`../architecture/overview.md`](../architecture/overview.md) §The shape of a run.
- F-003 (supervisor isolation) ← [SPEC.md](SPEC.md) §Fitness functions, [`architecture.md`](architecture.md) §4 Components, [`../architecture/overview.md`](../architecture/overview.md) §Components.
- F-004 (no sandbox-runtime in run pipeline) ← [ADR 021](../architecture/decisions/021-podman-default-containment-swap.md) decision 1, [`interfaces.md`](interfaces.md) §exec-sandbox `run()` seam, [`configuration.md`](configuration.md) §Removed variables.

## Notes

- Rules in this file are the *project's* commitments, not generic best practices. Don't add a rule because it's a popular metric — add it because violating it would break something this project promises.
- Fitness functions should fail fast and have low false-positive rates. A rule that flags 50 things every run gets ignored.
- The hook only runs at `strict` profile so fast iteration isn't slowed down. Run `make fitness` manually before any milestone where invariants matter.
