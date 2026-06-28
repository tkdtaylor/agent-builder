package memoryguard

import (
	"encoding/json"
	"fmt"
	"sync"
)

// planEntry holds in-process plan state alongside the stored_id returned by
// the memory-guard write-gate. The stored_id is the opaque handle passed to
// verify_delete calls.
type planEntry[P any] struct {
	plan     P
	storedID string
}

// MemoryGuardStore is a generic plan-state store that gates every write through
// the memory-guard write-gate (validate_write) and every delete through the
// delete-verify (verify_delete). It is kept generic (type parameter P) so this
// package does not import internal/orchestrator — the caller provides the concrete
// plan type and key-extraction logic via its own adapter.
//
// P must be serialisable by encoding/json (used to build the IPC entry string).
//
// Usage: callers in internal/orchestrator wrap this with an adapter that maps the
// orchestrator's PlanStore.Put(Plan) to Put(plan.GoalID, plan) on this type.
type MemoryGuardStore[P any] struct {
	mu       sync.Mutex
	client   *Client
	entries  map[string]planEntry[P]
	identity string
}

// NewMemoryGuardStore constructs a MemoryGuardStore backed by the given Client.
// identity is the actor label forwarded in IPC requests (e.g. "agent-builder/orchestrator").
func NewMemoryGuardStore[P any](client *Client, identity string) *MemoryGuardStore[P] {
	return &MemoryGuardStore[P]{
		client:   client,
		entries:  make(map[string]planEntry[P]),
		identity: identity,
	}
}

// StoredID returns the memory-guard stored_id for the given key, and whether one
// is held. Used in tests to assert the handle is held (TC-084-01).
func (s *MemoryGuardStore[P]) StoredID(key string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok {
		return "", false
	}
	return e.storedID, true
}

// Put serialises plan as JSON, sends it through the memory-guard write-gate, and
// on success records both the plan and the returned stored_id under key. It returns
// ErrWriteGateDenied when the guard rejects the write (fail-closed). Any other error
// (transport, parse, serialisation) is returned and must not be swallowed by the caller.
func (s *MemoryGuardStore[P]) Put(key string, plan P) error {
	entryJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("memoryguard store: serialise plan: %w", err)
	}

	storedID, err := s.client.ValidateWrite(string(entryJSON), s.identity)
	if err != nil {
		return fmt.Errorf("memoryguard store: write-gate: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = planEntry[P]{plan: plan, storedID: storedID}
	return nil
}

// Get returns the plan stored under key and whether one was found. The look-up is
// purely in-process (no IPC on the read path in this task's scope).
func (s *MemoryGuardStore[P]) Get(key string) (P, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	return e.plan, ok
}

// Delete removes the entry for key after calling verify_delete on its stored_id.
// It returns ErrTamperDetected (wrapped) when the memory-guard block reports
// confirmed=false or residue_detected=true. The entry is removed from the in-process
// index regardless of the tamper signal — tampered state is unusable.
func (s *MemoryGuardStore[P]) Delete(key string) error {
	s.mu.Lock()
	e, ok := s.entries[key]
	if ok {
		delete(s.entries, key)
	}
	s.mu.Unlock()

	if !ok {
		// No entry — nothing to verify. Matches MemoryPlanStore.Delete (no-op).
		return nil
	}

	if err := s.client.VerifyDelete(e.storedID); err != nil {
		return fmt.Errorf("memoryguard store: delete-verify for key %q: %w", key, err)
	}
	return nil
}
