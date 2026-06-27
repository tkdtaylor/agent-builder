# ADR 045 — Secure channel: Ed25519 envelope, key distribution, replay prevention, armor channel-mode

**Status:** Accepted (2026-06-27) — design-only. Resolves the open design questions
blocking task 080 (Telegram channel adapter + envelope + armor guard) so its test spec
can be expanded into real assertions. No code, spec, or diagram changes land with this
ADR.
**Date:** 2026-06-27

**Owner decisions (sign-off 2026-06-27):**
- **Confidentiality is KEPT.** The X25519+AEAD layer (§2) is adopted so Telegram carries
  ciphertext it cannot read, honoring ADR 042's intent. The authenticity-only fallback is
  *not* taken.
- **`golang.org/x/crypto` is APPROVED** as a new dependency for the AEAD/X25519 layer
  (the AGENTS.md "Ask first" gate is satisfied by this sign-off).
- **The task restructure (§5) is ACCEPTED:** extract `internal/envelope` as its own task
  ahead of 080; 080 owns the channel + human-side confidentiality; task 083's agent-mesh
  framing is revised. The task-planner executes this split next.
**Motivated by:** task 080's stub test spec, whose "Open questions" section defers three
load-bearing decisions (agent-mesh API shape, key-distribution model, replay window)
plus two wiring decisions (armor channel-mode, sequencing vs task 083). This ADR is the
design-prep that resolves all five before any code is written.
**Amends / reconciles ADR 042.** ADR 042 selected "Telegram + app-layer Ed25519
envelope" and claimed the envelope makes "Telegram carry **ciphertext it cannot
read**." A survey of the agent-mesh block (below) shows that claim cannot be satisfied
by agent-mesh as it exists today, and that **Ed25519 is a signing-only primitive** — it
provides authenticity, not confidentiality. This ADR **partially supersedes ADR 042's
confidentiality claim**: it keeps ADR 042's transport choice and security posture
(Telegram is a dumb untrusted transport; the security lives in our owned layer) but
corrects *how* confidentiality is obtained, and corrects the integration assumption that
agent-mesh is reusable as a library. ADR 042's two-tier architecture, its
swappable-transport position, and its "armor is load-bearing on the channel" mandate are
all preserved unchanged.

## Context

Task 080 builds the human↔orchestrator goal-source channel: a Telegram bot adapter that
applies an app-layer cryptographic envelope, routes the decrypted payload through armor,
and delivers a goal over the `supervisor.GoalSource` seam (relocated there by ADR 044).
Five questions were left open. Resolving them requires knowing what the agent-mesh block
actually exposes, so the block was surveyed directly at `~/Code/Public/agent-mesh`.

### Survey finding: what agent-mesh actually is (evidence)

The block was read at `~/Code/Public/agent-mesh` (commit state of 2026-06-26). Concrete
findings, with file evidence:

1. **It is `package main`, not an importable library.** `mesh.go`, `noncestore.go`,
   `transport.go`, `identity.go`, `main.go` all declare `package main`
   (`~/Code/Public/agent-mesh/mesh.go:3`). The types that matter — `Identity`,
   `Envelope`, `Mesh`, `NonceStore`, and the `SignEnvelope`/`Deliver` methods — are
   **unexported from any importable package**. A Go consumer **cannot** `import
   "github.com/tkdtaylor/agent-mesh"` and call `SignEnvelope`. This rules out the
   "link as a Go module" option for `internal/envelope`.

2. **It provides signing + replay, and *no* encryption.** `Envelope`
   (`mesh.go`) carries `From, To, Nonce, TS, Payload string, Sig string`. `Payload` is
   **plaintext**; `signingBytes()` signs the canonical body but never encrypts it. A
   repo-wide grep for `encrypt|aead|x25519|nacl|chacha|Seal|cipher|secretbox` over the
   block's `*.go` returns **zero matches**. The README §Status confirms: "Signal-protocol
   E2E" is listed under **Deferred (v1)**. agent-mesh today = Ed25519 signature +
   nonce/timestamp replay prevention. It is an **authenticity** layer, not a
   **confidentiality** layer.

