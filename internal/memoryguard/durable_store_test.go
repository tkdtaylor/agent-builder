package memoryguard_test

// Task 172: DurableStore[P] — memory-guard-gated, crash-safe cross-session store.
// Reuses stubRunner/newStubRunner from memoryguard_test.go.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
)

func allowWrite(s *stubRunner)  { s.setResponse("validate_write", map[string]any{"allow": true, "stored_id": "sid"}) }
func denyWrite(s *stubRunner)   { s.setResponse("validate_write", map[string]any{"allow": false}) }
func allowRead(s *stubRunner)   { s.setResponse("validate_read", map[string]any{"allow": true, "content_redacted": ""}) }
func denyRead(s *stubRunner)    { s.setResponse("validate_read", map[string]any{"allow": false}) }
func allowDelete(s *stubRunner) { s.setResponse("verify_delete", map[string]any{"confirmed": true, "residue_detected": false}) }
func tamperDelete(s *stubRunner) {
	s.setResponse("verify_delete", map[string]any{"confirmed": false, "residue_detected": true})
}

func opCount(s *stubRunner, op string) int {
	n := 0
	for _, o := range s.opsInOrder() {
		if o == op {
			n++
		}
	}
	return n
}

func journalLines(t *testing.T, dir string) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read journal: %v", err)
	}
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// TC-172-01: construction and basic round trip; journal is 0600 with one line.
func TestTC172_01_ConstructRoundTrip(t *testing.T) {
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	dir := t.TempDir()
	store, err := memoryguard.NewDurableStore[string](client, "agent-builder/test", dir)
	if err != nil {
		t.Fatalf("NewDurableStore: %v", err)
	}
	if err := store.Put("k1", "v1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, ok, err := store.Get("k1")
	if err != nil || !ok || v != "v1" {
		t.Fatalf("Get(k1) = (%q, %v, %v), want (v1, true, nil)", v, ok, err)
	}
	if lines := journalLines(t, dir); len(lines) != 1 {
		t.Fatalf("journal lines = %d, want 1", len(lines))
	}
	fi, err := os.Stat(filepath.Join(dir, "journal.jsonl"))
	if err != nil {
		t.Fatalf("stat journal: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("journal mode = %o, want 600", perm)
	}
}

// TC-172-02: Put fails closed on write-gate denial, writes nothing.
func TestTC172_02_PutDeniedNoWrite(t *testing.T) {
	stub := newStubRunner()
	denyWrite(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	dir := t.TempDir()
	store, _ := memoryguard.NewDurableStore[string](client, "id", dir)

	err := store.Put("k1", "v1")
	if !errors.Is(err, memoryguard.ErrWriteGateDenied) {
		t.Fatalf("Put err = %v, want ErrWriteGateDenied", err)
	}
	if lines := journalLines(t, dir); len(lines) != 0 {
		t.Fatalf("journal has %d lines after a denied Put, want 0 (denial before any disk write)", len(lines))
	}
}

// TC-172-03: Put durably writes on allow.
func TestTC172_03_PutAllowedDurable(t *testing.T) {
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	dir := t.TempDir()
	store, _ := memoryguard.NewDurableStore[string](client, "id", dir)
	if err := store.Put("k1", "v1"); err != nil {
		t.Fatalf("Put k1: %v", err)
	}
	if err := store.Put("k2", "v2"); err != nil {
		t.Fatalf("Put k2: %v", err)
	}
	if lines := journalLines(t, dir); len(lines) != 2 {
		t.Fatalf("journal lines = %d, want 2", len(lines))
	}
	for k, want := range map[string]string{"k1": "v1", "k2": "v2"} {
		v, ok, err := store.Get(k)
		if err != nil || !ok || v != want {
			t.Errorf("Get(%s) = (%q,%v,%v), want (%s,true,nil)", k, v, ok, err, want)
		}
	}
}

// TC-172-04: Get fails closed on read-gate denial, never returns the cached value.
func TestTC172_04_GetDeniedNeverReturnsCached(t *testing.T) {
	stub := newStubRunner()
	allowWrite(stub)
	denyRead(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	store, _ := memoryguard.NewDurableStore[string](client, "id", t.TempDir())
	if err := store.Put("k1", "v1"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	v, ok, err := store.Get("k1")
	if !errors.Is(err, memoryguard.ErrReadGateDenied) {
		t.Fatalf("Get err = %v, want ErrReadGateDenied", err)
	}
	if ok || v != "" {
		t.Fatalf("Get on denied read = (%q, %v), want (\"\", false) — the cached value must NEVER be returned", v, ok)
	}
}

// TC-172-05: Get returns the durable value on allow; unknown key skips the gate.
func TestTC172_05_GetAllowedAndUnknownKeySkipsGate(t *testing.T) {
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	store, _ := memoryguard.NewDurableStore[string](client, "id", t.TempDir())
	_ = store.Put("k1", "v1")

	v, ok, err := store.Get("k1")
	if err != nil || !ok || v != "v1" {
		t.Fatalf("Get(k1) = (%q,%v,%v), want (v1,true,nil)", v, ok, err)
	}
	readsAfterKnown := opCount(stub, "validate_read")

	rv, rok, rerr := store.Get("unknown-key")
	if rerr != nil || rok || rv != "" {
		t.Fatalf("Get(unknown) = (%q,%v,%v), want (\"\",false,nil)", rv, rok, rerr)
	}
	if opCount(stub, "validate_read") != readsAfterKnown {
		t.Errorf("Get(unknown) called validate_read; a never-written key must skip the read gate")
	}
}

// TC-172-06: Delete on tamper still drops the in-process entry.
func TestTC172_06_DeleteTamperDropsEntry(t *testing.T) {
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	tamperDelete(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	store, _ := memoryguard.NewDurableStore[string](client, "id", t.TempDir())
	_ = store.Put("k1", "v1")

	err := store.Delete("k1")
	if !errors.Is(err, memoryguard.ErrTamperDetected) {
		t.Fatalf("Delete err = %v, want ErrTamperDetected", err)
	}
	v, ok, gerr := store.Get("k1")
	if gerr != nil || ok || v != "" {
		t.Fatalf("Get(k1) after tampered Delete = (%q,%v,%v), want (\"\",false,nil) — entry must be gone", v, ok, gerr)
	}
}

// TC-172-07: Delete durably removes the entry (tombstone survives restart) — L5.
func TestTC172_07_DeleteDurableAcrossConstruction(t *testing.T) {
	dir := t.TempDir()
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	allowDelete(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)

	store1, _ := memoryguard.NewDurableStore[string](client, "id", dir)
	_ = store1.Put("k1", "v1")
	if err := store1.Delete("k1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	store2, err := memoryguard.NewDurableStore[string](client, "id", dir)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	v, ok, gerr := store2.Get("k1")
	if gerr != nil || ok || v != "" {
		t.Fatalf("store2.Get(k1) = (%q,%v,%v), want (\"\",false,nil) — tombstone must survive reconstruction", v, ok, gerr)
	}
}

// TC-172-08: cross-construction durability for writes (the load-bearing proof) — L5.
func TestTC172_08_WritesDurableAcrossConstruction(t *testing.T) {
	dir := t.TempDir()
	stub := newStubRunner()
	allowWrite(stub)
	allowRead(stub)
	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)

	store1, _ := memoryguard.NewDurableStore[string](client, "id", dir)
	_ = store1.Put("k1", "v1")
	_ = store1.Put("k2", "v2")

	store2, err := memoryguard.NewDurableStore[string](client, "id", dir)
	if err != nil {
		t.Fatalf("store2: %v", err)
	}
	for k, want := range map[string]string{"k1": "v1", "k2": "v2"} {
		v, ok, gerr := store2.Get(k)
		if gerr != nil || !ok || v != want {
			t.Errorf("store2.Get(%s) = (%q,%v,%v), want (%s,true,nil) — write must survive reconstruction", k, v, ok, gerr, want)
		}
	}
}
