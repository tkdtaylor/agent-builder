# Data Model

**Project:** agent-builder
**Last updated:** 2026-06-05

What data exists, how it's structured, where it lives, and what relationships hold between entities. Covers persistent storage, in-memory state, and data-on-the-wire formats.

Not in this file:
- Operations on the data (that's in [behaviors.md](behaviors.md))
- How the data is accessed (that's in [interfaces.md](interfaces.md))
- Tunable parameters (that's in [configuration.md](configuration.md))

---

## Persistent state

> Data that survives process restart. For each store, document the schema, ownership, and access pattern.

### Store: <name> (e.g. PostgreSQL `app_db`, SQLite `data.db`, S3 `bucket-name`)

**Purpose:** what this store holds and why it exists separately from any others
**Owner:** which component is the single writer (or "shared, see access rules below")
**Backup / retention:** how long data lives, how it's recovered

#### Entity: <EntityName>

```
field_name      type          notes
─────────────────────────────────────
id              uuid          primary key
created_at      timestamp     UTC, set by DB default
…
```

- **Identity:** what makes a row unique beyond the primary key (natural key, unique constraint)
- **Lifecycle:** when rows are created, when they're updated, when they're deleted (or "never")
- **Relationships:** foreign keys, parent/child, many-to-many junctions
- **Indexes:** non-obvious indexes and what they support

> Add one section per entity. For schemas that are large or change frequently, prefer pasting the canonical DDL/migration source rather than retyping field lists by hand.

---

## In-memory state

> Data that exists only during process lifetime. For long-running services this is often as important as persistent state — race conditions and lock orders live here.

### State: Verification Gate

- **Shape:** `*gate.Gate` owns an ordered slice of registered `gate.Step` implementations. Each registered step stores the stable step name captured at construction plus the Step implementation.
- **Owner:** callers construct the gate through `gate.New(steps ...Step)` and pass it to the component that needs verification.
- **Lifetime:** process-local; no gate state is persisted. Each `Verify(repoPath)` call returns a fresh `Verdict`.
- **Concurrency rules:** no internal mutation occurs during `Verify` except inside Step implementations; callers choose whether individual Steps are safe to share across goroutines.
- **Bounds:** bounded by the configured step list.

#### Value: `gate.Verdict`

```
field       type                notes
────────────────────────────────────────────────────────────
OK          bool                true only when every executed step passed
Results     []gate.StepResult   ordered outcomes for executed steps
```

- **Identity:** a Verdict is scoped to one `Verify(repoPath)` call.
- **Lifecycle:** created by `Gate.Verify`; consumed by future CLI, agent loop, log, and escalation renderers.
- **Relationships:** `Results` preserves execution order and contains no entries for steps not run after a prior blocking failure.

#### Value: `gate.StepResult`

```
field       type            notes
────────────────────────────────────────────────────────────
Name        string          registered Step name
OK          bool            step pass/fail outcome
Output      string          captured human-readable stdout/stderr or message
Duration    time.Duration   elapsed time measured by the gate
```

- **Identity:** the `Name` is the registered Step name captured by `gate.New`; a Step cannot override it from `Run`.
- **Lifecycle:** produced by one Step execution and appended to the Verdict before the gate continues or short-circuits.
- **Relationships:** belongs to exactly one Verdict.
- **Native Go command output:** `Output` stores combined stdout/stderr for native Go subprocess failures. For `gofmt -l .`, non-empty output stores the listed unformatted files and makes the Step fail even when the subprocess exits zero. Missing native tools store a human-readable lookup failure naming the absent executable.
- **Lint command output:** `Output` stores combined stdout/stderr for `golangci-lint run` failures, including lint findings and configuration/tool errors. Missing `golangci-lint` stores a human-readable lookup failure naming the absent executable.
- **Dependency scan output:** `Output` stores combined stdout/stderr for `gods` failures, including high-or-above CVE findings and scanner/tool errors. Missing `gods` stores a human-readable lookup failure naming the absent executable.

---

## Wire / interchange formats

> Data formats used to exchange information across process boundaries: JSON over HTTP, NDJSON log lines, protobuf messages, CSV exports, etc. Each entry is a stable contract — version it like you would an API.

### Format: <name> (e.g. evaluation NDJSON, scanner result JSON)

- **Producer:** what writes this
- **Consumer:** what reads it (including humans / external tools)
- **Schema:** field-by-field, with types and required/optional markers
- **Versioning:** how schema changes are signaled (top-level `version` field, separate filename, etc.)
- **Example:** a real, valid record

```
<paste a representative example>
```

---

## Derived data

> Data that's computed from other data and treated as authoritative for some purpose (caches, materialized views, indexes). Note the source and the recompute rule so consumers know what guarantees they have.

| Derived | Source | Recompute trigger | Staleness tolerance |
|---------|--------|-------------------|---------------------|
| | | | |

---

## Data invariants

> Properties that must hold across the data model. Examples:
>
> - `account.balance == sum(account.transactions.amount)` for all accounts.
> - No two `Order` rows share an `external_id` for the same `account_id`.
> - In-memory `SessionRegistry` only contains entries whose status is one of {Active, Idle, Failed, Closed}.
>
> If an invariant is enforced by code (DB constraint, runtime assertion, type system), say so.

- A Verdict with `OK == true` contains only passing StepResults.
- A Verdict with `OK == false` ends at the first failing StepResult; later configured steps do not run and do not appear in Results.
