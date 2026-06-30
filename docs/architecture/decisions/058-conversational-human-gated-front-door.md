# ADR 058 — Conversational human-gated orchestrate front door

**Status:** Proposed
**Date:** 2026-06-30
**Supersedes / relates to:** ADR 046 (approval gate), ADR 054 (async message-driven control plane), ADR 055 (plan-derived authorization).

## Context

The Tier-1 orchestrator decomposes high-level goals into planned sequences of actions executed by containerized workers. In its initial iteration, a goal entered the planning phase immediately upon receipt. However, goals received in a production environment are frequently ambiguous, underspecified, or contain implicit assumptions that may result in plans that violate safety constraints or perform incorrect work.

Furthermore, human confirmation should be required before entering the planning and execution phases. We need a structured, secure, and conversational gate that clarification can be completed over before planning begins.

## Decision

To support conversational, human-gated execution, we adopt the following five sub-decisions:

### 1. Goal Lifecycle Phases (`StateClarifying` and `StatePlanning`)
The goal lifecycle is updated to introduce a clarifying phase. The `StateClarifying` phase precedes the `StatePlanning` phase. The entrypoint `Handle` is split conceptually into two operations:
- **Intake (`BeginGoal`):** Registers the goal, transitions it to `StateClarifying`, and generates clarifying questions if the goal is ambiguous.
- **Plan-onward (`ConfirmAndPlan`):** Transitions the goal from `StateClarifying` to `StatePlanning` once confirmation is received.

### 2. Conversational Message Type (`MsgConfirm`)
`MsgConfirm` is introduced as a first-class, channel-abstract message kind (with numeric value `4`) rather than relying on a magic string. This ensures that the control loop dispatches confirmation in a structured manner, symmetric to `MsgCancel` and `MsgInfo`.
The env/stdin grammar will map `confirm <goalID>` to this message kind.

### 3. Ambiguity Resolution Seam (`Clarifier`)
A narrow seam `Clarifier` is defined to determine whether a goal needs clarification before planning.
- **`HeuristicClarifier` (v1 default):** Performs rule-based and regex checks on the goal spec text (e.g. checking for missing target repos or vague commands) to prompt for clarification.
- **`LLMClarifier` (opt-in):** Delegates clarification analysis to a single-shot Completer LLM.

### 4. Operator Approval Requirement
An operator-level configuration `AGENT_BUILDER_REQUIRE_APPROVAL` (boolean, default `true`) is introduced. This configuration is orthogonal to policy-engine risk limits and determines whether human approval must always be sought before executing a plan.

### 5. Unified Operator Communications via `Reporter`
All human touchpoints (clarifying questions, plan approval requests, and needs-human escalations) flow over the `Reporter` seam (e.g. Telegram or stdin/stdout). Escalation notifications move off the file-backed `tasksource.StatusWriter` to prevent writing to task files during interactive loops.

## Consequences

- The intake phase becomes conversational: ambiguous goals are caught and clarified with the operator before plans are generated or resources allocated.
- Confirmation is unified under the `MsgConfirm` control-plane type.
- Verification and auditability are improved, since the entire intake conversation flows through the `Reporter` and can be recorded/signed.
