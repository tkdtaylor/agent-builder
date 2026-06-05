# Behaviors

**Project:** agent-builder
**Last updated:** 2026-06-04

What the system does, observably. Each behavior describes a triggering condition, the system's response, and any externally-visible side effects. This is the "you can verify this from outside the process" view.

Not in this file:
- *How* it does it (that's in source code; the contract is here, the implementation is there)
- *Why* it does it (that's in ADRs)
- *What data it operates on* (that's in [data-model.md](data-model.md))
- *What the entry points are* (that's in [interfaces.md](interfaces.md))

---

## Format

Each behavior is a numbered subsection with three parts:

> **B-NNN: Short imperative title**
>
> - **Trigger:** what causes this behavior to fire
> - **Response:** what the system does
> - **Side effects:** observable effects beyond the immediate response (writes, emitted events, log entries, external calls)
> - **Failure modes:** how it can fail and what the system does when it does
> - *(optional)* **References:** ADRs that drove the behavior, related test specs

Behaviors are numbered `B-001`, `B-002`, … sequentially. Numbers are stable references — never reuse a number, even if a behavior is removed (mark it `B-NNN: REMOVED — see ADR-XXX` and leave the number).

---

## Core behaviors

> Replace this section with the system's primary behaviors. Order them roughly by how central they are to the system's purpose, not chronologically.

### B-001: <first behavior title>

- **Trigger:**
- **Response:**
- **Side effects:**
- **Failure modes:**

### B-002: <second behavior title>

- **Trigger:**
- **Response:**
- **Side effects:**
- **Failure modes:**

---

## Edge cases and error behaviors

> Behaviors specifically for error conditions, recovery, and edge cases. Keep them here rather than scattered through the core list — most readers want core first, edge cases on demand.

### B-NNN: <edge case title>

- **Trigger:**
- **Response:**
- **Side effects:**
- **Failure modes:**

---

## Behavioral invariants

> Cross-cutting properties that hold across many or all behaviors. Examples:
>
> - All write operations are idempotent on retry.
> - No behavior can leave the system in an inconsistent state on partial failure (transactions / rollback / compensating action).
> - User-triggered destructive actions always require confirmation.
>
> Invariants here are stronger than ones in `SPEC.md` "Top-level invariants" — those are about system architecture; these are about observable behavior.

-
