package agentbuilderworker

import "testing"

// TC-145-04: recipe capability-tier audit — agentbuilderworker authors a new,
// gated coding recipe where a broken commit or a missing gate binding is
// costly (it self-modifies the agent's own recipe surface), so ADR 061 places
// it at the mid capability tier (sonnet-equivalent), one above the mechanical
// base tier. MinCapability must stay 2 (post-audit value; unchanged by this
// task's audit — task 145 §Scope).
func TestAgentBuilderWorkerRoutingSpecMinCapability(t *testing.T) {
	r, err := newAgentBuilderWorkerRecipe()
	if err != nil {
		t.Fatalf("newAgentBuilderWorkerRecipe() failed: %v", err)
	}
	if r.RoutingSpec.MinCapability != 2 {
		t.Errorf("agentbuilderworker RoutingSpec.MinCapability: expected 2 (ADR 061 mid tier — gated coding worker), got %d", r.RoutingSpec.MinCapability)
	}
}
