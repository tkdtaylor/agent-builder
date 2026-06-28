package runtime

import (
	"os"
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
