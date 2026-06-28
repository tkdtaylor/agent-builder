package registry

import (
	"os"
	"testing"
	"time"
)

// TC-087-01: RegistryEntry struct compiles with all fields; zero value is valid
func TestRegistryEntryStructAndTypes(t *testing.T) {
	// Construct a RegistryEntry value with all fields populated
	entry := RegistryEntry{
		ID:             "claude-oauth",
		Harness:        HarnessClaudeCLI,
		CapabilityTier: 3,
		CostWeight:     10,
		ModelID:        "claude-opus-4-5",
		Endpoint:       "https://api.anthropic.com",
		SecretRef:      "claude-oauth-token",
		Budget:         QuotaBudget{Limit: 100, Window: 5 * time.Hour},
		Usage:          0,
		Availability:   Availability{Status: AvailStatusAvailable},
	}

	// Verify all fields are set correctly
	if entry.ID != "claude-oauth" {
		t.Errorf("ID: expected %q, got %q", "claude-oauth", entry.ID)
	}
	if entry.Harness != HarnessClaudeCLI {
		t.Errorf("Harness: expected %v, got %v", HarnessClaudeCLI, entry.Harness)
	}
	if entry.CapabilityTier != 3 {
		t.Errorf("CapabilityTier: expected 3, got %d", entry.CapabilityTier)
	}
	if entry.CostWeight != 10 {
		t.Errorf("CostWeight: expected 10, got %d", entry.CostWeight)
	}
	if entry.Budget.Limit != 100 {
		t.Errorf("Budget.Limit: expected 100, got %d", entry.Budget.Limit)
	}
	if entry.Budget.Window != 5*time.Hour {
		t.Errorf("Budget.Window: expected 5h, got %v", entry.Budget.Window)
	}
	if entry.Availability.Status != AvailStatusAvailable {
		t.Errorf("Availability.Status: expected %v, got %v", AvailStatusAvailable, entry.Availability.Status)
	}

	// Verify zero-value RegistryEntry is valid
	zeroEntry := RegistryEntry{}
	if zeroEntry.ID != "" {
		t.Errorf("Zero-value ID should be empty, got %q", zeroEntry.ID)
	}
	if zeroEntry.Budget.Limit != 0 {
		t.Errorf("Zero-value Budget.Limit should be 0, got %d", zeroEntry.Budget.Limit)
	}

	// Verify HarnessDriver, QuotaBudget, Availability, AvailStatus compile
	_ = HarnessClaudeCLI
	_ = HarnessCodexCLI
	_ = HarnessGeminiCLI
	_ = QuotaBudget{Limit: 0, Window: time.Second}
	_ = Availability{Status: AvailStatusAvailable}
	_ = AvailStatusExhausted
}

// TC-087-02: HarnessDriver discriminator covers all ADR-043 harnesses
func TestHarnessDriverConstants(t *testing.T) {
	tests := []struct {
		driver   HarnessDriver
		expected string
	}{
		{HarnessClaudeCLI, "claude-cli"},
		{HarnessCodexCLI, "codex-cli"},
		{HarnessGeminiCLI, "gemini-cli"},
	}

	// Verify all three constants are distinct
	if HarnessClaudeCLI == HarnessCodexCLI || HarnessClaudeCLI == HarnessGeminiCLI || HarnessCodexCLI == HarnessGeminiCLI {
		t.Error("HarnessDriver constants must be distinct")
	}

	// Verify String() returns human-readable names
	for _, tt := range tests {
		got := tt.driver.String()
		if got != tt.expected {
			t.Errorf("String() for %v: expected %q, got %q", tt.driver, tt.expected, got)
		}
	}
}

