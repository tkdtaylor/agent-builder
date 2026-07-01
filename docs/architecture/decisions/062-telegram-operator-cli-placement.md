# ADR 062 — Telegram operator CLI placement: an in-repo, liftable `examples/agent-cli`

**Status:** proposed
**Date:** 2026-07-01

**Motivated by:** tasks 148 (`keygen`) / 149 (`send`) / 150 (`reply-open`) — the operator's
laptop-side CLI that generates keys, seals+signs commands into `envelope.Envelope`s the
live `internal/channel/telegram/adapter.go` will accept, and opens the sealed replies the
`ReplyAdapter` emits. Before those tasks implement anything, this ADR fixes *where* the
client lives in the repo layout and *what dependency edges* it may have, because that
placement is load-bearing for both the client's test strategy (a real round-trip through
`internal/envelope`) and the trust-boundary separation between operator-side and
orchestrator-side code.

## Context

The Telegram inbound channel (ADR 045 / ADR 054) only accepts commands that are
Ed25519-signed and X25519+AEAD-sealed `envelope.Envelope`s. The stock Telegram app cannot
construct one, so an operator needs a first-party companion CLI that holds the operator's
**private** keys, seals+signs a command, and POSTs it through the bot; and, in the reverse
direction, opens the ciphertext replies the orchestrator sends back. That CLI is a
different animal from the orchestrator product (`cmd/agent-builder`): it runs on the
operator's laptop, holds operator private keys, and makes outbound Bot API calls — a
distinct trust boundary and deployment target.

The planner's original slice put the CLI at `cmd/agent-cli` with logic in
`internal/agentcli`. Two things about that placement need a deliberate decision rather
than a default:

1. **Where does it live relative to the orchestrator?** Same module (so it can import
   `internal/envelope`), a sibling under `cmd/`, or a separate repo entirely?
2. **What may it depend on, and what may depend on it?** The client must not become
   entangled with orchestrator internals, or it can never be lifted out later.

The load-bearing constraint is Go's `internal/` visibility rule: only code in the same
module can import `internal/envelope`. Tasks 149/150 rest on a round-trip test that seals
and opens through the *real* `internal/envelope.VerifyAndOpen` — the exact code
`internal/channel/telegram/adapter.go` runs — proving the adapter would genuinely accept
the client's envelopes. A separate repo forfeits that: it could only assert against golden
byte-vectors, which drift silently the moment the wire format changes on one side.

A wire-encoding detail is also in scope. `Envelope.Payload` and `Envelope.Nonce` are
**hex**-encoded on the wire — `internal/channel/telegram/reply.go:95,97` hex-encodes both,
and `internal/envelope.VerifyAndOpen` calls `hex.DecodeString(env.Payload)` — even though
the `Envelope` struct doc comment at `internal/envelope/envelope.go:59` wrongly says
base64. The client must match the real code path, so the encoding needs to be pinned.

## Options considered

### Option A — Separate repo (`agent-cli` as its own module)

The client is its own repository/module, wire-compatible with agent-builder by contract
only.

- **Pros**
  - Cleanest trust separation: operator-side code physically cannot reach orchestrator
    internals; no `internal/` visibility to abuse.
  - Independent release cadence and its own CI, issue tracker, and dependency set.
  - Signals unambiguously that the client is not the product.
- **Cons**
  - **Cannot import `internal/envelope`** — the whole point of Go's `internal/` rule. The
    load-bearing round-trip test (149/150) degrades to asserting against **golden
    byte-vectors** that drift silently when either side changes the wire format.
  - Duplicates the envelope construction (seal/sign/open) by hand, or vendors a copy —
    two implementations of a security primitive that can diverge.
  - Cross-repo change coordination for every wire-format tweak; a two-repo dance for what
    is one logical change.

  *Sketch:* a new `github.com/tkdtaylor/agent-cli` module reimplements (or vendors) the
  `Envelope` shape and seal/sign/open, and its tests compare produced bytes against
  checked-in `testdata/*.golden` vectors captured from agent-builder. Any change to
  `internal/envelope`'s canonicalization requires regenerating and re-committing vectors
  in the other repo; nothing fails fast if someone forgets.

### Option B — In `cmd/agent-cli` + `internal/agentcli` (the planner's original split)

A second `cmd/` entrypoint with its logic in an `internal/agentcli` package.

- **Pros**
  - Can import `internal/envelope` — the real round-trip test works.
  - Familiar layout; mirrors `cmd/agent-builder` + `internal/cli`.
- **Cons**
  - Puts operator-side client logic in `internal/` **alongside the orchestrator's own
    internal packages**, muddying the trust boundary: `internal/` reads as "orchestrator
    product code," and the client is not that.
  - **Defeats liftability.** Spreading the client across `cmd/agent-cli` (entrypoint) and
    `internal/agentcli` (logic) entangles it with the orchestrator's package tree; lifting
    it into its own repo later means untangling two locations, not moving one directory.
  - No structural signal that this is a reference client rather than a shipped surface of
    the product — a reader browsing `internal/` sees it as first-class orchestrator code.

  *Sketch:* `cmd/agent-cli/main.go` calls `agentcli.Main`, with all logic under
  `internal/agentcli/`. It imports `internal/envelope` cleanly, but now `internal/` holds
  two unrelated products' internals, and the operator-key-custody boundary is invisible in
  the layout.

### Option C — In-repo but cleanly separated under a new top-level `examples/agent-cli/` (recommended)

All client code — entrypoint **and** logic — lives together under a new top-level
`examples/` directory, in its own package(s), importing only `internal/envelope` and
stdlib crypto.

