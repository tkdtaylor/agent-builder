package memoryguard_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
)

// --- stub ExecRunner ---------------------------------------------------------

// stubCall records one invocation of the ExecRunner.
type stubCall struct {
	binPath string
	req     map[string]any
}

// stubRunner is an injectable ExecRunner that returns configurable responses per op.
type stubRunner struct {
	calls     []stubCall
	responses map[string][]byte // keyed by op value
	errors    map[string]error  // keyed by op value
}

func newStubRunner() *stubRunner {
	return &stubRunner{
		responses: make(map[string][]byte),
		errors:    make(map[string]error),
	}
}

// setResponse configures the JSON response the stub returns for a given op.
func (r *stubRunner) setResponse(op string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("stub: marshal response for op %q: %v", op, err))
	}
	r.responses[op] = b
}

func (r *stubRunner) Run(binPath string, reqJSON []byte) ([]byte, error) {
	var req map[string]any
	if err := json.Unmarshal(reqJSON, &req); err != nil {
		return nil, fmt.Errorf("stub: bad request JSON: %w", err)
	}
	op, _ := req["op"].(string)
	r.calls = append(r.calls, stubCall{binPath: binPath, req: req})

	if err, ok := r.errors[op]; ok {
		return nil, err
	}
	if resp, ok := r.responses[op]; ok {
		return resp, nil
	}
	return nil, fmt.Errorf("stub: no response configured for op %q", op)
}

// opsInOrder returns the sequence of op values in call order.
func (r *stubRunner) opsInOrder() []string {
	out := make([]string, len(r.calls))
	for i, c := range r.calls {
		out[i], _ = c.req["op"].(string)
	}
	return out
}

// --- TC-084-01: Client.ValidateWrite -----------------------------------------

func TestTC084_01_ValidateWrite_Success(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow":     true,
		"stored_id": "stub-id-1",
		"flags":     nil,
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	storedID, err := client.ValidateWrite(`{"goal":"test-goal"}`, "agent-builder/test")
	if err != nil {
		t.Fatalf("TC-084-01: ValidateWrite: unexpected error: %v", err)
	}
	if storedID != "stub-id-1" {
		t.Errorf("TC-084-01: stored_id: want %q, got %q", "stub-id-1", storedID)
	}

	// Assert the IPC op was sent correctly.
	if len(stub.calls) != 1 {
		t.Fatalf("TC-084-01: want 1 stub call, got %d", len(stub.calls))
	}
	call := stub.calls[0]
	if call.req["op"] != "validate_write" {
		t.Errorf("TC-084-01: IPC op: want %q, got %q", "validate_write", call.req["op"])
	}
	// entry must be the non-empty JSON string we passed.
	entry, _ := call.req["entry"].(string)
	if !strings.Contains(entry, "test-goal") {
		t.Errorf("TC-084-01: IPC entry must contain plan JSON, got %q", entry)
	}
	identity, _ := call.req["identity"].(string)
	if identity != "agent-builder/test" {
		t.Errorf("TC-084-01: IPC identity: want %q, got %q", "agent-builder/test", identity)
	}
}

func TestTC084_01_ValidateWrite_Denied(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow":     false,
		"stored_id": "",
		"flags":     []string{"injection_detected"},
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	_, err := client.ValidateWrite(`{"goal":"bad"}`, "agent-builder/test")
	if err == nil {
		t.Fatal("TC-084-01: want ErrWriteGateDenied, got nil")
	}
	if !errors.Is(err, memoryguard.ErrWriteGateDenied) {
		t.Errorf("TC-084-01: want errors.Is(err, ErrWriteGateDenied), got: %v", err)
	}
}

// --- TC-084-02: Client.VerifyDelete ------------------------------------------

