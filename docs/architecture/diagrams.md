# Architecture Diagrams

**Project:** agent-builder
**Last updated:** 2026-06-05

C4-structured Mermaid diagrams covering the system at three progressively detailed levels (Context → Container → Component), plus the runtime sequence flows that show how those pieces collaborate. See [overview.md](overview.md) for prose context, [decisions/](decisions/) for the ADRs referenced here, and [`../spec/architecture.md`](../spec/architecture.md) for the structured element catalog these diagrams render.

These diagrams are part of the **authoritative spec** for this project. They are not just documentation about the code — they are a source-of-truth statement of how components are arranged and how data flows. Code changes that contradict a diagram either invalidate the change or invalidate the diagram; one must be updated to match the other in the same commit.

GitHub and most IDE markdown previewers render Mermaid natively — no build step required. Mermaid's `C4Context`, `C4Container`, `C4Component`, `C4Deployment`, and `C4Dynamic` blocks render as proper C4 diagrams.

> **Scaling rule.** Trivial systems (single container, no integrations) can collapse Container and Component into one section, or skip Container entirely. Large systems may split Component into one diagram per container (3a, 3b, …). The C4 levels are the *grammar* — use as many as the system actually needs. Per-flow runtime sequences (Section 4+) always belong here regardless of size.

---

## 1. System Context — who uses it and what it touches

> Top-level view: the system as one box, the people who use it, and the external systems it depends on. No internals. Update when a new actor or external dependency appears.

```mermaid
C4Context
    title System Context for agent-builder

    Person(user, "User", "The person who interacts with the system")
    System(system, "agent-builder", "What this system does in one line")
    System_Ext(codeScanner, "code-scanner", "Malware/backdoor scanner used as a blocking gate step")

    Rel(user, system, "Uses")
    Rel(system, codeScanner, "Runs", "process PATH")
```

---

## 2. Containers — deployable units inside the system

> One level down: each independently deployable / runnable unit (process, service, database, queue, scheduled job). Show the technology choice on each container and the protocol on each edge.

```mermaid
C4Container
    title Container view of agent-builder

    Person(operator, "Operator", "Starts and observes autonomous builder runs")

    System_Boundary(boundary, "agent-builder") {
        Container(cli, "agent-builder CLI", "Go", "Entrypoint process for the autonomous builder scaffold")
        Container(execBox, "execution-box profile", "Rootless Podman / OCI image", "Product containment artifact for one target repo worktree")
    }

    Rel(operator, cli, "Runs")
    Rel(operator, execBox, "Runs probe")
```

---

## 3. Components — modules inside the main container

> One level deeper into whichever container is most worth zooming into — usually the one a new contributor will touch first. Show the major modules / packages and their dependencies. Add additional Component diagrams (3a, 3b, …) for other containers when they are non-trivial.

```mermaid
C4Component
    title Component view of agent-builder CLI

    System_Ext(codeScanner, "code-scanner", "Malware/backdoor scanner CLI")

    Container_Boundary(boundary, "agent-builder CLI") {
        Component(main, "Main", "cmd/agent-builder", "Entrypoint and process exit handling")
        Component(supervisor, "Supervisor", "internal/supervisor", "Trusted outside-the-box dispatcher, lifecycle logger, and stable seams")
        Component(agentloop, "Agent Loop", "internal/loop", "Inside-the-box pick-attempt-verify cycle plus bounded retry policy")
        Component(sandbox, "exec-sandbox Run Adapter", "internal/sandbox", "Typed contained-command seam and test fake")
        Component(tasksource, "Task Source", "internal/tasksource", "Read-only roadmap/task parser and next-task selector")
        Component(statuswriter, "Task Status Writer", "internal/tasksource", "Constrained task status mutation")
        Component(gate, "Verification Gate", "internal/gate", "Ordered blocking checks with structured Verdicts")
    }

    Rel(main, supervisor, "Starts")
    Rel(supervisor, sandbox, "Stores Runner / box seam")
    Rel(supervisor, gate, "Consumes Verdict model / Gate seam")
    Rel(agentloop, supervisor, "Consumes Task / Executor / Gate seams")
    Rel(agentloop, tasksource, "Picks next task")
    Rel(agentloop, statuswriter, "Marks needs-human after exhausted retries")
    Rel(agentloop, gate, "Verifies target worktree")
    Rel(tasksource, supervisor, "Uses Task model")
    Rel(gate, codeScanner, "Runs in target worktree")
```

