# Task 002: Gate orchestrator core + Verdict model

**Project:** agent-builder
**Created:** 2026-06-04
**Status:** backlog

## Goal
Replace the stubbed `Gate` with a concrete orchestrator that runs an ordered list of named, pluggable check Steps against a repo worktree and returns a structured `Verdict`, short-circuiting on the first failing blocking step with no skip path.

## Context
- Tech stack: Go 1.26
- Authoritative design: `autonomous-builder.md` §2 (verification gate as the definition of done; thin, blocking)
- Roadmap: `docs/plans/roadmap.md` Phase 0.1 — **Verification gate** (the one genuinely-missing necessary piece)
- Related ADRs: ADR required: gate orchestrator shape (Step interface + Verdict model + no-skip short-circuit)
- Dependencies: 001 (walking skeleton — supplies the `Gate` seam in `internal/supervisor/supervisor.go`)

## Requirements
| Req ID | Description | Priority |
|--------|-------------|----------|
| REQ-001 | Add a `Verdict` type capturing overall ok plus per-step results — each result holds step name, ok, captured output, and duration | must have |
| REQ-002 | Define a `Step` interface (`Name() string`; `Run(repoPath string) StepResult`) so individual checks are pluggable | must have |
| REQ-003 | `Gate.Verify(repoPath)` runs the configured steps in order, aggregates into a `Verdict`, short-circuits on the first blocking failure, and returns the `Verdict`; there is no skip/bypass route | must have |

## Readiness gate
- [ ] Test spec exists in `docs/tasks/test-specs/`
- [ ] All acceptance criteria have a linked REQ ID
- [ ] Blocking tasks complete: 001

## Acceptance criteria
- [ ] [REQ-001] A `Verdict` value reports overall ok and an ordered slice of per-step `StepResult`s (name, ok, output, duration)
- [ ] [REQ-002] Arbitrary checks satisfy the `Step` interface and can be registered into the gate in order
- [ ] [REQ-003] `Verify` returns ok only when every blocking step passed; the first blocking failure stops execution and remaining steps do not run; no input can skip a step

## Verification plan
- **Highest level achievable:** L2 only — internal seam, unit-test-covered. The gate is wired to no CLI/binary yet (that arrives with 003/023), so there is no live runtime path to observe.
- Unit tests drive the orchestrator with fake passing/failing/blocking steps and assert ordering, aggregation, short-circuit, and the absence of any skip route.
- **Cross-module state risk:** names new `Verdict` and `StepResult` types (data model) and the `Step` interface (interfaces).
- **Runtime-visible surface:** none yet (no CLI/binary wiring in this task).

## Out of scope
- Concrete check steps (003 native Go, 004 lint, 005 dep-scan, 006 code-scanner)
- CLI wiring of the gate into a runnable binary (023)

## Notes
- Updates `docs/spec/data-model.md` (Verdict/StepResult) and `docs/spec/architecture.md` (gate orchestrator boundary) in the same commit when implemented.
- Concrete Step implementations land in follow-on tasks; this task only delivers the orchestrator + model + interface.
