# Test Spec 172: `DurableStore[P]`, a memory-guard-gated, crash-safe cross-session memory store

**Linked task:** [`docs/tasks/backlog/172-persistent-memory-store.md`](../backlog/172-persistent-memory-store.md)
**Written:** 2026-07-11
**Status:** ready for implementation

## Context

`docs/plans/roadmap.md`'s Forward arc item 4, "Persistent cross-session memory,
durable goal/skill/context memory across runs, guarded by memory-guard
(write-gate + delete-verify)", and `AGENTS.md`'s "Known missing" list both name
this gap directly. The existing `memoryguard.MemoryGuardStore[P]`
(`internal/memoryguard/store.go`, task 084) gates writes and deletes through
memory-guard but is explicitly documented as NOT durable: its `entries` field is
an in-process `map[string]planEntry[P]`, lost on restart, and its `Get` is
"purely in-process (no IPC on the read path in this task's scope)"
(`store.go:77-84`), never calling a read-gate at all.

This task adds `DurableStore[P]`, a NEW sibling type in `internal/memoryguard`
that closes both gaps at once: every write goes through `Client.ValidateWrite`
(existing, unmodified) AND is durably persisted (crash-safe append-only
JSONL + snapshot, the SAME pattern task 167 established for `internal/runstore`,
reimplemented here rather than shared as a cross-package dependency, since
`internal/memoryguard` must remain a stdlib-only leaf per F-012 and cannot
import `internal/runstore`); every read goes through the NEW
`Client.ValidateRead` (task 165) AND fails closed (returns the read-gate's
denial, not the cached value) when memory-guard denies it.

**Module boundary:** `internal/memoryguard` only, a strict leaf (F-012). This
task adds a new file (`internal/memoryguard/durable_store.go`) and its test; it
does not modify `MemoryGuardStore[P]`, `Client`, or anything in
`internal/orchestrator` (task 173 swaps the orchestrator's `PlanStore` backend
onto this new type).

---

## Requirements coverage

| Req ID     | Description | Test cases |
|------------|--------------|------------|
| REQ-172-01 | `NewDurableStore[P any](client *Client, identity, dir string) (*DurableStore[P], error)` constructs a store rooted at `dir` (creating `journal.jsonl`/`snapshot.json` conventions mirroring `internal/runstore`'s exact on-disk layout and crash-safety guarantees: `0600` mode, `fsync`'d appends, temp+rename atomic snapshot writes). | TC-172-01 |
| REQ-172-02 | `Put(key string, value P) error` calls `client.ValidateWrite` FIRST; on `ErrWriteGateDenied`, returns it WITHOUT writing to disk (fail closed, no partial durable write of a denied entry); on allow, durably appends the entry (crash-safe) and updates the in-memory index. | TC-172-02, TC-172-03 |
| REQ-172-03 | `Get(key string) (value P, ok bool, err error) ` calls `client.ValidateRead(key, identity)` FIRST; on `ErrReadGateDenied`, returns the ZERO value, `ok=false`, and the wrapped error (fail closed, the caller never sees the value memory-guard denied); on allow, returns the durably-stored value. | TC-172-04, TC-172-05 |
| REQ-172-04 | `Delete(key string) error` calls `client.VerifyDelete` (existing, unmodified) and, on success, durably removes the entry (a tombstone, matching `internal/runstore`'s `Delete` convention); a tamper signal (`ErrTamperDetected`) is returned, and the in-process index still drops the entry (tampered state is unusable, mirroring `MemoryGuardStore[P].Delete`'s existing documented behavior). | TC-172-06, TC-172-07 |
| REQ-172-05 | A freshly-constructed `DurableStore[P]` sharing a prior instance's `dir` (same `client`, independently constructed) immediately reflects every durably-written `Put`/`Delete` from the prior instance (the cross-restart proof). | TC-172-08 |
| REQ-172-06 | `internal/memoryguard` remains a strict leaf (F-012 unaffected: no new import beyond what task 165 already added). | TC-172-09 |
| REQ-172-07 | Pre-existing `internal/memoryguard` suites (`ValidateWrite`, `VerifyDelete`, `ValidateRead` from task 165, `MemoryGuardStore[P]`) pass unchanged. | TC-172-10 |

---

## Pre-implementation checklist

- [x] Task 165 merged (`Client.ValidateRead`/`ErrReadGateDenied` exist)
- [x] Task 167 merged (the crash-safe journal pattern this task reimplements
  locally; read task 167's `filestore.go` for the exact append/replay/compact
  mechanics to mirror, do not attempt a cross-package import)
- [ ] `make check` green on `main` before branching

---

## Test cases

### TC-172-01, construction and basic round trip

- **Requirement:** REQ-172-01
- **Level:** L2 (unit test, real `Client` with a recording `ExecRunner` stub)
- **Test file:** `internal/memoryguard/durable_store_test.go` (new)

**Step:** `NewDurableStore[string](client, "agent-builder/test", t.TempDir())`,
`Put("k1", "v1")` (stub `ValidateWrite` to allow), `Get("k1")` (stub
`ValidateRead` to allow).

**Expected output:** `Get` returns `("v1", true, nil)`; `<dir>/journal.jsonl`
exists, mode `0600`, contains one well-formed JSON line.

---

### TC-172-02, `Put` fails closed on write-gate denial (no disk write)

- **Requirement:** REQ-172-02
- **Level:** L2

**Step:** Stub `ValidateWrite` to return `ErrWriteGateDenied`. `Put("k1", "v1")`.

