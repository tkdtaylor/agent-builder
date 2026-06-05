# Fitness functions

**Project:** agent-builder
**Last updated:** 2026-06-04

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

> Replace these example rows with the rules that actually hold for agent-builder. Keep entries concrete: the rule must be checkable by a tool, and the threshold must be a number or a yes/no, not a vibe. Delete rules that are no longer load-bearing. Each row should be earnable — write a one-line *why* in the row's description so a future reader (or future-you) can tell whether the rule is still load-bearing.

| ID | Rule | Category | Asserts | Threshold | Check command | Severity | Why this rule earns its row |
|----|------|----------|---------|-----------|---------------|----------|----------------------------|
| F-001 | *(example) No cycles between top-level packages* | structural | Module import graph is acyclic at the package level | 0 cycles | `make fitness-no-cycles` | block | Cycles compound across changes; once they appear, they require a refactor to remove. Catch at first introduction. |
| F-002 | *(example) Domain layer does not import infra* | layering | `src/domain/**` imports nothing from `src/infra/**` | 0 violations | `make fitness-layering` | block | The composability ADR commits to a one-way dependency. Drift here silently couples business rules to a specific store. |
| F-003 | *(example) No production `print`/`println!`/`console.log` outside an approved logger* | hygiene | Production source contains no direct stdout calls | 0 hits | `make fitness-no-print` | block | Stray prints leak into prod output, bypass log routing/redaction, and mask which call sites are real instrumentation. |
| F-004 | *(example) Cyclomatic complexity ceiling* | complexity | No function exceeds the complexity ceiling | ≤ 15 | `make fitness-complexity` | warn | Complexity above 15 correlates with bugs in this codebase's history; warn-only because exceptions sometimes have a reason. |
| F-005 | *(example) Zero high-severity vulnerabilities* | security | Dependency scan reports no high or critical CVEs | 0 high+ | `make fitness-deps` | block | Shipping with a known-exploitable dependency is a security regression that must be visible at commit time. |

Categories: `structural` (cycles, layering, dependency direction), `hygiene` (logging, leftovers, debug code), `performance` (latency, throughput, memory), `complexity` (cyclomatic, file size, fan-out), `security` (deps, surface, secrets), `coverage` (test coverage thresholds).

Severity:
- `block` — fitness check exits non-zero; the runner reports a failure. Fix the violation or relax the rule deliberately.
- `warn` — surfaces in output but does not fail the runner. Use for budgets that may have a temporary justified excursion.

## Rules considered but rejected

> Negative space matters as much as positive space. When a fitness rule is *proposed* and rejected, record it here so the same rule isn't re-proposed every six months. Keep this section short — if it grows long, the project is rejecting too many rules and the bar may be too high.

| Proposed rule | Why rejected |
|---------------|--------------|
| *(example) Per-file LOC ceiling at 500* | Some files are intentionally large (state machines, generated code). A blanket ceiling produced more carve-outs than coverage. Replaced by the per-file-with-ADR-exemption pattern in F-002 instead. |
| *(example) Test coverage ≥ 90%* | Coverage % is a lagging indicator that drives cosmetic test additions; the spec-coverage hook plus the spec-verifier agent give better signal at lower cost. |

## Source-of-truth links

> List which other spec files or ADRs each rule traces back to, so a reader can find the *why*.

- F-002 (layering) ← `architecture.md` §Components, ADR-002 (composability decision)
- F-003 (perf) ← `behaviors.md` B-001 (health-check contract)
- F-005 (security deps) ← top-level invariants in [SPEC.md](SPEC.md)

## Notes

- Rules in this file are the *project's* commitments, not generic best practices. Don't add a rule because it's a popular metric — add it because violating it would break something this project promises.
- Fitness functions should fail fast and have low false-positive rates. A rule that flags 50 things every run gets ignored.
- The hook only runs at `strict` profile so fast iteration isn't slowed down. Run `make fitness` manually before any milestone where invariants matter.
