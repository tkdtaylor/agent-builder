package runtime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
)

func TestConfigFromEnvAcceptsOAuthTokenOnly(t *testing.T) {
	// TC-059-05: ConfigFromEnv accepts OAuth token without API key
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIOAuthEnv:
			return "oauth-token-123"
		case executor.ClaudeCLIAuthEnv:
			return "" // No API key
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if config.ClaudeOAuthToken != "oauth-token-123" {
		t.Fatalf("ClaudeOAuthToken = %q, want %q", config.ClaudeOAuthToken, "oauth-token-123")
	}
	if config.ClaudeToken != "" {
		t.Fatalf("ClaudeToken = %q, want empty", config.ClaudeToken)
	}
}

func TestConfigFromEnvAcceptsAPIKeyOnly(t *testing.T) {
	// TC-059-05: ConfigFromEnv accepts API key without OAuth token
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "api-key-xyz"
		case executor.ClaudeCLIOAuthEnv:
			return "" // No OAuth
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if config.ClaudeToken != "api-key-xyz" {
		t.Fatalf("ClaudeToken = %q, want %q", config.ClaudeToken, "api-key-xyz")
	}
	if config.ClaudeOAuthToken != "" {
		t.Fatalf("ClaudeOAuthToken = %q, want empty", config.ClaudeOAuthToken)
	}
}

func TestConfigFromEnvRejectsNeither(t *testing.T) {
	// TC-059-05: ConfigFromEnv rejects when neither credential is set
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "" // No API key
		case executor.ClaudeCLIOAuthEnv:
			return "" // No OAuth
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	_, err := ConfigFromEnv(getenv)
	if err == nil {
		t.Fatal("ConfigFromEnv() returned nil error, want error")
	}
	if !strings.Contains(err.Error(), executor.ClaudeCLIAuthEnv) {
		t.Fatalf("error doesn't mention %s: %v", executor.ClaudeCLIAuthEnv, err)
	}
	if !strings.Contains(err.Error(), executor.ClaudeCLIOAuthEnv) {
		t.Fatalf("error doesn't mention %s: %v", executor.ClaudeCLIOAuthEnv, err)
	}
}

func TestConfigFromEnvBothCredentialsWired(t *testing.T) {
	// TC-059-05: ConfigFromEnv wires both credentials to Config
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "api-key-abc"
		case executor.ClaudeCLIOAuthEnv:
			return "oauth-def"
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if config.ClaudeToken != "api-key-abc" {
		t.Fatalf("ClaudeToken = %q, want %q", config.ClaudeToken, "api-key-abc")
	}
	if config.ClaudeOAuthToken != "oauth-def" {
		t.Fatalf("ClaudeOAuthToken = %q, want %q", config.ClaudeOAuthToken, "oauth-def")
	}
}

// TC-065-05: ConfigFromEnv reads tokens via SecretSource (regression guard)
func TestConfigFromEnvReadsTokensViaSecretSource(t *testing.T) {
	// This is a pure regression guard: behavior must be unchanged after the
	// SecretSource refactor. The getenv function drives the getenvSecretSource
	// wrapper, so the token values flow through SecretSource internally.
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "sk-env"
		case executor.ClaudeCLIOAuthEnv:
			return "oauth-env"
		case "AGENT_BUILDER_GIT_TOKEN":
			return "gittok"
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	if config.ClaudeToken != "sk-env" {
		t.Fatalf("ClaudeToken = %q, want %q", config.ClaudeToken, "sk-env")
	}
	if config.ClaudeOAuthToken != "oauth-env" {
		t.Fatalf("ClaudeOAuthToken = %q, want %q", config.ClaudeOAuthToken, "oauth-env")
	}
	if config.GitToken != "gittok" {
		t.Fatalf("GitToken = %q, want %q", config.GitToken, "gittok")
	}
}

func TestRunTimeoutParsing(t *testing.T) {
	// Ensure timeout parsing still works correctly
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "api-key"
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "10m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "3"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}

	expectedTimeout := 10 * time.Minute
	if config.RunTimeout != expectedTimeout {
		t.Fatalf("RunTimeout = %v, want %v", config.RunTimeout, expectedTimeout)
	}
}

