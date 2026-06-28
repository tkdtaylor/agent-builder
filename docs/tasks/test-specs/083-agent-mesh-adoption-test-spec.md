# Test spec — Task 083: orchestrator↔worker transport (signed envelopes via internal/envelope)

**Linked task:** `docs/tasks/backlog/083-agent-mesh-adoption.md`
**Written:** 2026-06-27
**Revised:** 2026-06-28 — expanded from stub to concrete TC-083-01..05 per ADR 048
  (in-process delivery v1 behind a transport-adapter seam; adapter package
  `internal/channel/worker`; agent-mesh A2A deferred). ADR 045 fixed the sign/verify
  primitive to `internal/envelope`; ADR 042 fixed the orchestrator↔worker boundary as a
  trust boundary.
**Status:** active

## Context

The orchestrator↔worker transport carries work-items (a sub-goal's `supervisor.Task`)
from the orchestrator to a worker and results back from the worker, each wrapped in an
`internal/envelope.Envelope` (Ed25519-signed, X25519+AEAD-sealed, replay-checked). Per
ADR 048 the v1 wire is **in-process** (matching task 081's sequential dispatch), but the
envelope is the load-bearing security layer regardless of the wire: tamper-evidence,
provenance, replay resistance, and a ready seam for a future out-of-process worker.

The transport adapter lives at `internal/channel/worker` (sibling of
`internal/channel/telegram`). It is a **leaf**: its only `agent-builder/internal/`
imports are `internal/envelope` and `internal/supervisor`. It also depends on
`internal/audit` for the `Sink` rejection-event seam — see the note under TC-083-04 for
why audit is on the allowed list alongside envelope+supervisor.

### Key roles (mirrors the telegram adapter exactly)

- **Work-item dispatch (orchestrator → worker):** orchestrator signs with its Ed25519
  private key (`orchEdPriv`), seals with its X25519 private key (`orchXPriv`) to the
  worker's X25519 public key (`workerXPub`). Envelope `From="orchestrator"`,
  `To="worker"`.
- **Result return (worker → orchestrator):** worker signs with its Ed25519 private key
  (`workerEdPriv`), seals with its X25519 private key (`workerXPriv`) to the
  orchestrator's X25519 public key (`orchXPub`). Envelope `From="worker"`,
  `To="orchestrator"`.

### SEC carry-forwards

- **Task 098 SEC-001:** after `Verify`+`Open` succeeds, the receiver MUST additionally
  assert the envelope `From`/`To` match the expected roles for the direction. Key
  separation alone is not sufficient — assert the declared roles too.
- No plaintext work-item/result on any logged surface; no key material in logs.
- Fresh per-message nonce (the nonce returned by `Seal`, which is `crypto/rand`-derived,
  never reused).

## Requirements coverage

| Req ID     | Description                                                                                       | Test cases  |
|------------|---------------------------------------------------------------------------------------------------|-------------|
| REQ-083-01 | Orchestrator sends a work-item to a worker as an Ed25519-signed + sealed envelope; worker verifies+opens to the byte-exact original sub-goal | TC-083-01 |
| REQ-083-02 | Worker result returned as a signed+sealed envelope; orchestrator verifies before incorporating; tampered signature → not incorporated + audit event | TC-083-02 |
| REQ-083-03 | Replayed envelope (same nonce) rejected at the receiver via `ReplayCache`; audit event; work-item not processed | TC-083-03 |
| REQ-083-04 | `internal/channel/worker` is a leaf — fitness `make fitness-worker-transport-isolation` (F-011) asserts no `internal/` imports except envelope + supervisor + audit | TC-083-04 |
| REQ-083-05 | Worker-transport constructor fails loudly + named when required key material is unset/absent — at startup, before accepting work | TC-083-05 |

## Pre-implementation checklist

- [x] Task 081 merged (orchestrator core)
- [x] Task 096 merged (`internal/envelope` leaf)
- [x] ADR 048 accepted (transport mechanism = in-process v1; adapter = `internal/channel/worker`)
- [x] Config knob name chosen: `AGENT_BUILDER_WORKER_SIGNING_KEY` (path to the
      orchestrator's Ed25519 signing-key file)
- [x] F-number assigned: F-011
- [x] All test cases refined into full inputs/expected-outputs

---

## Test cases

### TC-083-01 — Orchestrator sends a work-item as a signed+sealed envelope

- **Requirement:** REQ-083-01
- **Level:** L2 (unit test with in-process stub worker)
- **Status:** active

**Input:** A `supervisor.Task` sub-goal (`{ID:"sg-1", Repo:"exec-sandbox", Spec:"add rate limiter"}`).
The orchestrator-side sender wraps it via `Sender.DispatchWorkItem(task)` and the
in-process stub worker receives the resulting `envelope.Envelope`.

**Expected output (real assertions):**
- `envelope.Verify(received, orchEdPub)` returns `nil` (signature valid under the
  orchestrator's Ed25519 public key).
- The received envelope `From == "orchestrator"` and `To == "worker"`.
- After `envelope.Open` with the worker's X25519 private key and the orchestrator's
  X25519 public key, the decrypted bytes JSON-decode to a `supervisor.Task` that is
  **byte-exact `reflect.DeepEqual`** to the original sub-goal (round-trip).
- The nonce is non-empty and **unique across two dispatches** of the same task (dispatch
  the same task twice; assert the two nonces differ — proves a fresh `crypto/rand` nonce
  per message, never reused).
- The plaintext spec string `"add rate limiter"` does **not** appear anywhere in the
  marshalled envelope JSON (`Payload` is ciphertext, not plaintext).

---

### TC-083-02 — Worker result returned as a signed envelope; orchestrator verifies before incorporating; tamper rejected

- **Requirement:** REQ-083-02
- **Level:** L2 (unit test)
- **Status:** active

**Input (happy path):** A worker `Result` (`{Branch:"task/083", OK:true}`) wrapped by the
worker-side sender (`Sender.DispatchResult(result)`) and received by the orchestrator-side
receiver (`Receiver.ReceiveResult(env)`).

**Expected output (happy path):**
- `Receiver.ReceiveResult` returns the byte-exact `supervisor.Result`
  (`reflect.DeepEqual` to the original) and `nil` error.
- The received envelope `From == "worker"` and `To == "orchestrator"`.

**Input (tamper path):** Take the valid signed result envelope and flip one byte of the
`Sig` field (so `envelope.Verify` fails under the worker's Ed25519 public key). Feed it to
`Receiver.ReceiveResult`.

**Expected output (tamper path):**
- `Receiver.ReceiveResult` returns a non-nil error that satisfies
  `errors.Is(err, envelope.ErrBadSignature)` (the specific sentinel).
- The result is **NOT incorporated** (the returned `supervisor.Result` is the zero value).
- The audit `FakeSink` recorded exactly one `audit.ActionChannelReject` event whose
  `Detail.Reason` contains the rejection classification (e.g. `"unknown_key"`/
  `"bad_signature"` group, asserted via substring on the reason).

---

### TC-083-03 — Replayed envelope is rejected at the receiver; audit event; work-item not processed

- **Requirement:** REQ-083-03
- **Level:** L2 (unit test)
- **Status:** active

**Input:** A valid signed+sealed work-item envelope is delivered to the worker-side
receiver (`Receiver.ReceiveWorkItem(env)`) once, then the **same envelope (same nonce)** is
delivered a second time. A shared `*envelope.ReplayCache` is used across both calls.

**Expected output:**
- First delivery: returns the byte-exact `supervisor.Task` and `nil` error
  (work-item processed).
- Second delivery (same nonce): returns a non-nil error satisfying
  `errors.Is(err, envelope.ErrReplay)`.
- The returned `supervisor.Task` on the second delivery is the zero value
  (work-item **NOT processed**).
- The audit `FakeSink` recorded a `audit.ActionChannelReject` event whose `Detail.Reason`
  contains `"replay"` (substring assertion).

---

### TC-083-04 — `internal/channel/worker` is a leaf; fitness check asserts its isolation (F-011)

- **Requirement:** REQ-083-04
- **Level:** L3 (fitness check)
- **Status:** active

**Input:** `make fitness-worker-transport-isolation`, which runs
`go list -deps ./internal/channel/worker/...` and filters for
`github.com/tkdtaylor/agent-builder/internal/` paths.

**Expected output:**
- The only `agent-builder/internal/` paths in the dependency graph are
  `internal/channel/worker` itself, `internal/envelope`, `internal/supervisor`, and
  `internal/audit`.
- Any other `agent-builder/internal/` import (e.g. `internal/executor`,
  `internal/runtime`, `internal/orchestrator`) → check exits non-zero (`FAIL`).
- The target is added to `.PHONY`, to the `fitness:` prerequisites, and documented as
  **F-011** in `docs/spec/SPEC.md` and `docs/spec/fitness-functions.md`.

**Note on the allowed-import set:** ADR 048 §3 names `internal/envelope` and
`internal/supervisor` as the leaf's allowed internal imports. The adapter additionally
imports `internal/audit` for the rejection-event `Sink` seam — the same dependency the
telegram channel adapter (`internal/channel/telegram`) carries. `internal/audit` is itself
a verified leaf (F-005) with no executor/LLM/web reach, so including it does not widen the
transport's blast radius. F-011 therefore allows envelope + supervisor + audit and blocks
everything else.

---

### TC-083-05 — Missing key material → worker-transport startup fails loudly with a named error

- **Requirement:** REQ-083-05
- **Level:** L2 (unit test)
- **Status:** active

**Input:** Construct the worker transport via its config-driven constructor
(`NewSenderFromEnv` / `LoadKeyMaterial`) with `AGENT_BUILDER_WORKER_SIGNING_KEY` (a) unset
and (b) pointing to a nonexistent file path.

**Expected output (both sub-cases):**
- The constructor returns a non-nil error at construction time (startup), **before** any
  work-item is dispatched or received.
- The error satisfies `errors.Is(err, worker.ErrMissingSigningKey)` (a NAMED sentinel).
- The error message **names the missing configuration** — the string contains
  `"AGENT_BUILDER_WORKER_SIGNING_KEY"` (asserted via substring), not a generic
  "config error".
- No key material is read into a usable transport: the returned `*Sender` is nil.

---

## Verification plan

- **Highest level achievable:** L2 (unit tests with in-process stub workers) + L3 (the new
  fitness check). L5/L6 (live orchestrator + out-of-process worker round-trip) is deferred
  to a future out-of-process transport concrete (the envelope seam is ready for it; ADR
  048 §2/§5).
- **L2 + L3 harness commands:**
  ```
  go test -count=1 ./internal/channel/worker/... ./internal/orchestrator/...
  make fitness-worker-transport-isolation
  make fitness-supervisor-isolation
  ```
  Expected: `ok`; `PASS fitness-worker-transport-isolation`; `PASS fitness-supervisor-isolation`.
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- `internal/envelope` implementation — task 096.
- Orchestrator core — task 081.
- memory-guard for orchestrator state — task 084.
- Multi-worker concurrent dispatch — task 086.
- Key management / key distribution for worker signing keys (this task reads a configured
  Ed25519 key file; provisioning/rotation is separate).
- agent-mesh A2A transport (`serve`/`sendto`) — deferred (ADR 048 §5); if adopted, it is a
  new transport concrete behind the same `internal/channel/worker` seam.
- Full live wiring of the transport into `Orchestrator.dispatchPlan` (the orchestrator
  integration here is the startup key-material check + the envelope wrap/unwrap seam; deeper
  live dispatch is left to task 086's concurrent-dispatch work).
