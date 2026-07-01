package docsfix

import "testing"

// TC-145-04: recipe capability-tier audit — docsfix is a mechanical,
// deterministic markdown-lint fix with no design judgment required, so ADR 061
// places it at the base capability tier (haiku-equivalent). MinCapability must
// stay 1 (post-audit value; unchanged by this task's audit — task 145 §Scope).
func TestDocsFixRoutingSpecMinCapability(t *testing.T) {
	r, err := newDocsFixRecipe()
	if err != nil {
		t.Fatalf("newDocsFixRecipe() failed: %v", err)
	}
	if r.RoutingSpec.MinCapability != 1 {
		t.Errorf("docsfix RoutingSpec.MinCapability: expected 1 (ADR 061 base tier — mechanical docs fix), got %d", r.RoutingSpec.MinCapability)
	}
}