- **Pros**
  - Can import `internal/envelope` (same module) — the load-bearing round-trip test seals
    and opens through the *real* adapter code path; compile-time compatibility, no golden
    vectors.
  - **Liftable as a unit:** the whole client is one directory with one dependency edge; to
    extract it later you move `examples/agent-cli/` and re-point that single import.
  - `examples/` signals "reference client that consumes published contracts, not the
    product" — distinct from `cmd/agent-builder` (the orchestrator) and from `internal/`
    (orchestrator internals).
  - Keeps the operator-side trust boundary visible in the layout: operator-key-holding
    code sits under `examples/`, physically separate from the orchestrator runtime.
- **Cons**
  - A new top-level directory convention (`examples/`) that the structure docs must
    introduce and future contributors must respect.
  - Weaker isolation than a separate repo — `examples/agent-cli` *could* import other
    `internal/**` packages unless a boundary rule forbids it (mitigated by the one-way
    invariant below, optionally enforced by a future fitness check).
  - Ships example code in the product repo (small surface: three subcommands, no
    orchestrator runtime coupling).

  *Sketch:* `examples/agent-cli/main.go` plus the client's package(s) under the same
  directory; `go run ./examples/agent-cli keygen --keyfile op.json`. It imports
  `internal/envelope` and stdlib `crypto/*` only. The orchestrator (`cmd/agent-builder`,
  `internal/**`) never imports back.

## Recommendation

**Option C.** The deciding factor is that it uniquely satisfies both hard constraints at
once: it keeps the **compile-time wire-compatibility** that only same-module code can have
(killing Option A's silent-golden-vector drift), while preserving **liftability** as a
single directory with a single dependency edge (which Option B forfeits by smearing the
client across `cmd/` and `internal/`). Option A trades away the one test that actually
proves the adapter accepts the client's envelopes; Option B keeps that test but buries an
operator-side, private-key-holding tool inside the orchestrator's own internals, erasing
the trust boundary and the extraction path. Option C is the only one that gets the real
round-trip *and* a clean, visible, liftable boundary.

## Decision

Adopt **Option C**. The Telegram operator CLI lives in-repo under a new top-level
`examples/agent-cli/` directory — **not** under `cmd/` and **not** with logic in
`internal/`. All client code (entrypoint and logic) lives together there as its own
package(s), importing `internal/envelope` and stdlib crypto exclusively.

Concretely:

- **One-way boundary (invariant).** `examples/agent-cli` → `internal/envelope` is the
  **only** dependency edge for the client. The orchestrator (`cmd/agent-builder`,
  `internal/**`) must **never** import `examples/agent-cli`. This is recommended for
  enforcement by a future `make fitness-agentcli-boundary` check (assert `go list -deps
  ./cmd/... ./internal/...` never contains `examples/agent-cli`, and that
  `examples/agent-cli` imports no `internal/**` other than `internal/envelope`) — proposed
  here, **not built by these tasks**.

- **Operator key custody.** Operator private keys (Ed25519 signing + X25519 sealing) live
  only in a local `0600` keyfile on the operator's laptop, never on the orchestrator host.
  The orchestrator's `AGENT_BUILDER_TELEGRAM_*` env block carries only **public** keys plus
  the orchestrator's **own** private keys — never operator privates.

- **Wire-encoding pin.** `Envelope.Payload` and `Envelope.Nonce` are **hex**-encoded on
  the wire (what `internal/channel/telegram/reply.go:95,97` does and what
  `internal/envelope.VerifyAndOpen`'s `hex.DecodeString` requires). The client MUST
  hex-encode to match the real code path. The stale `Envelope` doc comment at
  `internal/envelope/envelope.go:59` ("base64-encoded") is wrong and must be corrected to
  hex; that one-line correction belongs in **task 148's implementation commit** (this ADR
  does not edit `envelope.go`).

- **Extraction path (kept open).** To move `examples/agent-cli` to a standalone repo later,
  one of two things must first happen: `internal/envelope` is promoted to an importable
  (non-`internal`) package, **or** the client switches to committed golden byte-vectors for
  its wire-compatibility tests. Recording this keeps the door open; the in-repo placement
  is chosen precisely so that the extraction cost is a known, bounded, single-edge change
  rather than an entanglement to unwind.

## Consequences

- **What becomes easier.** Tasks 149/150 get their load-bearing test: a produced envelope
  round-trips through the exact `internal/envelope.VerifyAndOpen` the live adapter runs, so
  wire-format drift between client and adapter fails a unit test at compile+run time rather
  than silently in production. The client is one liftable directory. The operator-side
  trust boundary is visible in the repo layout.
- **What becomes harder.** A new top-level `examples/` convention must be documented and
  respected; the one-way boundary is a discipline (until a fitness check enforces it) that
  a careless import could violate. The client ships in the product repo, so a reader must
  understand `examples/` means "reference client, not product surface."
- **New structural invariant.** `examples/agent-cli` → `internal/envelope` one-way edge;
  orchestrator never imports the client. Enforcement fitness check is recommended, not
  built here.
- **Correction rider.** The base64→hex doc-comment fix in
  `internal/envelope/envelope.go:59` is folded into task 148's implementation commit, not
  taken as a standalone change.
- **Reversibility.** The decision is reversible at a bounded cost recorded above (promote
  `envelope` or adopt golden vectors, then move one directory). It is not a one-way door;
  the in-repo choice is what *lowers* the future extraction cost, not what locks it in.
- **Load-bearing invariants untouched.** The verification gate, no-self-modification,
  one-task-one-branch, containment, and the executor seam are all unaffected; `internal/
  envelope` remains a stdlib+`x/crypto` leaf (F-007) with no new inbound edge from the
  supervisor graph.