func TestTC084_02_VerifyDelete_Clean(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("verify_delete", map[string]any{
		"confirmed":        true,
		"residue_detected": false,
		"deletion_hash":    "abc123",
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	if err := client.VerifyDelete("stub-id-2"); err != nil {
		t.Fatalf("TC-084-02: VerifyDelete clean: unexpected error: %v", err)
	}
	if len(stub.calls) != 1 || stub.calls[0].req["op"] != "verify_delete" {
		t.Errorf("TC-084-02: want 1 verify_delete call, got %v", stub.opsInOrder())
	}
	id, _ := stub.calls[0].req["id"].(string)
	if id != "stub-id-2" {
		t.Errorf("TC-084-02: IPC id: want %q, got %q", "stub-id-2", id)
	}
}

func TestTC084_02_VerifyDelete_TamperDetected(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("verify_delete", map[string]any{
		"confirmed":        false,
		"residue_detected": true,
		"residue_summary":  "unexpected residue",
		"deletion_hash":    "abc123",
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	err := client.VerifyDelete("stub-id-2")
	if err == nil {
		t.Fatal("TC-084-02: want ErrTamperDetected, got nil")
	}
	if !errors.Is(err, memoryguard.ErrTamperDetected) {
		t.Errorf("TC-084-02: want errors.Is(err, ErrTamperDetected), got: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tamper") {
		t.Errorf("TC-084-02: error message must contain 'tamper', got: %q", err.Error())
	}
}

// --- MemoryGuardStore unit tests (used by TC-084-01 and TC-084-02) -----------

// testPlan is a simple serialisable type used in store tests.
type testPlan struct {
	GoalID string `json:"goal_id"`
	Goal   string `json:"goal"`
}

func TestMemoryGuardStore_PutGetDelete_Success(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": true, "stored_id": "sid-abc", "flags": nil,
	})
	stub.setResponse("verify_delete", map[string]any{
		"confirmed": true, "residue_detected": false, "deletion_hash": "xyz",
	})

	store := memoryguard.NewMemoryGuardStore[testPlan](
		memoryguard.NewClientWithRunner("/stub/mg", stub),
		"test-agent",
	)

	plan := testPlan{GoalID: "g1", Goal: "do the thing"}
	if err := store.Put("g1", plan); err != nil {
		t.Fatalf("Put: unexpected error: %v", err)
	}

	// stored_id must be held.
	sid, ok := store.StoredID("g1")
	if !ok || sid != "sid-abc" {
		t.Errorf("StoredID: want (sid-abc, true), got (%q, %v)", sid, ok)
	}

	// Get must return the plan.
	got, ok := store.Get("g1")
	if !ok || got.Goal != "do the thing" {
		t.Errorf("Get: want plan, got (%+v, %v)", got, ok)
	}

	// Delete must invoke verify_delete.
	if err := store.Delete("g1"); err != nil {
		t.Fatalf("Delete: unexpected error: %v", err)
	}
	ops := stub.opsInOrder()
	if len(ops) != 2 || ops[0] != "validate_write" || ops[1] != "verify_delete" {
		t.Errorf("ops: want [validate_write, verify_delete], got %v", ops)
	}

	// After Delete, Get returns nothing.
	if _, ok := store.Get("g1"); ok {
		t.Errorf("Get after Delete: want ok=false")
	}
}

func TestMemoryGuardStore_Delete_TamperDetected(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_write", map[string]any{
		"allow": true, "stored_id": "sid-xyz", "flags": nil,
	})
	stub.setResponse("verify_delete", map[string]any{
		"confirmed": false, "residue_detected": true,
		"residue_summary": "injected", "deletion_hash": "deadbeef",
	})

	store := memoryguard.NewMemoryGuardStore[testPlan](
		memoryguard.NewClientWithRunner("/stub/mg", stub),
		"test-agent",
	)

	if err := store.Put("g2", testPlan{GoalID: "g2", Goal: "tamper me"}); err != nil {
		t.Fatalf("Put: unexpected error: %v", err)
	}

	err := store.Delete("g2")
	if err == nil {
		t.Fatal("Delete with tamper: want error, got nil")
	}
	if !errors.Is(err, memoryguard.ErrTamperDetected) {
		t.Errorf("Delete with tamper: want ErrTamperDetected, got: %v", err)
	}

	// Entry removed from in-process index even on tamper.
	if _, ok := store.Get("g2"); ok {
		t.Errorf("Get after tamper Delete: want ok=false (entry evicted)")
	}
}