3. **Replay prevention is *both* time-window and nonce-set.** `Deliver` (`mesh.go`)
   rejects `stale_timestamp` when `abs(now-env.TS) > replayWindow` (a fixed
   `60 * time.Second`), then rejects `replay_detected` when `nonces.Seen(env.Nonce)`.
   The `NonceStore` seam (`noncestore.go`) keeps each nonce for `2 * replayWindow` and
   evicts older ones — so the time window *bounds* the nonce set, keeping it small. This
   is the canonical "both" construction and is worth mirroring.

4. **The CLI surface is two-process A2A, not a sign/verify filter.** `main.go`
   subcommands are `identity | demo | serve | serve-svid | sendto | tracer`. `sendto`
   signs and POSTs an envelope to a *running* peer `serve` process over A2A HTTP/JSON-RPC
   (`transport.go`). There is **no** `agent-mesh sign <stdin> → envelope` /
   `agent-mesh verify <envelope> → {ok,payload}` one-shot filter analogous to
   `audit-trail emit` or armor's JSON-stdin/stdout contract. So even the
   "wrap the binary like `internal/audit` does" pattern does **not** map cleanly: there
   is no stdin→stdout verb to wrap; there is only a long-lived A2A server pair.

**Net:** the envelope crypto ADR 042 assumed it would reuse from agent-mesh is, today,
(a) not importable, (b) not exposed as a wrappable one-shot CLI, and (c) signing-only
with no confidentiality. The two design directions ADR 042 implied — "link the block" or
"wrap the block binary" — are both blocked by the block's current shape.

## Decision

Five decisions, each grounded in the survey.

### 1. agent-mesh integration shape — **neither link nor wrap; reimplement the thin envelope over stdlib `crypto/ed25519`, treating agent-mesh's wire format as the contract**

Because agent-mesh is `package main` (not importable) and exposes no sign/verify CLI
filter (only a two-process A2A server pair), there is no seam to consume. The envelope
construction itself is small and standard: a JSON canonical body, an `ed25519.Sign` over
it, a hex signature field, a nonce, and a timestamp. `internal/envelope` therefore
**reimplements that construction directly over Go's stdlib `crypto/ed25519`**, adopting
agent-mesh's **`Envelope` JSON shape and `signingBytes()` canonicalization as the
on-the-wire contract** so the two stay interoperable and a future agent-mesh that *does*
ship a library/filter can be swapped in behind the same internal interface.

This is consistent with the project's leaf-adapter discipline, just at a different
boundary: `internal/audit` wraps a binary because audit-trail *has* a `emit` filter;
`internal/policy` IPCs a daemon because policy-engine *is* a daemon; `internal/envelope`
links stdlib crypto because agent-mesh *neither* exposes a library *nor* a filter for the
operation we need. The unifying rule — "consume the block over its published contract,
do not absorb it" — is honored by adopting the block's **wire format** as the contract,
which is the only published contract the block actually offers for this operation (the
`Envelope` JSON + canonical signing bytes).

**Import / leaf-purity implications.** `internal/envelope` imports only stdlib
(`crypto/ed25519`, `crypto/rand`, `encoding/json`, `encoding/hex`, `time`) plus, for
confidentiality, stdlib-adjacent `golang.org/x/crypto` (see §2). It imports **no**
`agent-builder/internal/*` package and **no** `github.com/tkdtaylor/agent-mesh` (it
cannot — `package main`). It must stay a **leaf**: `internal/channel/telegram` and the
orchestrator depend on `internal/envelope`, never the reverse. Crucially, `internal/
envelope` must **not** appear in `go list -deps ./internal/supervisor/...` (F-003) — the
supervisor stays a dumb host-side control core with no crypto/transport in its graph. The
channel adapter sits *above* the supervisor and feeds it goals over the `GoalSource`
seam; the envelope/crypto lives on the channel side of that seam, not inside it. A new
`make fitness-envelope-isolation` check (proposed for the planner, not built here)
should assert `internal/envelope` is a stdlib-plus-x/crypto leaf, mirroring F-005/F-006.

