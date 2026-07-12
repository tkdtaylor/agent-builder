package memoryguard_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/memoryguard"
)

// --- TC-165-01/02: Client.ValidateRead, allow=true --------------------------

func TestValidateRead_TC165_01_SendsCorrectWireRequest(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_read", map[string]any{
		"allow":            true,
		"content_redacted": "[REDACTED]",
		"flags":            nil,
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	contentRedacted, flags, err := client.ValidateRead("goal:abc123", "agent-builder/orchestrator")
	if err != nil {
		t.Fatalf("TC-165-01: ValidateRead: unexpected error: %v", err)
	}

	if len(stub.calls) != 1 {
		t.Fatalf("TC-165-01: want 1 stub call, got %d", len(stub.calls))
	}
	call := stub.calls[0]
	if call.req["op"] != "validate_read" {
		t.Errorf("TC-165-01: IPC op: want %q, got %q", "validate_read", call.req["op"])
	}
	query, _ := call.req["query"].(string)
	if query != "goal:abc123" {
		t.Errorf("TC-165-01: IPC query: want %q, got %q", "goal:abc123", query)
	}
	identity, _ := call.req["identity"].(string)
	if identity != "agent-builder/orchestrator" {
		t.Errorf("TC-165-01: IPC identity: want %q, got %q", "agent-builder/orchestrator", identity)
	}
	// The request must carry exactly these three fields, mirroring validate_write's shape.
	if len(call.req) != 3 {
		t.Errorf("TC-165-01: IPC request field count: want 3, got %d (%v)", len(call.req), call.req)
	}

	if contentRedacted != "[REDACTED]" {
		t.Errorf("TC-165-01: contentRedacted: want %q, got %q", "[REDACTED]", contentRedacted)
	}
	if len(flags) != 0 {
		t.Errorf("TC-165-01: flags: want empty, got %v", flags)
	}
}

func TestValidateRead_TC165_02_AllowTrueReturnsRedactedContent(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_read", map[string]any{
		"allow":            true,
		"content_redacted": "plan text here",
		"flags":            []string{"stale"},
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	contentRedacted, flags, err := client.ValidateRead("goal:abc123", "agent-builder/orchestrator")
	if err != nil {
		t.Fatalf("TC-165-02: ValidateRead: unexpected error: %v", err)
	}
	if contentRedacted != "plan text here" {
		t.Errorf("TC-165-02: contentRedacted: want %q, got %q", "plan text here", contentRedacted)
	}
	if len(flags) != 1 || flags[0] != "stale" {
		t.Errorf("TC-165-02: flags: want [stale], got %v", flags)
	}
}

// --- TC-165-03: Client.ValidateRead, allow=false -----------------------------

func TestValidateRead_TC165_03_DeniedReturnsSentinelWithFlags(t *testing.T) {
	stub := newStubRunner()
	stub.setResponse("validate_read", map[string]any{
		"allow":            false,
		"content_redacted": "",
		"flags":            []string{"policy_violation"},
	})

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	contentRedacted, flags, err := client.ValidateRead("goal:abc123", "agent-builder/orchestrator")
	if err == nil {
		t.Fatal("TC-165-03: want ErrReadGateDenied, got nil")
	}
	if !errors.Is(err, memoryguard.ErrReadGateDenied) {
		t.Errorf("TC-165-03: want errors.Is(err, ErrReadGateDenied), got: %v", err)
	}
	if len(flags) != 1 || flags[0] != "policy_violation" {
		t.Errorf("TC-165-03: flags: want [policy_violation], got %v", flags)
	}
	if contentRedacted != "" {
		t.Errorf("TC-165-03: contentRedacted: want empty, got %q", contentRedacted)
	}
}

// --- TC-165-04: transport error -----------------------------------------------

func TestValidateRead_TC165_04_TransportErrorWrapped(t *testing.T) {
	stub := newStubRunner()
	transportErr := errors.New("exec: binary not found")
	stub.errors["validate_read"] = transportErr

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	_, _, err := client.ValidateRead("goal:abc123", "agent-builder/orchestrator")
	if err == nil {
		t.Fatal("TC-165-04: want error, got nil")
	}
	if !strings.Contains(err.Error(), "validate_read subprocess") {
		t.Errorf("TC-165-04: error message: want to contain %q, got %q", "validate_read subprocess", err.Error())
	}
	if !errors.Is(err, transportErr) {
		t.Errorf("TC-165-04: want errors.Is(err, transportErr) to hold, got: %v", err)
	}
}

// --- TC-165-05: malformed JSON response --------------------------------------

func TestValidateRead_TC165_05_MalformedResponseIsParseError(t *testing.T) {
	stub := newStubRunner()
	stub.responses["validate_read"] = []byte("not json")

	client := memoryguard.NewClientWithRunner("/stub/memory-guard", stub)
	_, _, err := client.ValidateRead("goal:abc123", "agent-builder/orchestrator")
	if err == nil {
		t.Fatal("TC-165-05: want error, got nil")
	}
	if !strings.Contains(err.Error(), "parse validate_read response") {
		t.Errorf("TC-165-05: error message: want to contain %q, got %q", "parse validate_read response", err.Error())
	}
	if !strings.Contains(err.Error(), "not json") {
		t.Errorf("TC-165-05: error message: want to contain raw response %q, got %q", "not json", err.Error())
	}
}
