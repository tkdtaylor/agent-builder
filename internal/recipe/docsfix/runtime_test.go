// Package docsfix_test provides runtime-integration tests that check the recipe
// against the actual runtime assembler (task 078 gate-existence assertion).
package docsfix_test

import (
	"fmt"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/recipe"
)

// TestTC079_04_RuntimeGateAssertionPassesForDocsFix tests TC-079-04:
// The runtime gate-existence assertion (task 078) passes when recipe="docs-fix".
//
// This test simulates the verifyGateExists call that the assembler makes before
// constructing the supervisor. If the docs-fix gate does not implement
// gate.Blocker or returns false from Blocks(), this test fails.
func TestTC079_04_RuntimeGateAssertionPassesForDocsFix(t *testing.T) {
	// Select the docs-fix recipe
	r, err := recipe.SelectRecipe("docs-fix")
	if err != nil {
		t.Fatalf("SelectRecipe(\"docs-fix\") failed: %v", err)
	}

	// Verify the GateFactory is non-nil
	if r.GateFactory == nil {
		t.Fatal("GateFactory is nil")
	}

	// Call the GateFactory to get the gate
	g := r.GateFactory()
	if g == nil {
		t.Fatal("GateFactory() returned nil — runtime assembler would reject this")
	}

	// The runtime's verifyGateExists check requires the gate to implement Blocker.
	// Check that this gate implements gate.Blocker.

	// Attempt to assert the Blocker interface
	blocker, ok := g.(interface{ Blocks() bool })
	if !ok {
		t.Fatalf("gate type %T does not implement Blocker interface — runtime would reject this", g)
	}

	// Verify Blocks() returns true (not a pass-through gate)
	if !blocker.Blocks() {
		t.Fatal("gate.Blocks() returned false — runtime would reject this gate as a pass-through")
	}

	// If we reach here, the gate-existence assertion passes
}

// TestTC079_04_RunWithDocsFixRecipePassesGateAssertion tests that a minimal
// runtime assembly attempt would pass the gate assertion for docs-fix.
//
// This test does NOT run the full runtime.Run (which would need full config),
// but instead verifies the gate-existence check directly by mirroring the
// verifyGateExists function from runtime/run.go.
func TestTC079_04_RunWithDocsFixRecipePassesGateAssertion(t *testing.T) {
	// Select the docs-fix recipe (must not error)
	r, err := recipe.SelectRecipe("docs-fix")
	if err != nil {
		t.Fatalf("SelectRecipe(\"docs-fix\") failed: %v", err)
	}

	// Verify the gate-existence assertion (mirroring runtime.verifyGateExists)
	// This is the same check that runtime.Run performs before supervisor construction.
	if err := verifyGateExists(r.GateFactory); err != nil {
		t.Fatalf("verifyGateExists failed (runtime would reject docs-fix): %v", err)
	}

	// If we reach here, the gate-existence assertion passes
}

// verifyGateExists mirrors the check from runtime/run.go (task 078).
// It asserts the gate is non-nil and implements gate.Blocker.
func verifyGateExists(gateFactory recipe.GateFactory) error {
	if gateFactory == nil {
		return fmt.Errorf("runtime: gate assembly error: recipe's GateFactory is nil — a Recipe must have a real, blocking gate")
	}

	g := gateFactory()
	if g == nil {
		return fmt.Errorf("runtime: gate assembly error: GateFactory returned nil — gate must be a non-nil, blocking gate")
	}

	// Check if the gate implements the Blocker marker interface
	blocker, ok := g.(interface{ Blocks() bool })
	if !ok {
		return fmt.Errorf("runtime: gate assembly error: gate does not implement the Blocker marker interface — gate must be a real, blocking gate")
	}

	// Verify the gate reports it is blocking (not a pass-through)
	if !blocker.Blocks() {
		return fmt.Errorf("runtime: gate assembly error: gate.Blocks() returned false — gate must be a real, blocking gate")
	}

	return nil
}
