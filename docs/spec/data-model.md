# Data Model

**Project:** agent-builder
**Last updated:** 2026-06-04

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

### State: <StateName> (e.g. `SessionRegistry`, `ScannerEvaluator::candidates`)

- **Shape:** the type signature, including any sync wrappers (`Arc<RwLock<…>>`, `Mutex<…>`)
- **Owner:** which component constructs and owns it; how it's shared
- **Lifetime:** when it comes into being, when it's torn down, what survives a panic
- **Concurrency rules:** lock ordering, single-writer guarantees, what's safe to call from where
- **Bounds:** is the size bounded? if not, by what?

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

-