// TC-077-01: SelectRecipe("coding-agent") returns a non-nil Recipe with all
// required seam fields and a non-zero RoutingSpec.
func TestSelectCodingAgentRecipe(t *testing.T) {
	// Select the coding-agent recipe (registered by init() in this package).
	r, err := recipe.SelectRecipe("coding-agent")
	if err != nil {
		t.Fatalf("SelectRecipe(\"coding-agent\") failed: %v", err)
	}

	// Verify the recipe name is set correctly.
	if r.Name != "coding-agent" {
		t.Errorf("Name = %q, want \"coding-agent\"", r.Name)
	}

	// Verify all required seam factory fields are non-nil (ADR 044, task 077).
	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory is nil")
	}
	if r.GateFactory == nil {
		t.Error("GateFactory is nil")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory is nil")
	}

	// Verify the RoutingSpec is non-zero (has MinCapability set).
	if r.RoutingSpec.MinCapability == 0 {
		t.Error("RoutingSpec.MinCapability is zero")
	}

	// Verify the recipe is included in ListRecipes().
	recipes := recipe.ListRecipes()
	found := false
	for _, name := range recipes {
		if name == "coding-agent" {
			found = true
			break
		}
	}
	if !found {
		t.Error("\"coding-agent\" not found in ListRecipes()")
	}
}

// TC-077-06: ConfigFromEnv defaults RecipeName to "coding-agent" when unset.
func TestConfigFromEnvRecipeNameDefault(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "test-token"
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.RecipeName != "coding-agent" {
		t.Errorf("RecipeName = %q, want \"coding-agent\"", config.RecipeName)
	}
}

// TC-077-06: ConfigFromEnv reads AGENT_BUILDER_RECIPE when set.
func TestConfigFromEnvRecipeNameExplicit(t *testing.T) {
	getenv := func(key string) string {
		switch key {
		case "AGENT_BUILDER_TASK_ROOT":
			return "/tmp/tasks"
		case "AGENT_BUILDER_WORKTREE":
			return "/tmp/work"
		case executor.ClaudeCLIAuthEnv:
			return "test-token"
		case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
			return "containment/execution-box/run.sh"
		case "AGENT_BUILDER_RUN_TIMEOUT":
			return "5m"
		case "AGENT_BUILDER_MAX_ATTEMPTS":
			return "2"
		case "AGENT_BUILDER_PUBLISH_REMOTE":
			return "origin"
		case "AGENT_BUILDER_RECIPE":
			return "test-recipe"
		default:
			return ""
		}
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() error = %v", err)
	}
	if config.RecipeName != "test-recipe" {
		t.Errorf("RecipeName = %q, want \"test-recipe\"", config.RecipeName)
	}
}

// TC-101-04: ConfigFromEnv accepts a local-only registry (no cloud credential in env)
func TestConfigFromEnvAcceptsLocalOnlyRegistry(t *testing.T) {
	// Use t.Setenv to set real environment variables (since registry.LoadFromEnv() uses os.Getenv directly).
	t.Setenv("AGENT_BUILDER_TASK_ROOT", "/tmp/tasks")
	t.Setenv("AGENT_BUILDER_WORKTREE", "/tmp/work")
	t.Setenv("AGENT_BUILDER_EXEC_BOX_LAUNCHER", "containment/execution-box/run.sh")
	t.Setenv("AGENT_BUILDER_RUN_TIMEOUT", "5m")
	t.Setenv("AGENT_BUILDER_MAX_ATTEMPTS", "2")
	t.Setenv("AGENT_BUILDER_PUBLISH_REMOTE", "origin")
	// Local registry entry configuration (using the "local" entry ID which is in knownEntries)
	t.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_ENABLED", "true")
	t.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_ENDPOINT", "http://localhost:8080")
	t.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_MODEL", "qwen2.5-coder:7b")
	t.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_CAPABILITY_TIER", "1")
	t.Setenv("AGENT_BUILDER_REGISTRY_LOCAL_COST_WEIGHT", "1")
	// NO cloud credentials in environment
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	getenv := func(key string) string {
		// Delegate to actual environment (which we've set up with t.Setenv above).
		val, _ := os.LookupEnv(key)
		return val
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() for local-only registry failed: %v", err)
	}

	// Verify that config was successfully created with no cloud credentials
	if config.ClaudeToken != "" {
		t.Fatalf("ClaudeToken should be empty for local-only registry, got %q", config.ClaudeToken)
	}
	if config.ClaudeOAuthToken != "" {
		t.Fatalf("ClaudeOAuthToken should be empty for local-only registry, got %q", config.ClaudeOAuthToken)
	}
}

