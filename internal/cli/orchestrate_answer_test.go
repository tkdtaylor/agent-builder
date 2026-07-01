package cli

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// Compile-time assertion (TC-140-02): cliAnswerer satisfies orchestrator.Answerer.
var _ orchestrator.Answerer = cliAnswerer{}

// Stub seams for testing goalAnalyzerFromEnv.
type stubSeams struct{}

func (s *stubSeams) Resolve(ctx context.Context, spec router.RoutingSpec) (registry.RegistryEntry, error) {
	return registry.RegistryEntry{}, nil
}

func (s *stubSeams) Invoke(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error) {
	return `{"kind": "answer", "complexity": "simple"}`, nil
}

// TC-140-01: the AGENT_BUILDER_GOAL_ANALYSIS gate — enabling values yield a heuristic
// analyzer; empty/false yields nil (default-off, coding-only).
// REQ-142-03: the "llm" value yields an LLMGoalAnalyzer when seams are available.
func TestGoalAnalyzerFromEnv(t *testing.T) {
	cases := []struct {
		val           string
		wantNil       bool
		wantLLM       bool
		wantHeuristic bool
		provideSeams  bool
	}{
		{"", true, false, false, false},
		{"false", true, false, false, false},
		{"0", true, false, false, false},
		{"true", false, false, true, false},
		{"1", false, false, true, false},
		{"yes", false, false, true, false},
		{"heuristic", false, false, true, false},
		{"ON", false, false, true, false},
		{"llm", false, true, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.val, func(t *testing.T) {
			var resolver planner.ExecutorResolver
			var invoke planner.Invoker

			if tc.provideSeams {
				// For llm case, provide stub seams.
				stubs := &stubSeams{}
				resolver = stubs
				invoke = stubs.Invoke
			}

			analyzer := goalAnalyzerFromEnv(
				func(string) string { return tc.val },
				resolver, invoke,
			)

			if tc.wantNil && analyzer != nil {
				t.Errorf("goalAnalyzerFromEnv(%q) = %T, want nil", tc.val, analyzer)
			}
			if tc.wantHeuristic && analyzer != nil {
				if _, ok := analyzer.(*orchestrator.HeuristicGoalAnalyzer); !ok {
					t.Errorf("goalAnalyzerFromEnv(%q) = %T, want *HeuristicGoalAnalyzer", tc.val, analyzer)
				}
			}
			if tc.wantLLM && analyzer != nil {
				if _, ok := analyzer.(*planner.LLMGoalAnalyzer); !ok {
					t.Errorf("goalAnalyzerFromEnv(%q) = %T, want *LLMGoalAnalyzer", tc.val, analyzer)
				}
			}
		})
	}
}

// tierCatalog builds an in-memory brain catalog with one available entry per tier
// (1/2/3), so a Select at a given MinCapability picks the cheapest entry at or above
// it. Costs increase with tier so the router returns the entry whose tier == the
// floor (its cheapest eligible), letting the test read the floor back off the ID.
func tierCatalog() *registry.Catalog {
	cat := registry.NewCatalog()
	for _, e := range []registry.RegistryEntry{
		{ID: "tier1", CapabilityTier: 1, CostWeight: 1},
		{ID: "tier2", CapabilityTier: 2, CostWeight: 5},
		{ID: "tier3", CapabilityTier: 3, CostWeight: 10},
	} {
		e.Availability = registry.Availability{Status: registry.AvailStatusAvailable}
		cat.RegisterEntry(e)
	}
	return cat
}

// TC-146-02: the tier the analyzer emits reaches RoutingSpec.MinCapability on the
// live answer path. The producer is answerMinCapability (the single tier→MinCapability
// resolution inside cliAnswerer.Answer); the consumer is router.Select(RoutingSpec{
// MinCapability: ...}). This drives the exact producer function and the exact
// consumer call cliAnswerer.Answer uses — not a hand-set field — and reads the
// selected entry's tier back to prove the floor took effect.
func TestEmittedTierReachesRoutingSpec(t *testing.T) {
	cases := []struct {
		name         string
		tier         int
		wantMinCap   int
		wantSelected string
	}{
		{"tier 1", 1, 1, "tier1"},
		{"tier 2", 2, 2, "tier2"},
		{"tier 3", 3, 3, "tier3"},
		{"unset (0) falls back to default floor", 0, answerDefaultMinCapability, "tier1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Producer: the same resolution cliAnswerer.Answer performs.
			gotMinCap := answerMinCapability(tc.tier)
			if gotMinCap != tc.wantMinCap {
				t.Fatalf("answerMinCapability(%d) = %d, want %d", tc.tier, gotMinCap, tc.wantMinCap)
			}
			// Consumer: the same RoutingSpec build + Select cliAnswerer.Answer performs.
			spec := router.RoutingSpec{MinCapability: gotMinCap}
			entry, err := router.New(tierCatalog()).Select(spec)
			if err != nil {
				t.Fatalf("Select(MinCapability=%d) error = %v", gotMinCap, err)
			}
			if entry.CapabilityTier < gotMinCap {
				t.Errorf("selected entry tier %d < floor %d — MinCapability floor not honored",
					entry.CapabilityTier, gotMinCap)
			}
			if entry.ID != tc.wantSelected {
				t.Errorf("selected %q, want %q (floor %d)", entry.ID, tc.wantSelected, gotMinCap)
			}
		})
	}
}

// TC-146-03: the emitted tier feeds MinCapability independently of SensitivityHint.
// Model tier (how strong) and sensitivity (how private) are orthogonal (ADR 061):
// the same tier yields the same MinCapability floor at any sensitivity, so the tier's
// effect on selection does not change when the sensitivity axis varies.
func TestTierMinCapabilityIndependentOfSensitivity(t *testing.T) {
	const tier = 2
	minCap := answerMinCapability(tier)
	for _, s := range []router.Sensitivity{
		router.SensitivityNone,
		router.SensitivitySensitive,
	} {
		spec := router.RoutingSpec{MinCapability: minCap, SensitivityHint: s}
		entry, err := router.New(tierCatalog()).Select(spec)
		if err != nil {
			t.Fatalf("Select(sensitivity=%v) error = %v", s, err)
		}
		if entry.CapabilityTier < minCap {
			t.Errorf("sensitivity %v: selected tier %d < floor %d — tier floor must not depend on sensitivity",
				s, entry.CapabilityTier, minCap)
		}
	}
}