> Note: `golang.org/x/crypto` is a **new dependency** (the project is currently pure
> stdlib for crypto). Per AGENTS.md "Ask first → Adding dependencies not already in the
> tech stack," it required the project owner's call — **approved 2026-06-27.** `x/crypto`
> is the Go team's own module, widely vetted, and the standard home for
> `nacl/box`/`chacha20poly1305`. The alternative (hand-rolling an AEAD) is strictly worse.

### 2. Key-distribution model — **two keypairs per side (Ed25519 for signing, X25519 for confidentiality); a small static trusted-key set on the orchestrator; sign-then-encrypt with an authenticated AEAD**

ADR 042 said "Ed25519 (asymmetric signing)" and "ciphertext Telegram cannot read." Those
two claims need **two different primitives** — Ed25519 cannot encrypt. The concrete
construction:

- **Identities.** Each side (the human's Telegram client, and the orchestrator) holds
  **two keypairs**:
  - an **Ed25519** keypair for **signing** (authenticity — proves the message came from
    that side and was not tampered), and
  - an **X25519** keypair for **key agreement / encryption** (confidentiality — so
    Telegram carries ciphertext).
- **What the orchestrator trusts.** The orchestrator holds a **static trusted-key set**:
  the human client's **Ed25519 public key** (to verify inbound signatures) and the
  human client's **X25519 public key** (the peer half of the encryption agreement). A
  message whose signature does not verify against a key in that set is rejected as
  `unknown_sender`/`bad_signature` *before* armor (mirrors agent-mesh's `Deliver` order).
  The set is small and static for v1 — a single human operator — and is provisioned at
  orchestrator config time (env-referenced key files; see §5 / configuration).
- **What is signed vs encrypted.** **Encrypt-then-the-signature-covers-the-ciphertext is
  the safe ordering for a two-party authenticated channel:** the sender derives a shared
  secret via X25519 (its X25519 private key + the peer's X25519 public key), seals the
  plaintext goal with an **AEAD** (`nacl/box`, i.e. X25519 + XSalsa20-Poly1305, or
  `chacha20poly1305` over an HKDF-derived key — both in `golang.org/x/crypto`), then
  **Ed25519-signs the envelope whose `Payload` is the ciphertext** (the same
  `signingBytes()` canonicalization agent-mesh uses, now over ciphertext rather than
  plaintext). The receiver verifies the signature first (cheap, rejects forgeries before
  doing any decryption), checks freshness + replay, then decrypts. The AEAD tag gives
  integrity of the plaintext; the Ed25519 signature gives sender authenticity of the
  whole envelope.
- **How the human client gets/holds keys.** The human's Telegram client is **not the
  Telegram app itself** (a bot cannot run app-layer crypto inside Telegram's UI). It is a
  **thin first-party companion** the operator runs (a small CLI / script that holds the
  human-side Ed25519+X25519 private keys and the orchestrator's two public keys, encrypts
  + signs an outgoing goal into the envelope JSON, and pastes/sends the resulting opaque
  blob through the Telegram bot chat). Key provisioning for v1 is **manual + out-of-band**:
  generate both keypairs on each side once, exchange the **public** keys over a trusted
  channel (the operator setting up their own machine), store private keys locally with
  `0600` perms, never in the repo, never logged (REQ-080-05). Key **rotation** stays
  out of scope (task 080 already excludes it).

This reconciles ADR 042: the **signing** is Ed25519 exactly as 042 said; the
**confidentiality** ("ciphertext Telegram cannot read") is delivered by the **added
X25519+AEAD layer**, which 042 implicitly required but did not name because it assumed
agent-mesh provided it. It does not. This ADR names the construction.

> **Resolved (owner sign-off 2026-06-27): confidentiality is KEPT.** The X25519+AEAD
> layer is adopted and `golang.org/x/crypto` is approved as a dependency. The
> authenticity-only fallback (pure-stdlib `crypto/ed25519`, retracting ADR 042's
> "ciphertext Telegram cannot read" claim) was the documented alternative and is **not**
> taken. ADR 042's confidentiality intent is therefore *satisfied* by the named
> construction, not retracted.

### 3. Replay-prevention window — **both: a time-freshness window AND a nonce set, with the nonce set bounded by the window; replay state lives in `internal/envelope`, in-memory for v1**

Adopt agent-mesh's exact construction (it is correct and battle-tested in that block):

- **Reject** any envelope whose `abs(now - TS)` exceeds a freshness window `W` (default
  **60s**, matching agent-mesh's `replayWindow`; make it a config knob).
- **Reject** any envelope whose nonce has already been `Seen`.
- **Bound the nonce set** by retaining each nonce for only `2 * W` and evicting older
  ones (agent-mesh's `retentionSecs()`), so the set can never grow unbounded — a nonce
  older than the retention horizon can never pass the freshness check again, so dropping
  it is safe.

Time-only is insufficient (two messages within the same window could replay); nonce-only
is insufficient (an unbounded set is a memory-exhaustion DoS, and there is no horizon to
safely evict). Both together give freshness *and* exactly-once within the window *and*
bounded memory.

**Where the state lives.** The seen-nonce set lives **inside `internal/envelope`** behind
a small `ReplayCache` interface (mirroring agent-mesh's `NonceStore`), with an in-memory
backend as the v1 default. It is **channel-local state**, not orchestrator goal/fleet
state — so it is **outside memory-guard's scope (task 084)**. memory-guard guards the
orchestrator's *long-lived goal/fleet store*; an ephemeral, self-evicting, per-process
replay cache that bounds itself to `2*W` of nonces is not durable state worth a
write-gate/delete-verify. If a *persistent* replay cache is ever needed (to survive
orchestrator restart, like agent-mesh's `--store` file backend), that is a follow-on and
*then* it touches memory-guard's scope — but v1 is in-memory and explicitly out of 084.

### 4. armor channel-mode wiring — **reuse `internal/armor.Guard` verbatim; armor sits on the *decrypted plaintext* after envelope verification and before `GoalSource` delivery; a block decision drops the goal and emits an audit event**

armor already exposes exactly the seam needed: `armor.Guard.DecideContent(ctx,
ingestion.ContentCandidate) (ingestion.Decision, error)` (`internal/armor/guard.go`),
which maps an external armor process's JSON verdict onto `ingestion.Decision{Allow |
Block | Quarantine}` and **fails closed**. No new armor adapter is needed — the channel
reuses the same `armor.Guard` the executor harness uses (`internal/executorharness/
armor.go` is the precedent: a thin constructor that wires `armor.NewGuard` into a
different consumer). The channel gets a parallel thin constructor (e.g.
`channel.NewArmorGuarded` or direct use of `armor.NewGuard`).

**The seam — exact position in the channel path:**

```
Telegram getUpdates → adapter pulls opaque blob
  → envelope.Verify (Ed25519 sig over ciphertext; reject unknown key → audit + drop)
  → envelope freshness + replay check (reject stale/replayed → audit + drop)
  → envelope.Decrypt (X25519+AEAD → plaintext goal)
  → armor.Guard.DecideContent(plaintext goal as ContentCandidate)
        ├─ Allow    → deliver goal over supervisor.GoalSource; advance offset
        └─ Block / Quarantine / fail-closed
                    → DROP the goal (no GoalSource delivery)
                       + emit audit event (rejected: armor block, with reason/findings)
                       + advance offset (the update is consumed, not re-polled)
```

armor runs on the **decrypted plaintext** — guarding the plaintext is the whole point
(prompt injection lives in the human-supplied text, which is meaningless until
decrypted). It runs **after** envelope verification (so armor never wastes work on
forged/replayed messages — cryptographic rejection is cheaper and strictly prior) and
**before** any `GoalSource` delivery (so a blocked payload never reaches orchestrator
goal-intake). A `Block`/`Quarantine`/fail-closed `Decision` causes the adapter to drop
the goal and emit an audit event via the existing `audit.Sink` — never silently. This is
the literal realization of ADR 042's "armor moves from optional to load-bearing on the
human↔orchestrator channel."

### 5. Sequencing vs task 083 — **extract the shared envelope primitive first; 080 owns the channel + the human-side confidentiality layer; 083 owns the orchestrator↔worker transport. Neither task depends on agent-mesh being "adopted" first, because there is nothing importable to adopt.**

The apparent dependency inversion ("080's envelope wraps agent-mesh, which 083 adopts
later") **dissolves once the survey lands**: there is no agent-mesh library or CLI filter
to adopt, so neither 080 nor 083 can "wrap the adopted block." Both tasks need the *same*
small stdlib envelope construction (Ed25519 sign + nonce/timestamp replay over the
agent-mesh wire shape). The clean factoring is:

- **A new task `internal/envelope` (extract first).** A pure-stdlib (+`x/crypto`) leaf
  that produces/verifies the agent-mesh-compatible `Envelope` (sign, verify, replay
  cache) and — if confidentiality is kept (§2) — the X25519+AEAD seal/open. This is the
  shared primitive **both** 080 and 083 consume. It has **no** orchestrator dependency,
  so it is **not** blocked by task 081 (unlike 083 today). Recommend slotting it as the
  first slice, ideally with its own task number ahead of 080's channel work.
- **Task 080 (channel) then owns:** the Telegram bot adapter (`internal/channel/
  telegram`), the **human-side confidentiality** decision (X25519+AEAD over the
  envelope), the trusted-key set / key-distribution wiring for the human operator, the
  armor channel-mode wiring, and the `supervisor.GoalSource` satisfaction. It consumes
  `internal/envelope`; it does not redefine it.
- **Task 083 (orchestrator↔worker transport) then owns:** the orchestrator-side use of
  the *same* `internal/envelope` to sign/verify work-items and results between
  orchestrator and workers, plus whatever transport (A2A HTTP, or in-process) the fleet
  uses. 083 stays blocked by task 081 (it needs the orchestrator + workers to exist), but
  it is **no longer blocked on a separate "adopt agent-mesh as a library" step** — that
  step does not exist. 083's REQ-083-04 ("`internal/agentmesh` is a leaf reached
  IPC-only") should be **revisited**: there is no IPC to a agent-mesh binary for the
  sign/verify operation; the leaf is `internal/envelope` linking stdlib crypto, and the
  fitness check asserts *that* leaf shape. (If 083 also wants agent-mesh's A2A *transport*
  between live worker processes, that is a separate, genuinely-out-of-process concern
  from the envelope crypto, and can wrap `agent-mesh serve`/`sendto` — but the envelope
  signing itself is the shared `internal/envelope` leaf.)

**Recommended ordering:** `internal/envelope` extract → 080 (channel) and 083 (worker
transport) both build on it, in their existing dependency order (080 after Cluster A; 083
after 081). No inversion: the shared primitive precedes both consumers.

## Why this framing and not the alternatives

- **Why not link agent-mesh as a Go module?** It is `package main`
  (`~/Code/Public/agent-mesh/mesh.go:3`); its types are unimportable. Not an option, full
  stop — not a preference.
- **Why not wrap the agent-mesh binary like `internal/audit` wraps audit-trail?** Because
  audit-trail exposes a one-shot `emit` stdin→stdout filter to wrap; agent-mesh exposes
  only `serve`/`sendto` (a long-lived two-process A2A pair) with **no** sign/verify
  filter verb (`main.go`). Wrapping it would mean standing up an agent-mesh `serve`
  process and round-tripping our own control-channel payload through its A2A HTTP just to
  get a signature — a heavyweight, stateful, out-of-process dependency for an operation
  that is ten lines of `crypto/ed25519`. That is absorbing complexity, not composing a
  contract.
- **Why reimplement over the block's *wire format* rather than ignore agent-mesh
  entirely?** Adopting the `Envelope` JSON + `signingBytes()` canonicalization as the
  contract keeps us **wire-interoperable** with agent-mesh, so if the block later ships
  an importable library or a sign/verify filter (its README lists E2E and live
  SPIRE/Vault as v1 deferrals — the block is still evolving), we can swap to it behind
  `internal/envelope`'s interface with zero change to the channel or transport callers.
  We get the block's *contract* without taking on its *current packaging*.
- **Why two keypairs (Ed25519 + X25519) instead of one?** Ed25519 signs; it cannot
  encrypt. There is no single asymmetric primitive that does both well, and reusing an
  Ed25519 key for X25519 (the birationally-equivalent conversion) is a known footgun that
  cross-protocol-attack literature warns against. Two keypairs is the textbook safe
  construction and the cost is trivial (two 32-byte keys per side).
- **Why both replay mechanisms, not one?** Stated in §3: time-only allows in-window
  replay; nonce-only is an unbounded-memory DoS with no safe eviction horizon. agent-mesh
  already proves the combined construction; mirroring it is lower-risk than inventing a
  variant.

## Consequences

- **Design-only.** No change to `internal/`, `cmd/`, `docs/spec/`, or `docs/architecture/
  diagrams.md` lands with this ADR. The orchestrator/channel surface enters the spec only
  when it ships (per ADR 040/041/042 — spec stays present-tense on the coding agent).
- **ADR 042 is partially superseded on one point:** its "Ed25519 envelope → ciphertext
  Telegram cannot read" conflated authenticity and confidentiality. This ADR keeps the
  transport choice and posture but names the real construction (Ed25519 sign **+** X25519/
  AEAD encrypt), and corrects the integration assumption (agent-mesh is not reusable as a
  library or as a sign/verify filter today). ADR 042's two-tier architecture and
  swappable-transport position are untouched.
- **A new dependency is adopted:** `golang.org/x/crypto` for the AEAD/X25519 layer
  (confidentiality kept — §2; owner-approved 2026-06-27). The tech-stack doc should record
  it when `internal/envelope` lands. The authenticity-only fallback (pure-stdlib, claim
  retracted) was the documented alternative and was not taken.
- **A new leaf package `internal/envelope` is introduced** (shared by tasks 080 and 083),
  with a new `make fitness-envelope-isolation` check (proposed, not built) asserting it
  stays a stdlib(+x/crypto) leaf and never enters `internal/supervisor`'s graph (F-003
  stays intact).
- **Task 083's "adopt agent-mesh as an IPC leaf" framing is revised:** there is no
  agent-mesh sign/verify binary to IPC for the envelope operation. 083's leaf is
  `internal/envelope` (shared with 080), not a separate `internal/agentmesh` IPC client —
  unless 083 *additionally* needs agent-mesh's live A2A transport between worker
  processes, which is a distinct concern. The planner should reconcile 083's REQ-083-04/05.
- **What becomes harder.** The channel now owns a small but real crypto surface (two
  primitives, an AEAD, a replay cache) that the project must keep correct and test
  thoroughly — rather than delegating it to an adopted block. This is the accepted cost of
  the block not exposing a consumable contract for the operation; the mitigation is that
  the construction is standard, the primitives are vetted (`crypto/ed25519`, `x/crypto`),
  and the wire format stays agent-mesh-compatible so a future swap is cheap.
- **All load-bearing invariants survive:** the verification gate, no-self-modification,
  one-task-one-branch, containment, and the executor seam are untouched; F-003 supervisor
  isolation is *preserved* by keeping the envelope/crypto strictly on the channel side of
  the `GoalSource` seam (a new fitness check guards it); armor remains load-bearing on the
  channel exactly as ADR 042 mandated, reusing the existing `armor.Guard` fail-closed
  adapter.
