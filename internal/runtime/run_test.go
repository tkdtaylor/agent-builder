package runtime

import (
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/executor"
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
