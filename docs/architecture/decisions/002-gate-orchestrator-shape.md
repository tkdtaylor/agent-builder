# ADR 002: Gate orchestrator shape

**Date:** 2026-06-05
**Status:** Accepted

## Context

The verification gate is the unattended builder's definition of done. The
walking skeleton exposed only a boolean gate seam, which was enough to compile
but not enough to explain which check ran, what it emitted, or where execution
stopped. Follow-on tasks add concrete checks, so the core must establish the
thin orchestration contract first.

## Decision

The gate is a small ordered orchestrator. Callers construct it with `Step`
implementations, and `Verify(repoPath)` runs those steps in registration order.
Each step has a stable `Name()` and a `Run(repoPath string) StepResult` method.

`Verify` returns a `Verdict` containing the overall `OK` value and an ordered
slice of `StepResult` values. Each result records the registered step name,
whether it passed, captured output, and measured duration.

All configured steps are blocking. The first failing step short-circuits the
gate, and there is no skip or bypass option in the API.

## Consequences

- Concrete checks remain pluggable and independently testable.
- Failure output is preserved for later CLI, log, and escalation rendering.
- A failing check cannot be bypassed by passing a flag, environment variable, or
  alternate Verify parameter.
- Registration rejects nil, blank-name, or duplicate-name steps so Verdict
  output remains unambiguous.