// TC-087-03: Env-driven entry construction via LoadFromEnv
func TestLoadFromEnv(t *testing.T) {
	// Save original env vars
	origEnabled := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED")
	origEndpoint := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT")
	origSecretRef := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF")
	origModel := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL")
	origTier := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER")
	origCost := os.Getenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT")

	defer func() {
		if origEnabled != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED", origEnabled)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED")
		}
		if origEndpoint != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT", origEndpoint)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT")
		}
		if origSecretRef != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF", origSecretRef)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF")
		}
		if origModel != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL", origModel)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL")
		}
		if origTier != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER", origTier)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER")
		}
		if origCost != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT", origCost)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT")
		}
	}()

	t.Run("LoadFromEnv with full config", func(t *testing.T) {
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED", "true")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT", "https://api.anthropic.com")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF", "claude-oauth-token")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL", "claude-opus-4-5")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER", "3")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT", "10")

		entries, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("LoadFromEnv failed: %v", err)
		}

		// Find the claude-oauth entry
		var found *RegistryEntry
		for i := range entries {
			if entries[i].ID == "claude-oauth" {
				found = &entries[i]
				break
			}
		}

		if found == nil {
			t.Fatal("claude-oauth entry not found in LoadFromEnv result")
		}

		if found.ID != "claude-oauth" {
			t.Errorf("ID: expected %q, got %q", "claude-oauth", found.ID)
		}
		if found.Harness != HarnessClaudeCLI {
			t.Errorf("Harness: expected %v, got %v", HarnessClaudeCLI, found.Harness)
		}
		if found.CapabilityTier != 3 {
			t.Errorf("CapabilityTier: expected 3, got %d", found.CapabilityTier)
		}
		if found.CostWeight != 10 {
			t.Errorf("CostWeight: expected 10, got %d", found.CostWeight)
		}
		if found.SecretRef != "claude-oauth-token" {
			t.Errorf("SecretRef: expected %q, got %q", "claude-oauth-token", found.SecretRef)
		}
	})

	t.Run("LoadFromEnv with ENABLED=false excludes entry", func(t *testing.T) {
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED", "false")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT", "https://api.anthropic.com")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF", "claude-oauth-token")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL", "claude-opus-4-5")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER", "3")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT", "10")

		entries, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("LoadFromEnv failed: %v", err)
		}

		for _, e := range entries {
			if e.ID == "claude-oauth" {
				t.Error("claude-oauth should be excluded when ENABLED=false")
			}
		}
	})

	t.Run("LoadFromEnv missing ENDPOINT returns descriptive error", func(t *testing.T) {
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED", "true")
		_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF", "claude-oauth-token")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL", "claude-opus-4-5")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER", "3")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT", "10")

		_, err := LoadFromEnv()
		if err == nil {
			t.Fatal("expected error for missing ENDPOINT")
		}
		if err.Error() != `missing required field ENDPOINT for enabled entry "claude-oauth"` {
			t.Errorf("expected missing ENDPOINT error, got: %v", err)
		}
	})

	t.Run("LoadFromEnv non-integer CAPABILITY_TIER returns descriptive error", func(t *testing.T) {
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED", "true")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT", "https://api.anthropic.com")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF", "claude-oauth-token")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL", "claude-opus-4-5")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER", "not-a-number")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT", "10")

		_, err := LoadFromEnv()
		if err == nil {
			t.Fatal("expected error for non-integer CAPABILITY_TIER")
		}
		if err.Error() != `invalid CAPABILITY_TIER for entry "claude-oauth": "not-a-number" is not an integer` {
			t.Errorf("expected non-integer CAPABILITY_TIER error, got: %v", err)
		}
	})
}

// TC-087-04: RegisterEntry + LookupEntry round-trip
func TestRegisterLookupRoundTrip(t *testing.T) {
	catalog := NewCatalog()

	entry := RegistryEntry{
		ID:             "local-qwen",
		Harness:        HarnessClaudeCLI,
		CapabilityTier: 2,
		CostWeight:     1,
		ModelID:        "qwen-7b",
		Endpoint:       "http://localhost:5000",
		SecretRef:      "",
		Budget:         QuotaBudget{Limit: 0, Window: 0},
		Usage:          0,
		Availability:   Availability{Status: AvailStatusAvailable},
	}

	catalog.RegisterEntry(entry)

	// LookupEntry("local-qwen") should return the registered entry
	retrieved, found := catalog.LookupEntry("local-qwen")
	if !found {
		t.Fatal("LookupEntry should find registered entry")
	}
	if retrieved.ID != entry.ID {
		t.Errorf("ID: expected %q, got %q", entry.ID, retrieved.ID)
	}
	if retrieved.Harness != entry.Harness {
		t.Errorf("Harness: expected %v, got %v", entry.Harness, retrieved.Harness)
	}
	if retrieved.CapabilityTier != entry.CapabilityTier {
		t.Errorf("CapabilityTier: expected %d, got %d", entry.CapabilityTier, retrieved.CapabilityTier)
	}

	// LookupEntry("unknown") should return false
	_, found = catalog.LookupEntry("unknown")
	if found {
		t.Error("LookupEntry should not find non-existent entry")
	}

	// LookupEntry("") should return false
	_, found = catalog.LookupEntry("")
	if found {
		t.Error("LookupEntry should not find empty string")
	}
}

// TC-087-05: Duplicate RegisterEntry panics or errors loudly
func TestDuplicateRegisterEntryPanics(t *testing.T) {
	catalog := NewCatalog()

	entry := RegistryEntry{
		ID:      "test-entry",
		Harness: HarnessClaudeCLI,
	}

	catalog.RegisterEntry(entry)

	// Second RegisterEntry with same ID should panic
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate RegisterEntry")
		} else {
			panicMsg := r.(string)
			if panicMsg != "duplicate registry entry ID: test-entry" {
				t.Errorf("unexpected panic message: %v", panicMsg)
			}
		}
	}()

	catalog.RegisterEntry(entry)
}

// TC-087-06: ListEntries returns entries in stable, deterministic order
func TestListEntriesStableOrder(t *testing.T) {
	catalog := NewCatalog()

	entries := []RegistryEntry{
		{ID: "entry-a", Harness: HarnessClaudeCLI},
		{ID: "entry-b", Harness: HarnessCodexCLI},
		{ID: "entry-c", Harness: HarnessGeminiCLI},
	}

	for _, e := range entries {
		catalog.RegisterEntry(e)
	}

	// Call ListEntries twice and verify order is identical
	list1 := catalog.ListEntries()
	list2 := catalog.ListEntries()

	if len(list1) != len(list2) {
		t.Errorf("ListEntries length mismatch: %d vs %d", len(list1), len(list2))
	}

	for i := range list1 {
		if list1[i].ID != list2[i].ID {
			t.Errorf("Order mismatch at index %d: %q vs %q", i, list1[i].ID, list2[i].ID)
		}
	}

	// Verify order matches insertion order
	for i, expected := range entries {
		if list1[i].ID != expected.ID {
			t.Errorf("Order mismatch at index %d: expected %q, got %q", i, expected.ID, list1[i].ID)
		}
	}
}
