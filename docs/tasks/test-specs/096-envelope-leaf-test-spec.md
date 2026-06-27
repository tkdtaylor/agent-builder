# Test spec — Task 096: internal/envelope leaf package

**Linked task:** `docs/tasks/backlog/096-envelope-leaf.md`
**Written:** 2026-06-27
**Status:** ready

## Context

`internal/envelope` is the shared cryptographic primitive that both task 080
(Telegram channel adapter) and task 083 (orchestrator↔worker transport) consume.
It is a **pure leaf**: it imports only stdlib (`crypto/ed25519`, `crypto/rand`,
`encoding/json`, `encoding/hex`, `time`) plus `golang.org/x/crypto` (approved
2026-06-27 in ADR 045). It must never appear in `internal/supervisor`'s import
graph (F-003 / F-007 described in REQ-096-05).

**ADR 045 §1–§3 govern every design decision.** The wire format mirrors agent-mesh's
`Envelope` JSON and `signingBytes()` canonicalization so the two remain
interoperable. The confidentiality layer uses X25519 + AEAD (nacl/box —
X25519+XSalsa20-Poly1305 from `golang.org/x/crypto/nacl/box`). The replay cache
mirrors agent-mesh's construction: both a time-freshness window (default 60 s,
config knob) and a nonce set bounded by `2×W` retention, evicting safely.

**What agent-mesh's wire format looks like** (from the block survey in ADR 045):

```json
{
  "from":    "<sender identity>",
  "to":      "<recipient identity>",
  "nonce":   "<hex-encoded 24-byte random nonce>",
  "ts":      "<RFC3339 or Unix-seconds timestamp>",
  "payload": "<base64-encoded AEAD ciphertext OR plaintext string>",
  "sig":     "<hex-encoded Ed25519 signature>"
}
```

`signingBytes()` is the canonical body that is signed: **canonical JSON marshalling
of the Envelope with Sig set to empty string**, matching agent-mesh's implementation
(see `mesh.go` ~65–68). This approach:
- Ensures byte-for-byte compatibility with agent-mesh for wire interoperability
- Prevents field-confusion attacks (no delimiter injection; unambiguous field boundaries)
- Mirrors agent-mesh's struct field order (From, To, Nonce, TS, Payload, Sig="")

**Why not pipe-delimited:** A bare `|` join (e.g. `from|to|nonce|ts|payload`) is
collision-prone: `{From:"a",To:"b|c"}` and `{From:"a|b",To:"c"}` both produce `a|b|c|ts|payload`
if the separator is not escaped. JSON marshalling closes this attack vector.

## Requirements coverage

| Req ID     | Description                                                                 | Test cases                        |
|------------|-----------------------------------------------------------------------------|-----------------------------------|
| REQ-096-01 | Sign/verify over Ed25519 using agent-mesh-compatible Envelope JSON shape    | TC-096-01, TC-096-02, TC-096-03   |
| REQ-096-02 | Seal/open confidentiality via X25519+AEAD (nacl/box); ciphertext ≠ payload | TC-096-04, TC-096-05              |
| REQ-096-03 | ReplayCache: time-freshness window + nonce set bounded by 2×W; eviction    | TC-096-06, TC-096-07, TC-096-08  |
| REQ-096-04 | Round-trip: Sign → Seal → Open → Verify delivers original plaintext         | TC-096-09                         |
| REQ-096-05 | Leaf-purity: imports only stdlib + golang.org/x/crypto; NOT in supervisor graph; fitness check exists | TC-096-10, TC-096-11 |

## Pre-implementation checklist

- [x] ADR 045 accepted (2026-06-27) — design authority for all decisions in this spec
- [x] `golang.org/x/crypto` approved as a new dependency (ADR 045, owner sign-off 2026-06-27)
- [x] agent-mesh wire format surveyed (ADR 045 §Context — `Envelope` JSON + `signingBytes()` canonicalization adopted)
- [x] Task 096 file written and dependencies confirmed (no internal/ blockers — this task is NOT blocked by tasks 080 or 081)

---

## Test cases

### TC-096-01 — Sign + verify happy path: signature over correct key verifies

- **Requirement:** REQ-096-01
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:**
Generate an Ed25519 keypair with `crypto/ed25519.GenerateKey(rand.Reader)`.
Construct an `Envelope` value with:
- `From = "operator"`, `To = "orchestrator"`
- `Nonce` = 24-byte random hex string
- `TS` = current time formatted consistently
- `Payload = "build the auth module"` (plaintext for this test; confidentiality is separate)

