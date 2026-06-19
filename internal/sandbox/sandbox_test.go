package sandbox

import "testing"

// TC-066-04: sandbox.Request has a Wiring field of type RunWiring with the three
// vault wiring fields; the sandbox.Runner interface is unchanged; constructing a
// Request with a Wiring literal compiles. These are compile-time guarantees —
// the test exists so the contract is asserted, not just implied by other code.

// runnerContract is a compile-time assertion that the Runner interface still has
// exactly the Run(Request) (Result, int, error) shape. If the signature changes,
// this fails to compile.
var _ Runner = runnerShape(nil)

type runnerShape func(Request) (Result, int, error)

func (f runnerShape) Run(r Request) (Result, int, error) { return f(r) }

func TestRequestHasWiringField(t *testing.T) {
	// Construct a Request with a populated RunWiring literal. If the field or any
	// of its sub-fields are missing or mistyped, this does not compile.
	req := Request{
		Command: []string{"sh", "-c", "true"},
		Wiring: RunWiring{
			VaultSocket:   "/tmp/vault.sock",
			SecretRefs:    []string{"handle-a", "handle-b"},
			InjectionMode: "proxy",
		},
	}

	if req.Wiring.VaultSocket != "/tmp/vault.sock" {
		t.Fatalf("Wiring.VaultSocket = %q, want /tmp/vault.sock", req.Wiring.VaultSocket)
	}
	if req.Wiring.InjectionMode != "proxy" {
		t.Fatalf("Wiring.InjectionMode = %q, want proxy", req.Wiring.InjectionMode)
	}
	if len(req.Wiring.SecretRefs) != 2 {
		t.Fatalf("Wiring.SecretRefs len = %d, want 2", len(req.Wiring.SecretRefs))
	}
}

func TestZeroValueWiringIsEmpty(t *testing.T) {
	// The zero-value Request must have empty wiring: ADR 035 deferred default.
	var req Request
	if req.Wiring.VaultSocket != "" {
		t.Errorf("zero Wiring.VaultSocket = %q, want empty", req.Wiring.VaultSocket)
	}
	if req.Wiring.InjectionMode != "" {
		t.Errorf("zero Wiring.InjectionMode = %q, want empty", req.Wiring.InjectionMode)
	}
	if req.Wiring.SecretRefs != nil {
		t.Errorf("zero Wiring.SecretRefs = %v, want nil", req.Wiring.SecretRefs)
	}
}

// TestCopyRequestDeepCopiesSecretRefs guards that FakeRunner's recorded requests
// snapshot the SecretRefs slice rather than aliasing the caller's backing array.
func TestCopyRequestDeepCopiesSecretRefs(t *testing.T) {
	refs := []string{"handle-a"}
	req := Request{
		Command: []string{"sh", "-c", "true"},
		Wiring:  RunWiring{SecretRefs: refs},
	}
	fake := NewFakeRunner(FakeResponse{ExitCode: 0})
	if _, _, err := fake.Run(req); err != nil {
		t.Fatalf("fake.Run err = %v", err)
	}
	// Mutate the original backing array; the recorded request must not change.
	refs[0] = "MUTATED"
	recorded := fake.Requests()
	if len(recorded) != 1 {
		t.Fatalf("recorded requests = %d, want 1", len(recorded))
	}
	if recorded[0].Wiring.SecretRefs[0] != "handle-a" {
		t.Fatalf("recorded SecretRefs[0] = %q, want handle-a (deep copy expected)", recorded[0].Wiring.SecretRefs[0])
	}
}
