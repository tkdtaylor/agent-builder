package router

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// TC-145-02: TestRouterSelectsModelByTier — a catalog with all three Claude
// per-model entries (haiku/sonnet/opus) enabled and available; Select at each
// MinCapability floor returns the cheapest-sufficient tier (ADR 061 §Decision:
// "model level *is* the capability tier").
func TestRouterSelectsModelByTier(t *testing.T) {
	catalogWithAllTiers := func() *registry.Catalog {
		return catalogOf(
			entry("claude-haiku", 1, 1, 0, "haiku-ref"),
			entry("claude-sonnet", 2, 5, 0, "sonnet-ref"),
			entry("claude-opus", 3, 10, 0, "opus-ref"),
		)
	}

	tests := []struct {
		name          string
		minCapability int
		wantID        string
	}{
		{name: "MinCapability=1 selects haiku (cheapest sufficient)", minCapability: 1, wantID: "claude-haiku"},
		{name: "MinCapability=2 selects sonnet", minCapability: 2, wantID: "claude-sonnet"},
		{name: "MinCapability=3 selects opus", minCapability: 3, wantID: "claude-opus"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(catalogWithAllTiers())
			got := mustSelect(t, r, RoutingSpec{MinCapability: tt.minCapability})
			if got.ID != tt.wantID {
				t.Fatalf("Select(MinCapability:%d): expected %q, got %q", tt.minCapability, tt.wantID, got.ID)
			}
		})
	}
}