**Key contracts**
- ADR 002 fixes the gate shape: ordered Steps, structured Verdict, first-failure short-circuit, and no skip path.
- ADR 012 fixes the agent loop shape: pick -> attempt -> verify -> advance states, done/idle/fail outcomes, and policy-free fail reporting.
- ADR 013 fixes the retry escalation policy: non-negative `MaxAttempts`, mandatory stop, status-writer `needs-human` marking, and substitutable escalation hook.
- ADR 020 fixes the exec-sandbox run adapter seam: command/worktree/typed limits in, result/exit/error out.
- Task 017 fixes the supervisor dispatch lifecycle: create one box, run one in-box loop, and tear the box down exactly once.
- The supervisor remains trusted and dumb; the gate contains verification orchestration only, not executor/LLM/web logic.
- The task source is read-only and only selects tasks; the task status writer is the separate constrained mutation component.
- ADR 014 defines the execution-box profile artifact; supervisor wiring to launch it is deferred to the dispatch task.

---

## 4. Primary runtime flow

> The most important sequence through the system — the one a new contributor needs to understand first. Startup → first user action → response is a good default.

```mermaid
sequenceDiagram
    autonumber
    participant Supervisor
    participant Box as Containment Box
    participant AgentLoop as Agent Loop
    participant TaskSource as Task Source
    participant Executor
    participant EscalationHook as Escalation Hook
    participant Gate as Verification Gate
    participant StatusWriter as Task Status Writer
    participant Roadmap as docs/plans/roadmap.md
    participant Tasks as docs/tasks/*.md

    Supervisor->>Box: Create(Task)
    Box-->>Supervisor: BoxHandle
    Supervisor-->>Supervisor: log box.created
    Supervisor-->>Supervisor: log loop.started
    Supervisor->>AgentLoop: RunInside(BoxHandle, Task)
    AgentLoop->>TaskSource: Next()
    TaskSource->>Roadmap: read
    TaskSource->>Tasks: read task files
    TaskSource-->>AgentLoop: first ready Task or empty result
    loop up to MaxAttempts
        AgentLoop->>Executor: Run(Task)
        Executor-->>AgentLoop: Result{Branch, OK}
        opt Executor OK
            AgentLoop->>Gate: Verify(worktreePath)
            Gate-->>AgentLoop: Verdict
        end
        alt attempt failed and retries remain
            AgentLoop->>EscalationHook: select next Executor
            EscalationHook-->>AgentLoop: Executor
        else Gate passed
            AgentLoop-->>Supervisor: RetryOutcome{done branch}
        end
    end
    alt failures exhausted
        AgentLoop->>StatusWriter: WriteStatus(Task.ID, needs-human)
        StatusWriter->>Tasks: rewrite status line
        AgentLoop-->>Supervisor: RetryOutcome{escalated}
    else no ready task
        AgentLoop-->>Supervisor: RetryOutcome{idle}
    end
    Supervisor->>Box: Teardown(BoxHandle)
    Supervisor-->>Supervisor: log box.torn_down
```

---

## Adding more diagrams

Add additional numbered sections (5., 6., …) for any of:

- **Per-flow sequence diagrams** — error handling, reconnect, batch processing, auth, etc. One per flow that crosses two or more components and matters to operate the system.
- **State machines** — if a subsystem has explicit states with transitions
- **Deployment topology** — `C4Deployment` if the runtime layout (nodes, hosts, regions) is non-obvious
- **Dynamic collaboration** — `C4Dynamic` for showing how containers collaborate during a specific use case

One concept per diagram. If a diagram tries to show both a component layout and a runtime sequence, split it.

---

## Maintaining these diagrams

- **Trigger to update:** any time a new actor, container, or component appears; a boundary moves; an external dependency is added or removed; an ADR changes a diagrammed flow. Keep [`../spec/architecture.md`](../spec/architecture.md) in sync — the catalog and these diagrams describe the same elements.
- **Edit existing over adding new.** Duplicates rot independently. If a diagram has grown unwieldy, split it by extracting a self-contained subflow into its own numbered section.
- **Note ADRs that don't change diagrams.** When an ADR introduces a refactor that preserves the diagrammed runtime shape, add a one-line note here saying so. This prevents future contributors from re-asking "should this have been drawn?"
- **Update the date at the top** when you change anything substantive.
