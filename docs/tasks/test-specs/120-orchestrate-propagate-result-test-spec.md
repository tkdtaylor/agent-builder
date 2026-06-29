# Test Spec 120: Propagate the worker's real result

**Linked task:** [`docs/tasks/backlog/120-orchestrate-propagate-result.md`](../backlog/120-orchestrate-propagate-result.md)
**Written:** 2026-06-29
**ADR:** [055](../../architecture/decisions/055-orchestrate-plan-derived-authorization.md) (seam 3)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-120-01 | TC-001, TC-002    | ⏳ |
| REQ-120-02 | TC-002, TC-003    | ⏳ |

## Unit under test

The orchestrate dispatch closure in `internal/cli/orchestrate_seams.go`, which today
seals `supervisor.Result{OK: true}` (`:102`) regardless of the worker's actual
outcome — a false success. `runtimewiring.Run` returns `nil` on success and a
descriptive error on gate failure / executor error / idle (no ready task). The
dispatch must carry that real outcome into the result envelope and the reporter.

## Test cases

### TC-001: successful run reports OK

- **Requirement:** REQ-120-01
- **Setup:** dispatch with a spy worker runner returning `nil` (success).
- **Expected:** the sealed result is `OK == true`; the reporter receives a success report for the sub-goal.

### TC-002: a failed run reports NOT OK with the failure carried

- **Requirement:** REQ-120-01, REQ-120-02
- **Setup:** spy worker runner returns an error (e.g. gate failure).
- **Expected:** the sealed result is `OK == false`; the dispatch surfaces the error (does **not** swallow it into a hardcoded OK); the reporter receives a failure report identifying the sub-goal. Assert the result is not OK and the error text/verdict is present.

### TC-003: an idle worker (no ready task) is reported as not-done, not success

- **Requirement:** REQ-120-02
- **Setup:** spy worker runner simulating the "run idle: no ready task" outcome (a no-op run).
- **Expected:** the dispatch does **not** report OK — an idle worker that did nothing is reported as not-completed (the prior hardcoded `OK: true` masked this).

## Post-implementation verification

- [ ] `go test ./internal/cli/...` passes
- [ ] `make check` passes
- [ ] Cross-module trace: producer = `runtimewiring.Run` outcome; consumer = result envelope + reporter. A failing run must NOT surface as OK — assert directly.

## Test framework notes

- Go `testing`. Reuse the dispatch seam fake + a recording reporter from `internal/cli`
  tests. Depends on task 119 (the worker actually runs the dispatched task, so there is
  a real outcome to propagate). L5/L6 in the end-to-end run.
