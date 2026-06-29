# Test Spec 131: LLM clarifier (deferred — could-have)

**Linked task:** [`docs/tasks/backlog/131-llm-clarifier.md`](../backlog/131-llm-clarifier.md)
**Written:** 2026-06-29
**ADR:** 056 — Conversational human-gated orchestrate front door (extends ADR 054/055/046)
**Priority:** could-have / deferred (do not implement before tasks 124–130 are ✅)

## Requirements coverage

| Req ID     | Test cases        | Covered? |
|------------|-------------------|----------|
| REQ-131-01 | TC-131-01         | ✅ |
| REQ-131-02 | TC-131-02         | ✅ |
| REQ-131-03 | TC-131-03         | ✅ |
| REQ-131-04 | TC-131-04         | ✅ |

## Test locations

- `internal/orchestrator/clarifier_llm_test.go` (new file) — TC-131-01, TC-131-02, TC-131-03
- `internal/cli/orchestrate_test.go` — TC-131-04

Test function names:
- **TC-131-01:** `TestLLMClarifierCallsInvokerWithGoalText`
- **TC-131-02:** `TestLLMClarifierParsesReadyResponse`
- **TC-131-03:** `TestLLMClarifierParsesQuestionsFromResponse`
- **TC-131-04:** `TestAssembleOrchestrateSelectsLLMClarifierWhenEnvSet`

## Unit under test

`internal/orchestrator/clarifier_llm.go` (new file) — `LLMClarifier` struct that
implements the `Clarifier` interface. It sends a structured prompt to the model via
the `Invoker` seam (the same narrow seam used by `LLMPlanner` in task 110), parses
the model's response for a readiness decision and optional questions, and returns a
`Clarification`.

`internal/cli/orchestrate.go` — `clarifierFromEnv` reads `AGENT_BUILDER_CLARIFIER`;
value `"llm"` constructs an `LLMClarifier` using the same resolver/invoker wiring as
`plannerFromEnv`; any other value (including unset) returns the `HeuristicClarifier`.

The `Invoker` seam is `internal/orchestrator/planner.Invoker` (task 100):
```go
type Invoker interface {
    Invoke(ctx context.Context, prompt string) (string, error)
}
```
`LLMClarifier` does NOT directly import `internal/executor` (F-010 invariant — same
constraint as `LLMPlanner`).

## Test cases

### TC-131-01: LLMClarifier calls the Invoker with the goal text as part of the prompt

- **Requirement:** REQ-131-01
- **Setup:** construct an `LLMClarifier` with a spy `Invoker` that records prompts
  and returns a stub response indicating "ready" (e.g. `"READY"`). Call
  `Clarify(supervisor.Task{ID: "goal-1", Spec: "add retry backoff to exec-sandbox in github.com/tkdtaylor/exec-sandbox"})`.
- **Expected:**
  - The spy `Invoker.Invoke` is called exactly once.
  - The prompt passed to `Invoke` contains the goal spec text (`"add retry backoff…"`).
  - `err == nil`.

### TC-131-02: LLMClarifier parses a READY response → Clarification.Ready=true

- **Requirement:** REQ-131-02
- **Setup:** spy `Invoker` returns a response indicating ready (the exact format is
  defined by the implementation — e.g. a JSON `{"ready":true,"questions":[]}` or a
  plain-text `READY` marker). Call `Clarify(goal)`.
- **Expected:**
  - `Clarification.Ready == true`
  - `len(Clarification.Questions) == 0`
  - `err == nil`
  The exact response format is an implementation detail of `LLMClarifier`; the test
  asserts the PARSED outcome, not the raw response string.

### TC-131-03: LLMClarifier parses a response with questions → Ready=false + Questions populated

- **Requirement:** REQ-131-02
- **Setup:** spy `Invoker` returns a response with one or more questions (e.g.
  `{"ready":false,"questions":["Which repo?","What should change?"]}`). Call `Clarify(goal)`.
- **Expected:**
  - `Clarification.Ready == false`
  - `len(Clarification.Questions) == 2` (or however many the stub returns)
  - `Clarification.Questions[0] == "Which repo?"` (exact match)
  - `err == nil`

### TC-131-04: assembleOrchestrate selects LLMClarifier when AGENT_BUILDER_CLARIFIER=llm

- **Requirement:** REQ-131-03 + REQ-131-04
- **Setup:** set `AGENT_BUILDER_CLARIFIER=llm` (via the test's `getenv` stub). Call
  `assembleOrchestrate`. Assert the assembled orchestrator's clarifier is an
  `*LLMClarifier` (type assertion or white-box check).
- **Expected:**
  - With `AGENT_BUILDER_CLARIFIER=llm`: the assembled clarifier is an `*LLMClarifier`.
  - With `AGENT_BUILDER_CLARIFIER` unset or `"heuristic"`: the assembled clarifier is
    a `*HeuristicClarifier`.
  - With `AGENT_BUILDER_CLARIFIER="unknown"`: `assembleOrchestrate` returns an error
    (fail-fast — unknown clarifier values are a configuration error, like unknown
    planner values).

## Post-implementation verification

- [ ] `go test -race -count=1 ./internal/orchestrator/... ./internal/cli/...` passes
  with all four TCs non-vacuous (hard assertions — not smoke tests)
- [ ] `make check` passes (lint + build + fitness green)
- [ ] F-010 (`make fitness-orchestrator-no-executor`) passes — `LLMClarifier` must
  NOT directly import `internal/executor`; it reaches the model only through the
  `Invoker` seam, which the CLI wires at assembly time
- [ ] `docs/spec/interfaces.md` updated: `LLMClarifier` documented alongside
  `HeuristicClarifier` under the Clarifier seam; `AGENT_BUILDER_CLARIFIER` env var
  documented with its two values (`heuristic` default, `llm` opt-in)
- [ ] `docs/spec/configuration.md` updated: `AGENT_BUILDER_CLARIFIER` added
  (value `llm` → LLMClarifier via ollama-native entries; any other harness fails
  closed with `ErrSingleShotUnsupported`, same as `plannerFromEnv`)

## Test framework notes

- Go `testing`. The spy `Invoker` follows the pattern used in task 110's
  `LLMPlanner` tests (`internal/orchestrator/planner/llm_planner_test.go`).
- This task is **deferred** — mark the task file as "could-have / lower priority"
  and do not begin implementation until tasks 124–130 are all ✅ verified. The test
  spec is written now so the seam contract is clear, not because implementation
  is imminent.
- The `Invoker` seam is shared with the `LLMPlanner`; the wiring pattern in
  `orchestrate.go`'s `plannerFromEnv` is the model for `clarifierFromEnv`.
- L5: the LLM clarifier requires a live ollama-native entry (qwen3:8b or equivalent).
  The scripted conversation replaces the heuristic stub with a real LLM call.
  L6: the Telegram round-trip with `AGENT_BUILDER_CLARIFIER=llm` proves the
  channel-abstract + LLM-backed claim.
