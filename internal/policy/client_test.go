package policy_test

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/policy"
)

// startFakeServer starts an in-process Unix-socket server. The handler function
// receives the raw decoded request map and returns the JSON to write as a
// newline-delimited response. The server handles one connection at a time.
func startFakeServer(t *testing.T, handler func(req map[string]any) []byte) string {
	t.Helper()
	dir := t.TempDir()
	sockPath := filepath.Join(dir, "policy.sock")
	ln, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // listener closed
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				dec := json.NewDecoder(c)
				var req map[string]any
				if err := dec.Decode(&req); err != nil {
					return
				}
				resp := handler(req)
				resp = append(resp, '\n')
				_, _ = c.Write(resp)
			}(conn)
		}
	}()
	return sockPath
}

// TC-071-01: PolicyClient.Ping succeeds against a fake server.
// TC-071-02: typed types compile (constructor exercised here).
func TestPolicyClientPing(t *testing.T) {
	// TC-071-01
	sockPath := startFakeServer(t, func(req map[string]any) []byte {
		if req["op"] == "ping" {
			b, _ := json.Marshal(map[string]any{"ok": true})
			return b
		}
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": "unknown_op", "message": "unexpected op", "retryable": false}})
		return b
	})

	client := policy.NewClient(sockPath)
	if err := client.Ping(); err != nil {
		t.Fatalf("Ping() err = %v, want nil", err)
	}
	// Second call — connection must not be held open; each call dials fresh.
	if err := client.Ping(); err != nil {
		t.Fatalf("second Ping() err = %v, want nil", err)
	}
}

// TC-071-02: DecideRequest, DecideResponse, Decision, Obligation types compile
// and can be constructed.
func TestPolicyClientTypesCompile(t *testing.T) {
	// Compile-time construction — if these lines build, the types are correct.
	req := policy.DecideRequest{
		Subject:  policy.Subject{Type: "agent", ID: "agent-builder"},
		Action:   policy.Action{Name: "run-task"},
		Resource: policy.Resource{Type: "task", ID: "task-001"},
		Context:  policy.DecideContext{Risk: "low"},
	}
	if req.Subject.ID != "agent-builder" {
		t.Errorf("Subject.ID = %q, want agent-builder", req.Subject.ID)
	}

	// Decision enum constants must exist and be distinct.
	if policy.DecisionAllow == policy.DecisionDeny {
		t.Error("DecisionAllow == DecisionDeny — enum values must be distinct")
	}
	if policy.DecisionAllow == policy.DecisionRequireApproval {
		t.Error("DecisionAllow == DecisionRequireApproval — enum values must be distinct")
	}
	if policy.DecisionDeny == policy.DecisionRequireApproval {
		t.Error("DecisionDeny == DecisionRequireApproval — enum values must be distinct")
	}

	// Obligation struct construction.
	ob := policy.Obligation{Type: "tier_select", Value: "bubblewrap"}
	if ob.Type != "tier_select" {
		t.Errorf("Obligation.Type = %q, want tier_select", ob.Type)
	}

	// DecideResponse with obligations.
	resp := policy.DecideResponse{
		Decision:    policy.DecisionAllow,
		Obligations: []policy.Obligation{ob},
	}
	if len(resp.Obligations) != 1 {
		t.Errorf("len(Obligations) = %d, want 1", len(resp.Obligations))
	}
}

// TC-071-03: Decide returns DecisionAllow with parsed obligations on allow response.
func TestPolicyClientDecideAllow(t *testing.T) {
	allowResp := `{"decision":"allow","context":{"reason":"host is allowlisted","obligations":[{"type":"tier_select","value":"bubblewrap"},{"type":"vault_injection_floor","value":"proxy"},{"type":"audit_emit","value":true}]}}`
	sockPath := startFakeServer(t, func(req map[string]any) []byte {
		return []byte(allowResp)
	})

	client := policy.NewClient(sockPath)
	req := policy.DecideRequest{
		Subject:  policy.Subject{Type: "agent", ID: "agent-builder"},
		Action:   policy.Action{Name: "run-task"},
		Resource: policy.Resource{Type: "task", ID: "task-001"},
		Context:  policy.DecideContext{Risk: "low"},
	}
	result, err := client.Decide(req)
	if err != nil {
		t.Fatalf("Decide() err = %v, want nil", err)
	}
	if result.Decision != policy.DecisionAllow {
		t.Errorf("Decision = %q, want %q", result.Decision, policy.DecisionAllow)
	}
	if len(result.Obligations) != 3 {
		t.Errorf("len(Obligations) = %d, want 3", len(result.Obligations))
	}
	// Check specific obligations.
	found := map[string]bool{}
	for _, ob := range result.Obligations {
		found[ob.Type] = true
	}
	for _, want := range []string{"tier_select", "vault_injection_floor", "audit_emit"} {
		if !found[want] {
			t.Errorf("obligation %q not found in result", want)
		}
	}
	// Check tier_select value.
	for _, ob := range result.Obligations {
		if ob.Type == "tier_select" {
			v, ok := ob.Value.(string)
			if !ok {
				t.Errorf("tier_select Value type = %T, want string", ob.Value)
			} else if v != "bubblewrap" {
				t.Errorf("tier_select Value = %q, want bubblewrap", v)
			}
		}
		if ob.Type == "vault_injection_floor" {
			v, ok := ob.Value.(string)
			if !ok {
				t.Errorf("vault_injection_floor Value type = %T, want string", ob.Value)
			} else if v != "proxy" {
				t.Errorf("vault_injection_floor Value = %q, want proxy", v)
			}
		}
	}
}

