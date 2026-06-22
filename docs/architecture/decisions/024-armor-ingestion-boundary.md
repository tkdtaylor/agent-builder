# ADR 024: armor ingestion and tool-call boundary

**Date:** 2026-06-05
**Status:** Accepted (implemented — tasks 024–029, all ✅ verified)

## Context

The Phase 0 design requires armor on the attacker-reachable web-ingestion and
tool-call path. The current codebase does not yet have a repo-owned seam for
that path: the agent loop picks a task and calls `supervisor.Executor.Run`, and
the Claude Code CLI executor then owns its own tool loop inside the subprocess.

That means the original task 024 cannot be implemented as a small wire-up. There
is no typed artifact for fetched web content, no typed tool-call request before
execution, and no interception point that can block or quarantine flagged
content before it reaches executor context.

The supervisor must also remain trusted and dumb. It must not import web-fetch,
LLM, executor-tooling, or armor code; those dependencies belong inside the
contained agent-loop side of the system so F-003 remains true.

## Proposed decision

Split armor ingestion into three tasks:

1. Define the repo-owned web-ingestion and tool-call boundary.
2. Add an external armor guard adapter behind that boundary.
3. Wire executor research and tool-call traffic through the guarded boundary.

The boundary should live in a small internal package owned by agent-builder,
tentatively `internal/ingestion`. It should define typed candidates and
decisions rather than passing raw strings:

- `ContentCandidate`: attacker-reachable content plus source URI, media type,
  retrieval metadata, and a stable correlation ID.
- `ToolCallCandidate`: requested tool name, typed arguments, target URI or
  resource when applicable, executor/task provenance, and a stable correlation
  ID.
- `Guard`: the policy/LLM-guard seam that decides `allow`, `block`, or
  `quarantine`.
- `Broker`: the component consumed by the inside-the-box loop or executor
  harness. It accepts candidates, invokes the configured guard before release,
  and returns only allowed content or tool calls to the executor path.

The guard contract fails closed. A guard error, timeout, unavailable armor
binary/service, malformed guard output, or explicit flagged decision blocks the
candidate. The broker records enough decision metadata for run-record/audit
integration later, but task 024 should not build the full audit-trail block.

The armor integration is an adapter to an existing external tool/service. No
task in this split edits armor source. The adapter owns process/service
invocation details and translates armor results into the boundary's stable
decision model.

The Claude Code CLI executor cannot satisfy this contract while its direct web
research and tool calls bypass agent-builder. Wiring must either constrain the
CLI so research/tool use goes through the broker, or introduce an executor
harness that exposes interceptable web-ingestion and tool-call events. Until
that wiring exists, task 026 cannot claim armor is on the live executor path.

## Consequences

- The armor work becomes implementable and testable in isolation instead of
  relying on an implied interception point.
- The supervisor stays free of web, LLM, executor-tooling, and armor imports.
- Benign, flagged, unavailable-guard, and timeout cases can be covered with a
  deterministic fake guard before a real armor runtime is available.
- The final armor task must prove the live executor path uses the broker. Unit
  tests that manually call the guard adapter are not sufficient evidence.
- Direct executor web/tool use that bypasses the broker is a blocker, not an
  acceptable partial implementation.

## Alternatives considered

- Call armor from the supervisor before dispatch. Rejected because the supervisor
  does not see in-box web content or tool-call requests and must not gain web or
  LLM dependencies.
- Wrap only the Claude CLI prompt with safety instructions. Rejected because
  instructions are not a blocking control and cannot quarantine attacker
  content before it reaches context.
- Add armor as a verification-gate step after executor completion. Rejected
  because the threat is prompt injection during the attempt, before verification
  runs.
