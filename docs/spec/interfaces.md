# Interfaces

**Project:** agent-builder
**Last updated:** 2026-06-04

The system's contact surface — everything that calls into the system, everything the system calls out to, and the public boundaries within the system. Each interface is a stable contract: changes here are breaking changes.

Not in this file:
- What the interfaces *do* (that's in [behaviors.md](behaviors.md))
- What data flows through them (that's in [data-model.md](data-model.md))
- How they're configured (that's in [configuration.md](configuration.md))

---

## Inbound interfaces

> What the outside world uses to call into this system.

### CLI

> The command-line surface. List every subcommand, flag, and positional argument. For each, give type, default, and effect.

```
agent-builder <subcommand> [flags] [args]

Subcommands:
  <subcommand>    <one-line description>

Global flags:
  --flag <type>   <effect> (default: <value>)
```

| Subcommand / flag | Type | Default | Effect |
|-------------------|------|---------|--------|
| | | | |

**Exit codes:**
- `0` — success
- `1` — generic error
- `2` — usage error
- *(add more as defined)*

### HTTP / RPC API

> If the project exposes an API, document each endpoint. For larger APIs, link to a generated OpenAPI/protobuf file rather than retyping it here.

| Method | Path | Purpose | Request shape | Response shape | Errors |
|--------|------|---------|---------------|----------------|--------|
| | | | | | |

### Wire protocol

> If the project speaks a binary or text protocol (TWS, FIX, custom), document the message catalogue here or link to a separate `protocol.md` if it's large.

---

## Outbound interfaces

> What this system calls out to. Each external dependency is a coupling point — list it explicitly so failure modes and version pinning are visible.

| Dependency | What we call | Library / version | Failure mode |
|------------|-------------|-------------------|--------------|
| | | | |

---

## Internal public surface

> Interfaces *within* the project that are stable contracts between modules. Examples: a `Strategy` trait that strategy crates implement, a `Repository` trait that handlers consume, an event bus shape.
>
> If a module's public API isn't listed here, it's an implementation detail — callers should not depend on it. Promotion to this list is a deliberate decision (often via ADR).

### Trait / interface: <Name>

```rust|python|ts|go
<paste the canonical signature>
```

- **Implementors:** who implements this
- **Consumers:** who calls this
- **Stability:** how breaking changes are coordinated (semver, ADR, both)
- **Required behavior:** what an implementation must guarantee beyond the type signature (e.g. "must be Send + Sync", "must not panic", "side-effect-free if input X")

---

## Extension points

> Plugin slots, hook points, registration mechanisms — anything designed for external code to extend the system without modification. If there are none, say "None — extension is by source modification" so it's an explicit choice.

-