// TC-071-04: Decide returns DecisionDeny with empty obligations on deny response.
func TestPolicyClientDecideDeny(t *testing.T) {
	denyResp := `{"decision":"deny","context":{"reason":"host not in allowlist","obligations":[]}}`
	sockPath := startFakeServer(t, func(req map[string]any) []byte {
		return []byte(denyResp)
	})

	client := policy.NewClient(sockPath)
	result, err := client.Decide(policy.DecideRequest{
		Subject: policy.Subject{Type: "agent", ID: "agent-builder"},
		Action:  policy.Action{Name: "run-task"},
	})
	if err != nil {
		t.Fatalf("Decide() err = %v, want nil", err)
	}
	if result.Decision != policy.DecisionDeny {
		t.Errorf("Decision = %q, want %q", result.Decision, policy.DecisionDeny)
	}
	if len(result.Obligations) != 0 {
		t.Errorf("len(Obligations) = %d, want 0", len(result.Obligations))
	}
}

// TC-071-05: Fail-closed — all six sub-cases must produce DecisionDeny (never DecisionAllow).
func TestPolicyClientFailClosed(t *testing.T) {
	makeReq := func() policy.DecideRequest {
		return policy.DecideRequest{
			Subject: policy.Subject{Type: "agent", ID: "agent-builder"},
			Action:  policy.Action{Name: "run-task"},
		}
	}

	// Sub-case A: unknown future decision value.
	t.Run("A_unknown_decision_string", func(t *testing.T) {
		sockPath := startFakeServer(t, func(req map[string]any) []byte {
			return []byte(`{"decision":"unknown_future_value","context":{}}`)
		})
		result, _ := policy.NewClient(sockPath).Decide(makeReq())
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow for unknown decision string")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for unknown string, got %q", result.Decision)
		}
	})

	// Sub-case B: server closes connection without responding.
	t.Run("B_connection_closed_without_response", func(t *testing.T) {
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "policy.sock")
		ln, err := net.Listen("unix", sockPath)
		if err != nil {
			t.Fatalf("listen: %v", err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		go func() {
			for {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				// Close immediately without sending any response.
				_ = conn.Close()
			}
		}()
		result, _ := policy.NewClient(sockPath).Decide(makeReq())
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow on empty response")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for closed connection, got %q", result.Decision)
		}
	})

	// Sub-case C: malformed JSON.
	t.Run("C_malformed_json", func(t *testing.T) {
		sockPath := startFakeServer(t, func(req map[string]any) []byte {
			return []byte(`{not json`)
		})
		result, _ := policy.NewClient(sockPath).Decide(makeReq())
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow on malformed JSON")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for malformed JSON, got %q", result.Decision)
		}
	})

	// Sub-case D: no server at socket path (dial failure).
	t.Run("D_dial_failure", func(t *testing.T) {
		dir := t.TempDir()
		sockPath := filepath.Join(dir, "no-server.sock")
		// Ensure the socket does not exist.
		_ = os.Remove(sockPath)
		result, err := policy.NewClient(sockPath).Decide(makeReq())
		if err == nil {
			t.Error("expected non-nil error on dial failure")
		}
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow on dial failure")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for dial failure, got %q", result.Decision)
		}
	})

	// Sub-case E: error-shaped response.
	t.Run("E_error_shaped_response", func(t *testing.T) {
		sockPath := startFakeServer(t, func(req map[string]any) []byte {
			return []byte(`{"error":{"code":"bad_request","message":"missing field","retryable":false}}`)
		})
		result, _ := policy.NewClient(sockPath).Decide(makeReq())
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow on error response")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for error response, got %q", result.Decision)
		}
	})

	// Sub-case F: ping-shaped response (wrong shape for decide).
	t.Run("F_ping_shaped_response", func(t *testing.T) {
		sockPath := startFakeServer(t, func(req map[string]any) []byte {
			return []byte(`{"ok":true}`)
		})
		result, _ := policy.NewClient(sockPath).Decide(makeReq())
		if result.Decision == policy.DecisionAllow {
			t.Error("fail-closed violated: got DecisionAllow on ping-shaped response")
		}
		if result.Decision != policy.DecisionDeny {
			t.Errorf("expected DecisionDeny for ping-shaped response, got %q", result.Decision)
		}
	})
}

// TC-071-06: leaf check is exercised by the harness command (go list -deps).
// The in-process assertion here is a compile-time guard: the import list of this
// file and client.go must only use standard library + internal/policy.
// The actual leaf check is the shell command in the verification plan.
func TestPolicyPackageIsLeaf(t *testing.T) {
	// This test just verifies the package builds without referencing anything
	// from other internal packages. The real assertion is the shell command:
	//   go list -deps ./internal/policy/... | grep 'agent-builder/internal/' && echo FAIL || echo PASS-leaf
	// which is run in the verification plan.
	client := policy.NewClient("/tmp/unused.sock")
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}
