# Test Spec 002: Gate orchestrator core + Verdict model

**Linked task:** [`docs/tasks/completed/002-gate-orchestrator-core.md`](../completed/002-gate-orchestrator-core.md)
**Written:** 2026-06-04
**Status:** ready for implementation

## Requirements coverage
| Req ID | Test cases | Covered? |
|--------|-----------|----------|
| REQ-001 | TC-001, TC-005 | ✅ |
| REQ-002 | TC-002, TC-006 | ✅ |
| REQ-003 | TC-003, TC-004, TC-005 | ✅ |

## Test cases
### TC-001: Verdict aggregates per-step results
- **Requirement:** REQ-001
- **Input:** gate configured with two fake passing steps
- **Expected output:** Verdict.ok true; one StepResult per step, each with name, ok=true, captured output, and a non-negative duration, in order
- **Edge cases:** output from each step is preserved exactly in the corresponding StepResult

### TC-002: Step interface is pluggable
- **Requirement:** REQ-002
- **Input:** a custom type implementing `Name()`/`Run()` registered into the gate
- **Expected output:** the gate invokes `Run(repoPath)` with the supplied repo path and records its StepResult
- **Edge cases:** `StepResult.Name` returned by the step cannot override the registered `Name()`

### TC-003: Blocking failure short-circuits
- **Requirement:** REQ-003
- **Input:** [pass, failing-blocking, pass] steps
- **Expected output:** Verdict.ok false; the second step ran and failed; the third step did NOT run (assert via a probe flag on the fake)
- **Edge cases:** failure in the first step → no later step runs

### TC-004: No skip/bypass route
- **Requirement:** REQ-003
- **Input:** any gate configuration / repoPath
- **Expected output:** there is no parameter, flag, or env that causes a blocking step to be skipped; every blocking step is either run or short-circuited-past after a prior failure
- **Edge cases:** confirm no exported "skip" surface exists (compile-time / API review)

### TC-005: Empty step list is vacuously ok
- **Requirement:** REQ-001, REQ-003
- **Input:** gate configured with no steps
- **Expected output:** Verdict.ok true; Results is empty; no panic or hidden default checks are injected
- **Edge cases:** repeated Verify calls on an empty gate produce independent empty Verdict values

### TC-006: Invalid step registration fails closed
- **Requirement:** REQ-002
- **Input:** duplicate step names, blank step name, or nil step at gate construction
- **Expected output:** constructor returns an error and no Gate; callers cannot create an ambiguous ordered gate
- **Edge cases:** duplicate detection uses the registered `Name()` value

## Notes
Framework: Go `testing` (table-driven). Fakes implement the `Step` interface with configurable ok/output and a `ran` probe flag; no real subprocesses in this seam. Duration assertions require non-negative values, not wall-clock thresholds.
