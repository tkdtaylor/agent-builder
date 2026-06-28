package registry

import (
	"os"
	"strings"
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

// TC-091-01: LoadFromEnv with local-entry env vars → HarnessClaudeCLI, SecretRef="", Budget=zero
func TestLoadFromEnv_LocalQwenEntry(t *testing.T) {
	// Set only the local-entry vars (no SECRET_REF — it is optional for local entries)
	keys := []string{
		"AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENABLED",
		"AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENDPOINT",
		"AGENT_BUILDER_REGISTRY_LOCAL_QWEN_MODEL",
		"AGENT_BUILDER_REGISTRY_LOCAL_QWEN_CAPABILITY_TIER",
		"AGENT_BUILDER_REGISTRY_LOCAL_QWEN_COST_WEIGHT",
	}
	origValues := make(map[string]string, len(keys))
	for _, k := range keys {
		origValues[k] = os.Getenv(k)
	}
	defer func() {
		for k, v := range origValues {
			if v != "" {
				_ = os.Setenv(k, v)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}()

	_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENABLED", "true")
	_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_QWEN_ENDPOINT", "http://localhost:8080")
	_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_QWEN_MODEL", "qwen2.5-coder-7b-instruct")
	_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_QWEN_CAPABILITY_TIER", "1")
	_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_QWEN_COST_WEIGHT", "1")
	// SECRET_REF is intentionally NOT set — local entries have no cloud auth

	entries, err := LoadFromEnv()
	if err != nil {
		t.Fatalf("LoadFromEnv() failed: %v", err)
	}

	var found *RegistryEntry
	for i := range entries {
		if entries[i].ID == "local-qwen" {
			found = &entries[i]
			break
		}
	}
	if found == nil {
		t.Fatal("local-qwen entry not found in LoadFromEnv result")
	}

	// TC-091-01: Harness must be HarnessClaudeCLI
	if found.Harness != HarnessClaudeCLI {
		t.Errorf("Harness: expected %v, got %v", HarnessClaudeCLI, found.Harness)
	}
	// TC-091-01: SecretRef must be empty (no cloud auth for local)
	if found.SecretRef != "" {
		t.Errorf("SecretRef: expected empty, got %q", found.SecretRef)
	}
	// TC-091-01: Budget must be zero (unlimited)
	if found.Budget.Limit != 0 {
		t.Errorf("Budget.Limit: expected 0 (unlimited), got %d", found.Budget.Limit)
	}
	if found.Budget.Window != 0 {
		t.Errorf("Budget.Window: expected 0, got %v", found.Budget.Window)
	}

	// TC-091-02: Endpoint is the translation-proxy URL, not the model URL
	if found.Endpoint != "http://localhost:8080" {
		t.Errorf("Endpoint: expected %q, got %q", "http://localhost:8080", found.Endpoint)
	}
	// TC-091-02: Harness is still HarnessClaudeCLI (same harness as cloud, different endpoint)
	if found.Harness != HarnessClaudeCLI {
		t.Errorf("Harness: expected HarnessClaudeCLI for local entry, got %v", found.Harness)
	}
}

// TC-091-02: TranslationProxySeam constant names the LiteLLM / claude-code-router pattern
func TestTranslationProxySeamConstant(t *testing.T) {
	// The constant must be non-empty and name the translation-proxy pattern.
	if TranslationProxySeam == "" {
		t.Error("TranslationProxySeam constant must not be empty")
	}
	// It should reference the LiteLLM or claude-code-router pattern.
	if !strings.Contains(TranslationProxySeam, "litellm") && !strings.Contains(TranslationProxySeam, "claude-code-router") {
		t.Errorf("TranslationProxySeam %q does not reference litellm or claude-code-router", TranslationProxySeam)
	}
}

// TC-091-04: IsUnlimited() returns true when Budget.Limit == 0
func TestRegistryEntry_IsUnlimited(t *testing.T) {
	tests := []struct {
		name    string
		entry   RegistryEntry
		want    bool
	}{
		{
			name:  "zero budget (unlimited)",
			entry: RegistryEntry{Budget: QuotaBudget{Limit: 0}},
			want:  true,
		},
		{
			name:  "local entry (unlimited by design)",
			entry: RegistryEntry{ID: "local-qwen", Harness: HarnessClaudeCLI, SecretRef: "", Budget: QuotaBudget{}},
			want:  true,
		},
		{
			name:  "cloud entry with budget cap",
			entry: RegistryEntry{ID: "claude-oauth", Budget: QuotaBudget{Limit: 100}},
			want:  false,
		},
		{
			name:  "budget limit 1",
			entry: RegistryEntry{Budget: QuotaBudget{Limit: 1}},
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.entry.IsUnlimited()
			if got != tt.want {
				t.Errorf("IsUnlimited() = %v, want %v", got, tt.want)
			}
		})
	}
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

// TC-105-01: HarnessOllamaNative constant has the correct string value and String() returns it
func TestHarnessOllamaNativeConstant(t *testing.T) {
	// Assert HarnessOllamaNative == "ollama-native"
	if HarnessOllamaNative != HarnessDriver("ollama-native") {
		t.Errorf("HarnessOllamaNative: expected %q, got %q", "ollama-native", HarnessOllamaNative)
	}

	// Assert String() returns "ollama-native"
	if HarnessOllamaNative.String() != "ollama-native" {
		t.Errorf("HarnessOllamaNative.String(): expected %q, got %q", "ollama-native", HarnessOllamaNative.String())
	}

	// Assert that the generic HarnessDriver("ollama-native").String() also returns "ollama-native"
	genericDriver := HarnessDriver("ollama-native")
	if genericDriver.String() != "ollama-native" {
		t.Errorf("HarnessDriver(\"ollama-native\").String(): expected %q, got %q", "ollama-native", genericDriver.String())
	}

	// Assert that the three existing constants are still present and unchanged
	if HarnessClaudeCLI != HarnessDriver("claude-cli") {
		t.Errorf("HarnessClaudeCLI regression: expected %q, got %q", "claude-cli", HarnessClaudeCLI)
	}
	if HarnessCodexCLI != HarnessDriver("codex-cli") {
		t.Errorf("HarnessCodexCLI regression: expected %q, got %q", "codex-cli", HarnessCodexCLI)
	}
	if HarnessGeminiCLI != HarnessDriver("gemini-cli") {
		t.Errorf("HarnessGeminiCLI regression: expected %q, got %q", "gemini-cli", HarnessGeminiCLI)
	}

	if HarnessClaudeCLI.String() != "claude-cli" {
		t.Errorf("HarnessClaudeCLI.String() regression: expected %q, got %q", "claude-cli", HarnessClaudeCLI.String())
	}
	if HarnessCodexCLI.String() != "codex-cli" {
		t.Errorf("HarnessCodexCLI.String() regression: expected %q, got %q", "codex-cli", HarnessCodexCLI.String())
	}
	if HarnessGeminiCLI.String() != "gemini-cli" {
		t.Errorf("HarnessGeminiCLI.String() regression: expected %q, got %q", "gemini-cli", HarnessGeminiCLI.String())
	}
}

// TC-105-02: LoadFromEnv parses an ollama-native registry entry correctly
func TestLoadFromEnvOllamaNative(t *testing.T) {
	// Save original env vars
	origEnabled := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED")
	origHarness := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS")
	origEndpoint := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT")
	origModel := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL")
	origTier := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER")
	origCost := os.Getenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT")

	defer func() {
		if origEnabled != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED", origEnabled)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED")
		}
		if origHarness != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS", origHarness)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS")
		}
		if origEndpoint != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT", origEndpoint)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT")
		}
		if origModel != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL", origModel)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL")
		}
		if origTier != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER", origTier)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER")
		}
		if origCost != "" {
			_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT", origCost)
		} else {
			_ = os.Unsetenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT")
		}
	}()

	t.Run("LoadFromEnv with ollama-native entry", func(t *testing.T) {
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENABLED", "true")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_HARNESS", "ollama-native")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_ENDPOINT", "http://localhost:11434")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_MODEL", "qwen3:8b")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_CAPABILITY_TIER", "1")
		_ = os.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_OLLAMA_COST_WEIGHT", "1")

		entries, err := LoadFromEnv()
		if err != nil {
			t.Fatalf("LoadFromEnv failed: %v", err)
		}

		// Find the local-ollama entry
		var found *RegistryEntry
		for i := range entries {
			if entries[i].ID == "local-ollama" {
				found = &entries[i]
				break
			}
		}

		if found == nil {
			t.Fatal("local-ollama entry not found in LoadFromEnv result")
		}

		if found.ID != "local-ollama" {
			t.Errorf("ID: expected %q, got %q", "local-ollama", found.ID)
		}
		if found.Harness != HarnessOllamaNative {
			t.Errorf("Harness: expected %v, got %v", HarnessOllamaNative, found.Harness)
		}
		if found.Endpoint != "http://localhost:11434" {
			t.Errorf("Endpoint: expected %q, got %q", "http://localhost:11434", found.Endpoint)
		}
		if found.ModelID != "qwen3:8b" {
			t.Errorf("ModelID: expected %q, got %q", "qwen3:8b", found.ModelID)
		}
		if found.SecretRef != "" {
			t.Errorf("SecretRef: expected empty string, got %q", found.SecretRef)
		}
		if found.CapabilityTier != 1 {
			t.Errorf("CapabilityTier: expected 1, got %d", found.CapabilityTier)
		}
		if found.CostWeight != 1 {
			t.Errorf("CostWeight: expected 1, got %d", found.CostWeight)
		}
		if !found.IsUnlimited() {
			t.Errorf("IsUnlimited: expected true (Budget.Limit==0), got false")
		}
	})
}
