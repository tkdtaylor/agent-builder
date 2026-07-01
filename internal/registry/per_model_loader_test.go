package registry

import (
	"testing"
)

// setEnvBlock sets AGENT_BUILDER_REGISTRY_<ID>_<FIELD> vars for an entry via
// t.Setenv (auto-restored after the test). SECRET_REF is set only when
// secretRef is non-empty, so callers can leave it entirely unset by passing "".
func setEnvBlock(t *testing.T, id, endpoint, secretRef, model, tier, cost string) {
	t.Helper()
	prefix := "AGENT_BUILDER_REGISTRY_" + envSafeID(id) + "_"
	t.Setenv(prefix+"ENABLED", "true")
	t.Setenv(prefix+"ENDPOINT", endpoint)
	if secretRef != "" {
		t.Setenv(prefix+"SECRET_REF", secretRef)
	}
	t.Setenv(prefix+"MODEL", model)
	t.Setenv(prefix+"CAPABILITY_TIER", tier)
	t.Setenv(prefix+"COST_WEIGHT", cost)
}

// findEntry returns the entry with the given ID from a LoadFromEnv result, or
// nil if absent.
func findEntry(entries []RegistryEntry, id string) *RegistryEntry {
	for i := range entries {
		if entries[i].ID == id {
			return &entries[i]
		}
	}
	return nil
}

// TC-145-01: TestLoadFromEnvPerModelClaudeEntries — env for claude-haiku /
// claude-sonnet / claude-opus each loads with the expected
// harness/tier/cost/ModelID (ADR 061 §Decision).
func TestLoadFromEnvPerModelClaudeEntries(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		model     string
		tier      string
		cost      string
		wantTier  int
		wantCost  int
		wantModel string
	}{
		{
			name:      "claude-haiku",
			id:        "claude-haiku",
			model:     "claude-haiku-4-5-20251001",
			tier:      "1",
			cost:      "1",
			wantTier:  1,
			wantCost:  1,
			wantModel: "claude-haiku-4-5-20251001",
		},
		{
			name:      "claude-sonnet",
			id:        "claude-sonnet",
			model:     "claude-sonnet-5",
			tier:      "2",
			cost:      "5",
			wantTier:  2,
			wantCost:  5,
			wantModel: "claude-sonnet-5",
		},
		{
			name:      "claude-opus",
			id:        "claude-opus",
			model:     "claude-opus-4-8",
			tier:      "3",
			cost:      "10",
			wantTier:  3,
			wantCost:  10,
			wantModel: "claude-opus-4-8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnvBlock(t, tt.id, "https://api.anthropic.com", "claude-oauth-token", tt.model, tt.tier, tt.cost)

			entries, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("LoadFromEnv() failed: %v", err)
			}

			found := findEntry(entries, tt.id)
			if found == nil {
				t.Fatalf("%s entry not found in LoadFromEnv result", tt.id)
			}
			if found.Harness != HarnessClaudeCLI {
				t.Errorf("Harness: expected %v, got %v", HarnessClaudeCLI, found.Harness)
			}
			if found.CapabilityTier != tt.wantTier {
				t.Errorf("CapabilityTier: expected %d, got %d", tt.wantTier, found.CapabilityTier)
			}
			if found.CostWeight != tt.wantCost {
				t.Errorf("CostWeight: expected %d, got %d", tt.wantCost, found.CostWeight)
			}
			if found.ModelID != tt.wantModel {
				t.Errorf("ModelID: expected %q, got %q", tt.wantModel, found.ModelID)
			}
			if found.SecretRef != "claude-oauth-token" {
				t.Errorf("SecretRef: expected %q, got %q", "claude-oauth-token", found.SecretRef)
			}
		})
	}
}

// TC-145-03: TestLoadFromEnvAgyModelLevels — agy-gemini-flash/pro load with
// HarnessAntigravityCLI and correct tiers; empty SECRET_REF is accepted
// (local/subscription entries, per localHarnessEntries).
func TestLoadFromEnvAgyModelLevels(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		model     string
		tier      string
		cost      string
		wantTier  int
		wantCost  int
		wantModel string
	}{
		{
			name:      "agy-gemini-flash",
			id:        "agy-gemini-flash",
			model:     "Gemini 3.5 Flash (High)",
			tier:      "1",
			cost:      "1",
			wantTier:  1,
			wantCost:  1,
			wantModel: "Gemini 3.5 Flash (High)",
		},
		{
			name:      "agy-gemini-pro",
			id:        "agy-gemini-pro",
			model:     "Gemini 3 Pro (High)",
			tier:      "3",
			cost:      "8",
			wantTier:  3,
			wantCost:  8,
			wantModel: "Gemini 3 Pro (High)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No SECRET_REF set — local/subscription entries authenticate via
			// the agy CLI's own cached OAuth login.
			setEnvBlock(t, tt.id, "https://agy.google.com", "", tt.model, tt.tier, tt.cost)

			entries, err := LoadFromEnv()
			if err != nil {
				t.Fatalf("LoadFromEnv() failed: %v", err)
			}

			found := findEntry(entries, tt.id)
			if found == nil {
				t.Fatalf("%s entry not found in LoadFromEnv result", tt.id)
			}
			if found.Harness != HarnessAntigravityCLI {
				t.Errorf("Harness: expected %v, got %v", HarnessAntigravityCLI, found.Harness)
			}
			if found.CapabilityTier != tt.wantTier {
				t.Errorf("CapabilityTier: expected %d, got %d", tt.wantTier, found.CapabilityTier)
			}
			if found.CostWeight != tt.wantCost {
				t.Errorf("CostWeight: expected %d, got %d", tt.wantCost, found.CostWeight)
			}
			if found.ModelID != tt.wantModel {
				t.Errorf("ModelID: expected %q, got %q", tt.wantModel, found.ModelID)
			}
			// Empty SecretRef must be accepted without error (local entry).
			if found.SecretRef != "" {
				t.Errorf("SecretRef: expected empty, got %q", found.SecretRef)
			}
		})
	}
}