// TC-101-05: ConfigFromEnv still errors when a cloud entry is configured but no credential is present
func TestConfigFromEnvErrorsOnCloudEntryWithoutCredential(t *testing.T) {
	tests := []struct {
		name        string
		getenvSetup func() func(string) string
	}{
		{
			name: "Cloud entry (claude-oauth) without credential",
			getenvSetup: func() func(string) string {
				return func(key string) string {
					switch key {
					case "AGENT_BUILDER_TASK_ROOT":
						return "/tmp/tasks"
					case "AGENT_BUILDER_WORKTREE":
						return "/tmp/work"
					case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
						return "containment/execution-box/run.sh"
					case "AGENT_BUILDER_RUN_TIMEOUT":
						return "5m"
					case "AGENT_BUILDER_MAX_ATTEMPTS":
						return "2"
					case "AGENT_BUILDER_PUBLISH_REMOTE":
						return "origin"
					// Cloud entry configuration
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENABLED":
						return "true"
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_ENDPOINT":
						return "https://api.anthropic.com"
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_SECRET_REF":
						return "claude-oauth-token"
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_MODEL":
						return "claude-opus-4-5"
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_CAPABILITY_TIER":
						return "3"
					case "AGENT_BUILDER_REGISTRY_CLAUDE_OAUTH_COST_WEIGHT":
						return "10"
					// NO cloud credentials
					case executor.ClaudeCLIAuthEnv:
						return ""
					case executor.ClaudeCLIOAuthEnv:
						return ""
					default:
						return ""
					}
				}
			},
		},
		{
			name: "No registry entries at all (no local, no cloud)",
			getenvSetup: func() func(string) string {
				return func(key string) string {
					switch key {
					case "AGENT_BUILDER_TASK_ROOT":
						return "/tmp/tasks"
					case "AGENT_BUILDER_WORKTREE":
						return "/tmp/work"
					case "AGENT_BUILDER_EXEC_BOX_LAUNCHER":
						return "containment/execution-box/run.sh"
					case "AGENT_BUILDER_RUN_TIMEOUT":
						return "5m"
					case "AGENT_BUILDER_MAX_ATTEMPTS":
						return "2"
					case "AGENT_BUILDER_PUBLISH_REMOTE":
						return "origin"
					// NO registry entries enabled
					// NO cloud credentials
					case executor.ClaudeCLIAuthEnv:
						return ""
					case executor.ClaudeCLIOAuthEnv:
						return ""
					default:
						return ""
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getenv := tt.getenvSetup()
			config, err := ConfigFromEnv(getenv)
			if err == nil {
				t.Fatalf("ConfigFromEnv() should have errored for %s, but got config: %+v", tt.name, config)
			}

			// Verify error message contains both credential names
			errMsg := err.Error()
			if !strings.Contains(errMsg, executor.ClaudeCLIAuthEnv) {
				t.Errorf("error message missing %s: %s", executor.ClaudeCLIAuthEnv, errMsg)
			}
			if !strings.Contains(errMsg, executor.ClaudeCLIOAuthEnv) {
				t.Errorf("error message missing %s: %s", executor.ClaudeCLIOAuthEnv, errMsg)
			}
		})
	}
}

// TC-105-03: buildExecutorForEntry routes HarnessOllamaNative to OllamaNative
func TestBuildExecutorForEntryOllamaNative(t *testing.T) {
	cfg := Config{
		Worktree: t.TempDir(),
	}

	entry := registry.RegistryEntry{
		ID:        "local-ollama",
		Harness:   registry.HarnessOllamaNative,
		Endpoint:  "http://localhost:11434",
		ModelID:   "qwen3:8b",
		SecretRef: "",
	}

	exec, err := buildExecutorForEntry(entry, cfg)
	if err != nil {
		t.Fatalf("buildExecutorForEntry failed: %v", err)
	}

	if exec == nil {
		t.Fatal("returned executor is nil")
	}

	// Type-assert to *executor.OllamaNative
	ollama, ok := exec.(*executor.OllamaNative)
	if !ok {
		t.Fatalf("executor is not *executor.OllamaNative, got %T", exec)
	}

	if ollama == nil {
		t.Fatal("type-asserted OllamaNative is nil")
	}
}

// TC-105-04: buildExecutorForEntry still errors on an unknown harness driver
func TestBuildExecutorForEntryUnknownHarness(t *testing.T) {
	cfg := Config{
		Worktree: t.TempDir(),
	}

	entry := registry.RegistryEntry{
		ID:      "test-entry",
		Harness: registry.HarnessDriver("bogus-harness"),
	}

	exec, err := buildExecutorForEntry(entry, cfg)
	if err == nil {
		t.Fatal("expected error for unknown harness, got nil")
	}

	if exec != nil {
		t.Fatalf("expected nil executor, got %v", exec)
	}

	// Verify the error mentions the unknown harness
	if !strings.Contains(err.Error(), "unknown harness") && !strings.Contains(err.Error(), "bogus-harness") {
		t.Errorf("error message should mention unknown harness or bogus-harness: %v", err)
	}
}

// TestConfigFromEnvAllowsGeminiSubscriptionEntryWithoutCloudKey tests that a Gemini
// subscription entry (empty SecretRef) is accepted without cloud credentials.
// TC-132-05, REQ-132-04
func TestConfigFromEnvAllowsGeminiSubscriptionEntryWithoutCloudKey(t *testing.T) {
	// Use t.Setenv to set real environment variables.
	t.Setenv("AGENT_BUILDER_TASK_ROOT", "/tmp/tasks")
	t.Setenv("AGENT_BUILDER_WORKTREE", "/tmp/work")
	t.Setenv("AGENT_BUILDER_EXEC_BOX_LAUNCHER", "containment/execution-box/run.sh")
	t.Setenv("AGENT_BUILDER_RUN_TIMEOUT", "5m")
	t.Setenv("AGENT_BUILDER_MAX_ATTEMPTS", "2")
	t.Setenv("AGENT_BUILDER_PUBLISH_REMOTE", "origin")
	// Gemini subscription entry (empty SecretRef, no API key required)
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_ENABLED", "true")
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_ENDPOINT", "https://gemini.google.com") // Required by loader, unused in subscription mode
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_MODEL", "gemini-2.0-flash")
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_CAPABILITY_TIER", "2")
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_COST_WEIGHT", "2")
	t.Setenv("AGENT_BUILDER_REGISTRY_GEMINI_SECRET_REF", "") // Empty: subscription mode
	// NO cloud credentials in environment
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")
	t.Setenv("GEMINI_API_KEY", "")

	getenv := func(key string) string {
		// Delegate to actual environment (which we've set up with t.Setenv above).
		val, _ := os.LookupEnv(key)
		return val
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() for Gemini subscription entry failed: %v", err)
	}

	// Verify that config was successfully created
	if config.Worktree != "/tmp/work" {
		t.Fatalf("Worktree = %q, want %q", config.Worktree, "/tmp/work")
	}

	// Now test buildExecutorForEntry for the Gemini subscription entry
	entries, err := registry.LoadFromEnv()
	if err != nil {
		t.Fatalf("registry.LoadFromEnv() failed: %v", err)
	}

	var geminiEntry *registry.RegistryEntry
	for i := range entries {
		if entries[i].ID == "gemini" {
			geminiEntry = &entries[i]
			break
		}
	}

	if geminiEntry == nil {
		t.Fatal("Gemini entry not found in registry")
	}

	if geminiEntry.SecretRef != "" {
		t.Fatalf("Gemini subscription entry SecretRef = %q, want empty", geminiEntry.SecretRef)
	}

	// TC-132-05: buildExecutorForEntry should return a *executor.GeminiCLI for this entry.
	exec, err := buildExecutorForEntry(*geminiEntry, config)
	if err != nil {
		t.Fatalf("buildExecutorForEntry() for Gemini subscription entry failed: %v", err)
	}

	if exec == nil {
		t.Fatal("buildExecutorForEntry() returned nil executor")
	}

	geminiCLI, ok := exec.(*executor.GeminiCLI)
	if !ok {
		t.Fatalf("buildExecutorForEntry() returned %T, want *executor.GeminiCLI", exec)
	}

	if geminiCLI == nil {
		t.Fatal("GeminiCLI is nil")
	}
}

// TestConfigFromEnvAllowsAntigravitySubscriptionEntryWithoutCloudKey tests that an Antigravity
// subscription entry (empty SecretRef) is accepted without cloud credentials.
// TC-133-06b, REQ-133-06
func TestConfigFromEnvAllowsAntigravitySubscriptionEntryWithoutCloudKey(t *testing.T) {
	// Use t.Setenv to set real environment variables.
	t.Setenv("AGENT_BUILDER_TASK_ROOT", "/tmp/tasks")
	t.Setenv("AGENT_BUILDER_WORKTREE", "/tmp/work")
	t.Setenv("AGENT_BUILDER_EXEC_BOX_LAUNCHER", "containment/execution-box/run.sh")
	t.Setenv("AGENT_BUILDER_RUN_TIMEOUT", "5m")
	t.Setenv("AGENT_BUILDER_MAX_ATTEMPTS", "2")
	t.Setenv("AGENT_BUILDER_PUBLISH_REMOTE", "origin")
	// Antigravity subscription entry (empty SecretRef, no API key required)
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_ENABLED", "true")
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_ENDPOINT", "https://agy.google.com") // Required by loader, unused in subscription mode
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_MODEL", "Claude Opus 4.6 (Thinking)")
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_CAPABILITY_TIER", "3")
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_COST_WEIGHT", "5")
	t.Setenv("AGENT_BUILDER_REGISTRY_ANTIGRAVITY_SECRET_REF", "") // Empty: subscription mode
	// NO cloud credentials in environment
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "")

	getenv := func(key string) string {
		// Delegate to actual environment (which we've set up with t.Setenv above).
		val, _ := os.LookupEnv(key)
		return val
	}

	config, err := ConfigFromEnv(getenv)
	if err != nil {
		t.Fatalf("ConfigFromEnv() for Antigravity subscription entry failed: %v", err)
	}

	// Verify that config was successfully created
	if config.Worktree != "/tmp/work" {
		t.Fatalf("Worktree = %q, want %q", config.Worktree, "/tmp/work")
	}

	// Now test buildExecutorForEntry for the Antigravity subscription entry
	entries, err := registry.LoadFromEnv()
	if err != nil {
		t.Fatalf("registry.LoadFromEnv() failed: %v", err)
	}

	var antigravityEntry *registry.RegistryEntry
	for i := range entries {
		if entries[i].ID == "antigravity" {
			antigravityEntry = &entries[i]
			break
		}
	}

	if antigravityEntry == nil {
		t.Fatal("Antigravity entry not found in registry")
	}

	if antigravityEntry.SecretRef != "" {
		t.Fatalf("Antigravity subscription entry SecretRef = %q, want empty", antigravityEntry.SecretRef)
	}

	// TC-133-06b: buildExecutorForEntry should return a *executor.AntigravityCLI for this entry.
	exec, err := buildExecutorForEntry(*antigravityEntry, config)
	if err != nil {
		t.Fatalf("buildExecutorForEntry() for Antigravity subscription entry failed: %v", err)
	}

	if exec == nil {
		t.Fatal("buildExecutorForEntry() returned nil executor")
	}

	antigravityCLI, ok := exec.(*executor.AntigravityCLI)
	if !ok {
		t.Fatalf("buildExecutorForEntry() returned %T, want *executor.AntigravityCLI", exec)
	}

	if antigravityCLI == nil {
		t.Fatal("AntigravityCLI is nil")
	}
}

// TC-163-01: requireWritable creates a fresh audit chain logfile at mode 0600,
// not the pre-task 0644.
func TestTC163_01_RequireWritableCreatesFileAt0600(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.log")

	if err := requireWritable(path); err != nil {
		t.Fatalf("requireWritable(%q) returned error: %v", path, err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) failed after requireWritable: %v", path, err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("requireWritable(%q) created file with mode %o, want %o", path, got, want)
	}
}

// TC-163-02: requireWritable tightens a pre-existing looser-permission file
// (simulating an audit chain logfile created by a prior deployment before this
// task's fix) to 0600 rather than leaving it at the looser mode it already had.
func TestTC163_02_RequireWritableTightensPreExistingLoosePermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chain.log")

	// Pre-create the file at the pre-task 0644 mode, as a prior deployment would
	// have left it on disk.
	if err := os.WriteFile(path, []byte("existing chain content\n"), 0o644); err != nil {
		t.Fatalf("failed to pre-create %q at 0644: %v", path, err)
	}
	// Sanity: confirm the pre-existing mode really is 0644 before the call under
	// test — otherwise this test would pass vacuously regardless of the fix.
	preInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) failed before requireWritable: %v", path, err)
	}
	if preInfo.Mode().Perm() != 0o644 {
		t.Fatalf("pre-existing file mode = %o, want 0644 (test setup invariant)", preInfo.Mode().Perm())
	}

	if err := requireWritable(path); err != nil {
		t.Fatalf("requireWritable(%q) returned error: %v", path, err)
	}

	postInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("os.Stat(%q) failed after requireWritable: %v", path, err)
	}
	if got, want := postInfo.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("requireWritable(%q) left pre-existing file at mode %o, want tightened to %o", path, got, want)
	}
	// The pre-existing content must survive (requireWritable opens O_APPEND, not
	// O_TRUNC — it must not destroy the chain it's probing).
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("os.ReadFile(%q) failed: %v", path, err)
	}
	if string(content) != "existing chain content\n" {
		t.Errorf("requireWritable(%q) altered file content: got %q", path, string(content))
	}
}