Call `envelope.Sign(env, privKey)` → returns a signed `Envelope` (the `Sig` field populated).

**Expected output:**
- `envelope.Verify(signedEnv, pubKey)` returns `nil`.
- The returned `Envelope`'s `Payload` field equals `"build the auth module"`.
- The `Sig` field is a non-empty hex string.

---

### TC-096-02 — Verify rejects envelope signed by an unknown key

- **Requirement:** REQ-096-01
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:**
Generate two Ed25519 keypairs: `(senderPriv, senderPub)` and `(otherPriv, otherPub)`.
Sign an envelope with `senderPriv`.

**Input:** Call `envelope.Verify(signedEnv, otherPub)` (the wrong public key).

**Expected output:**
- Returns a non-nil error.
- The error string contains `"unknown_key"` or `"bad_signature"` or `"signature"` (at least one; exact string is implementation's choice — TC asserts a substring, not a literal).
- No panic.

---

### TC-096-03 — Verify rejects a tampered envelope (payload mutated after signing)

- **Requirement:** REQ-096-01
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:**
Sign a valid envelope. Then mutate `signedEnv.Payload += "X"` (append one byte).

**Input:** `envelope.Verify(tamperedEnv, senderPub)`

**Expected output:**
- Returns a non-nil error.
- The error string contains `"bad_signature"` or `"tampered"` or `"signature"` (substring match).
- The `Payload` field has NOT been silently accepted as valid.

---

### TC-096-04 — Seal + Open round-trip: ciphertext is not equal to the plaintext

- **Requirement:** REQ-096-02
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup:**
Generate an X25519 keypair for sender `(senderX25519Priv, senderX25519Pub)` and
an X25519 keypair for recipient `(recipX25519Priv, recipX25519Pub)`.

Call `envelope.Seal(plaintext, senderX25519Priv, recipX25519Pub)` where
`plaintext = []byte("run the diagnostics task")`.

**Expected output:**
- Returns `(ciphertext []byte, nonce [24]byte, err)` where `err == nil`.
- `ciphertext` is not equal to `plaintext` (bytes differ; ciphertext is longer due to AEAD tag).
- `len(ciphertext) > len(plaintext)` (at minimum the Poly1305 tag adds 16 bytes).

---

### TC-096-05 — Open correctly decrypts ciphertext to original plaintext; tampered ciphertext fails

- **Requirement:** REQ-096-02
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/envelope_test.go`

**Setup (happy path):**
From TC-096-04's output: call `envelope.Open(ciphertext, nonce, recipX25519Priv, senderX25519Pub)`.

**Expected output (happy path):**
- Returns `(plaintext []byte, nil)`.
- `plaintext == []byte("run the diagnostics task")` (byte-equal).

**Setup (tamper path):**
Flip one byte in `ciphertext` (e.g. `ciphertext[0] ^= 0xFF`). Call `Open` again.

**Expected output (tamper path):**
- Returns `(nil, err)` where `err` is non-nil.
- Error contains `"authentication failed"` or `"decrypt"` or `"open"` (substring match — nacl/box returns a boolean from the underlying call; the wrapper must convert that to an error).
- No plaintext is returned on AEAD failure (the returned slice is nil or empty).

---

### TC-096-06 — ReplayCache: a fresh nonce within the time window is accepted; a stale timestamp is rejected

- **Requirement:** REQ-096-03
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/replay_test.go`

**Setup:**
Construct a `ReplayCache` with `Window = 60 * time.Second`.

**Input A (fresh):**
```go
nonce := "abc123unique"
ts    := time.Now()
err   := cache.Check(nonce, ts)
```

**Expected output A:** `err == nil` (fresh nonce within window is accepted).

**Input B (stale timestamp):**
```go
staleTS := time.Now().Add(-61 * time.Second)
err     := cache.Check("freshNonce", staleTS)
```

**Expected output B:** `err != nil`; error contains `"stale"` or `"timestamp"` or `"window"` (substring match).

**Input C (future timestamp outside window):**
```go
futureTS := time.Now().Add(61 * time.Second)
err      := cache.Check("futureNonce", futureTS)
```

**Expected output C:** `err != nil`; error contains `"stale"` or `"timestamp"` or `"window"`.

---

### TC-096-07 — ReplayCache: replayed nonce (same nonce, second call) is rejected

- **Requirement:** REQ-096-03
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/replay_test.go`

**Setup:**
Construct a `ReplayCache` with `Window = 60 * time.Second`.

**Step 1:** `cache.Check("nonce-XYZ", time.Now())` → must return `nil` (first-time accepted).
**Step 2:** `cache.Check("nonce-XYZ", time.Now())` → must return a non-nil error.

**Expected output (step 2):**
- `err != nil`.
- Error contains `"replay"` or `"seen"` or `"nonce"` (substring match).
- The nonce is durably recorded after step 1 (the cache is stateful between calls).

---

### TC-096-08 — ReplayCache: nonce set is bounded by 2×Window; evicted nonces don't prevent reuse beyond horizon

- **Requirement:** REQ-096-03
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/replay_test.go`

**Purpose:** Verify the eviction logic (mirrors agent-mesh's `retentionSecs()`):
a nonce older than `2×W` is evicted from the set, and the cache does not grow unbounded.

**Setup:**
Construct a `ReplayCache` with `Window = 100 * time.Millisecond` (short, so the test doesn't sleep long).

**Step 1:** Accept `"nonce-A"` at `T = time.Now()` (fresh). Verify `Check` returns nil.
**Step 2:** Record `"nonce-A"` is seen (step 1 was the accept call). Immediately
re-check with `"nonce-A"` → must return a replay error.
**Step 3:** Sleep `2*Window + 10ms` (210 ms total). At this point `"nonce-A"` has
aged beyond the `2×W` retention horizon.
**Step 4:** The cache's internal eviction runs (either on `Check` call or via a background sweep). Verify that the cache's nonce-set size has not grown unbounded.
**Step 5:** Call `cache.Check("nonce-A", time.Now())` — the nonce is now OLDER than
`2×W`, so even if it were re-accepted, the freshness check at step 5 would be
evaluated against `time.Now()` with an appropriate `ts`. Provide a fresh `ts` for
step 5.

**Expected output (step 5):** `err == nil` — because the nonce was evicted from the
set and the new `ts` is within the window. (This proves the cache does not leak old nonces indefinitely.) OR, if the implementation opts to NOT re-accept evicted nonces (stricter but unbounded), it must document that and the test must assert the bounded-size property via a direct inspection of the internal cache size (exported for testing or measured via a `Len()` method).

**Minimum assertion regardless of approach:** After 1000 `Check` calls with distinct
nonces at `ts = time.Now()` followed by the same 1000 nonces at `ts = time.Now()`
(replay attempts), and then after sleeping `2×W + 10ms`, calling `cache.Evict()` (or
a similar explicit eviction trigger), the cache's internal nonce count is ≤ 1000 (not
2000 or unbounded). If the implementation auto-evicts on `Check`, insert 1000 fresh
nonces, sleep `2×W`, insert 1000 new nonces, and assert the count is ≤ 1000. Document the approach chosen.

---

### TC-096-09 — Full round-trip: Sign+Seal → wire Envelope JSON → Unmarshal → Open+Verify → original plaintext

- **Requirement:** REQ-096-04
- **Level:** L2 (unit test)
- **Test file:** `internal/envelope/roundtrip_test.go`

**Purpose:** Proves the end-to-end path a sender and receiver exercise — from
plaintext goal string through the full envelope (sign + encrypt → JSON → decrypt + verify) back to the original plaintext.

**Setup:**
- Sender has: `(senderEdPriv, senderEdPub)`, `(senderX25519Priv, senderX25519Pub)`
- Recipient has: `(recipEdPriv, recipEdPub)`, `(recipX25519Priv, recipX25519Pub)`
- Recipient trusts `senderEdPub` and `senderX25519Pub` (the static trusted-key set)

**Steps (sender side):**
1. `plaintext = "deploy the new monitoring agent"`
2. `ciphertext, nonce, _ = envelope.Seal([]byte(plaintext), senderX25519Priv, recipX25519Pub)`
3. Encode `ciphertext` as base64 (or hex — implementation choice; consistent with the JSON shape).
4. Construct `Envelope{From:"operator", To:"orchestrator", Nonce: hex(nonce), TS: now, Payload: base64(ciphertext)}`
5. `signedEnv, _ = envelope.Sign(env, senderEdPriv)`
6. `jsonBytes, _ = json.Marshal(signedEnv)` → the wire representation.

**Steps (receiver side):**
7. `var received Envelope; json.Unmarshal(jsonBytes, &received)`
8. `err = envelope.Verify(received, senderEdPub)` → must be nil.
9. `decodedCipher = base64Decode(received.Payload)`
10. `got, err = envelope.Open(decodedCipher, nonceFromEnv, recipX25519Priv, senderX25519Pub)` → must be nil.

**Expected output:**
- `err` at steps 8 and 10 are both `nil`.
- `string(got) == "deploy the new monitoring agent"` (byte-equal to the original plaintext).
- The JSON bytes at step 6 contain `"from"`, `"to"`, `"nonce"`, `"ts"`, `"payload"`, `"sig"` keys (agent-mesh wire shape confirmed present).

---

### TC-096-10 — Leaf-purity: internal/envelope imports only stdlib + golang.org/x/crypto; no internal/ imports

- **Requirement:** REQ-096-05
- **Level:** L3 (import-graph check)
- **Harness:** `go list -deps ./internal/envelope/...`

**Input:** `go list -deps ./internal/envelope/...`

**Expected output:**
- The output contains `github.com/tkdtaylor/agent-builder/internal/envelope`.
- The output does NOT contain any path matching `github.com/tkdtaylor/agent-builder/internal/` other than `internal/envelope` itself.
  - Specifically forbidden: `internal/supervisor`, `internal/runtime`, `internal/armor`,
    `internal/audit`, `internal/policy`, `internal/vault`, `internal/recipe`,
    `internal/channel`, `internal/tasksource`, `internal/executor`, `internal/publisher`,
    `internal/sandbox`, `internal/secrets`, `internal/gate`, `internal/ingestion`.
- The output MAY contain `golang.org/x/crypto/...` paths (approved dependency).
- `go list` exits 0.

---

### TC-096-11 — F-007 fitness check: internal/envelope is absent from internal/supervisor's dependency graph

- **Requirement:** REQ-096-05
- **Level:** L3 (fitness check — new `make fitness-envelope-isolation` target)
- **Harness:** `make fitness-envelope-isolation`

**Input:** `make fitness-envelope-isolation`
(equivalent to: `go list -deps ./internal/supervisor/... | grep -F "internal/envelope"` returning no output, exit 0)

**Expected output:**
- `PASS fitness-envelope-isolation: internal/envelope is not in internal/supervisor's dependency graph.`
- Exit 0.

**Negative evidence (the check must be genuinely reachable):**
If a synthetic test file in `internal/supervisor` imports `internal/envelope`, the
check must emit:
- `FAIL fitness-envelope-isolation: internal/envelope appears in internal/supervisor's dependency graph.` (or similar naming)
- Exit 1.

**Makefile entry:** `fitness-envelope-isolation` must be added to the `.PHONY` list
and to the `fitness:` prerequisite list so `make fitness` runs it. The new check
must appear in `docs/spec/fitness-functions.md` (F-007 row — check category `isolation`,
threshold `0 occurrences`, severity `block`).

---

## Verification plan

- **Highest level achievable:** L3 — the envelope package has no CLI or runtime-observable
  surface of its own; correctness is proven by unit tests and import-graph checks.
- **L2 harness command:**
  ```
  go test -count=1 ./internal/envelope/...
  ```
  Expected: `ok github.com/tkdtaylor/agent-builder/internal/envelope`
- **L3 leaf-purity check:**
  ```
  go list -deps ./internal/envelope/... | grep 'agent-builder/internal/' | grep -v 'internal/envelope$'
  ```
  Expected: no output (only `internal/envelope` itself is in graph).
- **L3 fitness check:**
  ```
  make fitness-envelope-isolation
  ```
  Expected: `PASS fitness-envelope-isolation: ...` exit 0.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- The Telegram adapter (`internal/channel/telegram`) — task 080.
- The orchestrator↔worker transport wiring — task 083.
- Key rotation.
- Persistent replay cache (the v1 `ReplayCache` is in-memory; a persistent backend
  with memory-guard integration is a follow-on, explicitly out of scope per ADR 045 §3).
- Key-provisioning tooling (generating keypairs, distributing them); the task
  implements the cryptographic primitives; provisioning is the operator's
  out-of-band responsibility for v1.
