# Architecture — C4 Element Catalog

**Project:** agent-builder
**Last updated:** 2026-06-04

The structured catalog of architectural elements that the diagrams in [`../architecture/diagrams.md`](../architecture/diagrams.md) render. Tables here are the **machine-readable spec** for the system's structure — they survive a Mermaid rewrite and are what a drift audit checks the code against.

## How this file relates to the diagrams

| File | Form | Use when |
|------|------|----------|
| [`../architecture/diagrams.md`](../architecture/diagrams.md) | Visual (Mermaid C4 + sequence) | You want to *see* the structure |
| `architecture.md` (this file) | Tabular (rows + columns) | You want to *check, query, or regenerate* the structure |

When the structure changes, both files update in the same commit. The tables here are the source of truth for *what exists*; the diagrams are the source of truth for *how it's drawn*. If you can't reconcile a diagram to a row in this file, one of them is wrong — fix it before the change ships.

---

## 1. Persons (actors)

> Who uses the system. Includes humans (end users, operators, admins) and external automated systems acting as clients. One row per distinct role.

| Name | Description | Goals |
|------|-------------|-------|
| | | |

---

## 2. Systems

> The system itself, plus every external system it integrates with. The "system in scope" gets one row; each integration gets its own.

| Name | Type | Description | Owner |
|------|------|-------------|-------|
| agent-builder | In-scope | | This team |

---

## 3. Containers

> Independently deployable / runnable units inside the system: services, processes, databases, queues, scheduled jobs. Each container has a technology choice and a single responsibility.

| Name | Technology | Responsibility | Source path | Depends on |
|------|------------|----------------|-------------|------------|
| | | | | |

**Invariants for this table**
- Every container listed has a corresponding directory or deployable artifact under `src/` (or equivalent). The drift-audit mode of the `architect` agent checks this against the actual repo layout.
- Every `Depends on` entry must resolve to another row in this table (Container) or a row in Section 2 (Systems).

---

## 4. Components

> Modules / packages inside containers that are worth naming at the architecture level — typically the ones with stable interfaces between them. Not every file in the codebase belongs here; only the load-bearing ones.

| Container | Component | Source path | Responsibility | Depends on |
|-----------|-----------|-------------|----------------|------------|
| | | | | |

---

## 5. Cross-cutting decisions

> Architectural choices that span multiple containers or components and don't naturally fit in any single row above — auth approach, observability strategy, error-handling convention, retry policy, transaction boundaries. Each entry should link to an ADR.

-

---

## Maintenance

- **Update in the same commit as `../architecture/diagrams.md`** when the structure changes. The catalog and the diagrams are two views of the same model — they drift together or not at all.
- **Supersede in place. Never append.** When a container is renamed or a dependency edge moves, rewrite the row. The ADR carries the history of *why* something changed; this file carries *what* exists now.
- **Don't list every file.** Components in Section 4 are the load-bearing modules with stable interfaces. If listing a component does not change how someone reasons about the system, leave it out.
- The drift-audit mode of the `architect` agent uses this catalog as its primary check against the import graph and the deployable artifact list. Run it periodically — drift accumulates silently.
