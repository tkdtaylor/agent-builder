package supervisor

import (
	"errors"
	"testing"
)

func TestVersionSet(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must be set")
	}
}

func TestRunNotYetImplemented(t *testing.T) {
	// Phase 0 scaffold: the loop is stubbed. Asserting the sentinel keeps the gate
	// green while making the stub visibly deliberate, not silently passing.
	if err := New().Run(); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("expected ErrNotImplemented, got %v", err)
	}
}
