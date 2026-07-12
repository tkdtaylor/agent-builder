# Task 172: `DurableStore[P]`, a memory-guard-gated, crash-safe cross-session memory store

**Project:** agent-builder
**Created:** 2026-07-11
**Status:** backlog

## Goal

Add `DurableStore[P any]` to `internal/memoryguard`: every write gated through
`ValidateWrite` AND durably persisted (crash-safe, survives a restart); every
read gated through the new `ValidateRead` (task 165) AND fails closed on
denial, never returning a cached value the guard just rejected.

## Context

`docs/plans/roadmap.md`'s Forward arc item 4 and `AGENTS.md`'s "Known missing"
list both name persistent cross-session memory, guarded by memory-guard, as an
unbuilt capability. The existing `MemoryGuardStore[P]` (task 084,
`internal/memoryguard/store.go`) gates writes but is explicitly NOT durable (an
in-process map, lost on restart) and its `Get` never calls a read-gate at all,
documented directly in its own source: "purely in-process (no IPC on the read
path in this task's scope)" (`store.go:77-84`).

This task closes both gaps with a new sibling type, reusing the exact crash-safe
journal mechanics task 167 established for `internal/runstore` (append-only
JSONL, `fsync`'d appends, temp+rename atomic snapshots), reimplemented locally
inside `internal/memoryguard` since that package must remain a stdlib-only leaf
(F-012) and cannot import `internal/runstore`.

**Reference:**
- `internal/memoryguard/store.go:1-108` (`MemoryGuardStore[P]`, the pattern this
  task's gating calls mirror, NOT modified by this task)
- `internal/memoryguard/memoryguard.go` (`Client.ValidateWrite`/`VerifyDelete`,
  unmodified; `Client.ValidateRead` from task 165, consumed here)
- `internal/runstore/filestore.go` (task 167, the crash-safe journal mechanics
  to mirror: read it before implementing this task's `Put`/`Get`/`Delete`
  persistence)
- `docs/plans/roadmap.md` Forward arc item 4

## Requirements

| Req ID     | Description | Priority |
|------------|--------------|----------|
| REQ-172-01 | `NewDurableStore[P any](client *Client, identity, dir string) (*DurableStore[P], error)`, crash-safe on-disk layout mirroring `internal/runstore`'s. | must have |
| REQ-172-02 | `Put` calls `ValidateWrite` first; denial writes nothing to disk. | must have |
| REQ-172-03 | `Get` calls `ValidateRead` first; denial never returns the cached value. | must have |
| REQ-172-04 | `Delete` calls `VerifyDelete`; tamper still drops the in-process entry, durably. | must have |
| REQ-172-05 | A fresh instance sharing a prior instance's `dir` reflects all its durable writes/deletes. | must have |
| REQ-172-06 | `internal/memoryguard` remains a strict leaf (F-012 unaffected). | must have |
| REQ-172-07 | Pre-existing `internal/memoryguard` suites pass unchanged. | must have |

## Readiness gate

- [x] Test spec `docs/tasks/test-specs/172-persistent-memory-store-test-spec.md` exists (written first)
- [x] Task 165 merged (`ValidateRead` exists)
- [x] Task 167 merged (the journal pattern to mirror)
- [ ] `make check` green on `main` before branching

## Implementation outline

1. New file `internal/memoryguard/durable_store.go`:
   ```go
   type DurableStore[P any] struct {
       mu       sync.Mutex
       client   *Client
       identity string
       dir      string
       index    map[string]P
       renameFunc func(oldpath, newpath string) error
   }
   ```
2. `NewDurableStore[P any](client *Client, identity, dir string) (*DurableStore[P], error)`:
   mirror `internal/runstore.NewFileStore`'s construction sequence exactly
   (`MkdirAll`, load `snapshot.json` if present, replay `journal.jsonl` with the
   same truncated-final-line-tolerant / mid-file-corruption-fails-loud rule
   task 167 established), building `index map[string]P` instead of
   `map[string]runstore.Record`.
3. `Put(key string, value P) error`: call `c.client.ValidateWrite(<marshaled
   value>, s.identity)` FIRST; on `ErrWriteGateDenied`, return immediately,
   no disk write. On allow, append a crash-safe JSONL line (`{"key":...,
   "value":...}`, `0600`, `fsync`'d, mirroring `internal/runstore.Save`'s exact
   write sequence), update `index[key] = value`.
4. `Get(key string) (value P, ok bool, err error)`: look up `index[key]` first
   (in-memory, for existence only, not returned yet); if absent, return the
   zero value, `false`, `nil` WITHOUT calling `ValidateRead` (no gate call for a
   key that was never written). If present, call
   `s.client.ValidateRead(key, s.identity)`; on `ErrReadGateDenied`, return the
   ZERO value (never the cached one), `false`, the wrapped error. On allow,
   return `index[key]`, `true`, `nil`.
5. `Delete(key string) error`: mirror `MemoryGuardStore[P].Delete`'s exact
   shape (`store.go:90-107`): call `VerifyDelete`, remove from `index`
   regardless of the tamper signal, additionally append a durable tombstone
   line (mirroring `internal/runstore.Delete`'s convention) so the removal
   survives a restart, and return the tamper error if any.
6. `Compact() error`: mirror `internal/runstore.FileStore.Compact` exactly
   (temp+rename atomic snapshot, then truncate the journal).
7. Tests per the test spec.

## Acceptance criteria

- [ ] [REQ-172-01] TC-172-01: construction and basic round trip.
- [ ] [REQ-172-02] TC-172-02/03: write-gate denial writes nothing; allow durably writes.
- [ ] [REQ-172-03] TC-172-04/05: read-gate denial never returns cached value; allow returns it.
- [ ] [REQ-172-04] TC-172-06/07: tamper drops in-process entry; deletion is durable.
- [ ] [REQ-172-05] TC-172-08: cross-construction durability for writes (L5).
- [ ] [REQ-172-06] TC-172-09: `make fitness-memoryguard-isolation` passes.
- [ ] [REQ-172-07] TC-172-10: `go test -race -count=1 ./internal/memoryguard/...` passes; `make check` passes.

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

## Spec/doc footprint (update in the feat commit)

- `docs/spec/interfaces.md`: `internal/memoryguard` section gains
  `DurableStore[P]`.
- `docs/spec/data-model.md`: the on-disk journal/snapshot layout documented
  alongside `internal/runstore`'s equivalent entry, noting the two are
  independent (not shared) implementations of the same pattern.

## Out of scope

- Swapping `orchestrator.PlanStore`'s backend onto `DurableStore[Plan]` (task 173).
- Sharing implementation code between `internal/runstore` and
  `internal/memoryguard`.
- Compaction scheduling policy.

## Dependencies

- **Blocks on:** task 165, 167.
- **Blocks:** task 173.