**Expected output:** `errors.Is(err, memoryguard.ErrWriteGateDenied) == true`;
`<dir>/journal.jsonl` does NOT exist (or exists but is empty, zero lines),
proving the denial happened BEFORE any disk write, not a write-then-reject.

---

### TC-172-03, `Put` durably writes on allow

- **Requirement:** REQ-172-02
- **Level:** L2

**Step:** Stub `ValidateWrite` to allow. `Put("k1", "v1")`, `Put("k2", "v2")`.

**Expected output:** `<dir>/journal.jsonl` has exactly 2 lines; both round-trip
via `Get` after allowing `ValidateRead`.

---

### TC-172-04, `Get` fails closed on read-gate denial (cached value never returned)

- **Requirement:** REQ-172-03
- **Level:** L2

**Step:** `Put("k1", "v1")` (write-gate allowed). Stub `ValidateRead` to return
`ErrReadGateDenied`. `Get("k1")`.

**Expected output:** returns `(zero-value, false, err)` where
`errors.Is(err, memoryguard.ErrReadGateDenied) == true`, NEVER `"v1"` even
though it is durably present on disk and in the in-memory index, this is the
load-bearing proof that reads are gated, not merely written durably.

---

### TC-172-05, `Get` returns the durable value on allow

- **Requirement:** REQ-172-03
- **Level:** L2

**Step:** `Put("k1", "v1")`, stub `ValidateRead` to allow, `Get("k1")`.

**Expected output:** `("v1", true, nil)`. `Get("unknown-key")` (never `Put`)
returns `(zero-value, false, nil)` WITHOUT calling `ValidateRead` at all (no
gate call for a key that was never written, mirroring a plain map's absence
semantics, assert via the stub's call count).

---

### TC-172-06, `Delete` on tamper still drops the in-process entry

- **Requirement:** REQ-172-04
- **Level:** L2 (mirrors `MemoryGuardStore[P].Delete`'s existing documented
  tamper behavior exactly)

**Step:** `Put("k1", "v1")`. Stub `VerifyDelete` to return
`ErrTamperDetected`. `Delete("k1")`, then (stubbing `ValidateRead` to allow)
`Get("k1")`.

**Expected output:** `Delete` returns `errors.Is(err,
memoryguard.ErrTamperDetected) == true`; the subsequent `Get("k1")` returns
`(zero-value, false, nil)`, the entry is gone from the in-process index
regardless of the tamper signal, matching `store.go:90-107`'s documented
"tampered state is unusable" rule.

---

### TC-172-07, `Delete` durably removes the entry (tombstone survives restart)

- **Requirement:** REQ-172-04
- **Level:** L5 (two independently-constructed `DurableStore[P]` sharing one
  directory, mirroring TC-167-11/TC-168-07's pattern)

**Step:** `store1 := NewDurableStore[string](client, id, dir)`, `Put("k1",
"v1")` (allowed), `Delete("k1")` (`VerifyDelete` allowed, no tamper). Construct
`store2 := NewDurableStore[string](client, id, dir)` fresh, `Get("k1")` (allow
`ValidateRead`).

**Expected output:** `store2.Get("k1")` returns `(zero-value, false, nil)`, the
deletion is durable across independent construction, not merely in `store1`'s
in-memory index.

---

### TC-172-08, cross-construction durability for writes (the load-bearing proof)

- **Requirement:** REQ-172-05
- **Level:** L5

**Step:** `store1`, `Put("k1", "v1")`, `Put("k2", "v2")` (both allowed).
Construct `store2` fresh on the same `dir`. `Get("k1")`, `Get("k2")` (allow
`ValidateRead`).

**Expected output:** both return their durably-written values, proving state
survives independent construction (simulating a process restart), not just
in-memory accumulation within one `DurableStore` instance.

---

### TC-172-09, leaf isolation unaffected (F-012)

- **Requirement:** REQ-172-06
- **Level:** L3 (fitness)

**Step:** `make fitness-memoryguard-isolation`

**Expected output:** unchanged pass, zero violations, the new file uses only
`encoding/json`, `os`, `path/filepath`, `sync`, `time`, `fmt`, `errors`,
`bufio` (all already-available-to-a-leaf stdlib packages, matching task 167's
`internal/runstore` import set exactly, reimplemented locally per this
package's leaf constraint).

---

### TC-172-10, full regression

- **Requirement:** REQ-172-07
- **Level:** L2/L3

**Step:**
```
go test -race -count=1 ./internal/memoryguard/...
make check
```

**Expected output:** all `ok`, `ValidateWrite`/`VerifyDelete`/`ValidateRead`/`MemoryGuardStore[P]`
suites byte-identical; `make check` → `All checks passed.`

---

## Verification plan

- **Highest level achievable:** L5, TC-172-07/08's two-independently-constructed
  `DurableStore` cross-restart proofs.
- **L2 harness command:**
  ```
  go test -race -count=1 ./internal/memoryguard/... -run TestTC172
  ```
- **L5 harness command:**
  ```
  go test -race -count=1 -v ./internal/memoryguard/... -run 'TestTC172_07|TestTC172_08'
  ```
- **Full gate:**
  ```
  make check
  ```
  Expected: `All checks passed.`

## Out of scope

- Swapping `orchestrator.PlanStore`'s backend onto `DurableStore[Plan]` (task 173).
- Sharing crash-safe-journal implementation code between `internal/runstore` and
  `internal/memoryguard` (both stay independent leaves per their respective
  fitness invariants; a shared low-level primitive package is a future
  refactor, not required by this task).
- Compaction scheduling policy (mirrors task 167's own out-of-scope note: the
  primitive is provided, callers decide when to compact).
