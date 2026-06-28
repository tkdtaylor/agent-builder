// Package secrets provides the SecretSource abstraction for token retrieval.
// It is a leaf package: no imports from other agent-builder/internal packages.
// The dependency direction is executor → secrets, not the reverse.
//
// VaultSecretSource (vault_source.go) implements the same SecretSource
// interface and is swapped in at construction time without changing call-site
// code in executor or runtime.
package secrets

import (
	"errors"
	"os"
	"strings"
)

const (
	// EnvAnthropicAPIKey is the env var for the Claude Code CLI API key credential.
	EnvAnthropicAPIKey = "ANTHROPIC_API_KEY"
	// EnvClaudeCodeOAuthToken is the env var for the subscription OAuth token.
	EnvClaudeCodeOAuthToken = "CLAUDE_CODE_OAUTH_TOKEN"
	// EnvAgentBuilderGitToken is the env var for the git publication token.
	EnvAgentBuilderGitToken = "AGENT_BUILDER_GIT_TOKEN"
	// EnvAgentBuilderGitHubToken is the env var for the GitHub publication token.
	EnvAgentBuilderGitHubToken = "AGENT_BUILDER_GITHUB_TOKEN"
	// EnvSecretPrefix is the prefix for named-provider secret env vars.
	// SecretRef "codex-token" → AGENT_BUILDER_SECRET_CODEX_TOKEN.
	EnvSecretPrefix = "AGENT_BUILDER_SECRET_"
)

// ErrSecretNotFound indicates that a named secret was not found in the source.
var ErrSecretNotFound = errors.New("secret not found")

// SecretSource abstracts token retrieval so vault can be substituted for
// os.Getenv without changing call-site code.
type SecretSource interface {
	// ProviderToken returns the Claude auth token and OAuth token.
	// Either may be empty. OAuth is preferred when both are set (ADR 033).
	ProviderToken() (authToken, oauthToken string)

	// PublisherTokens returns the git and GitHub publication tokens.
	// Either may be empty.
	PublisherTokens() (gitToken, githubToken string)

	// NamedProviderToken resolves a named provider secret by its reference.
	// Returns an opaque handle (safe to log) and an error if not found.
	// See EnvSecretSource and VaultSecretSource for implementation details.
	NamedProviderToken(ref string) (string, error)
}

// EnvSecretSource reads tokens from the process environment.
// It is the default implementation for production use.
type EnvSecretSource struct{}

// Compile-time assertion: EnvSecretSource must implement SecretSource.
var _ SecretSource = (*EnvSecretSource)(nil)

// NewEnvSecretSource constructs an EnvSecretSource.
func NewEnvSecretSource() *EnvSecretSource {
	return &EnvSecretSource{}
}

// ProviderToken returns the Claude API key and OAuth token from the process
// environment. Either may be empty. OAuth is preferred when both are set (ADR 033).
func (e *EnvSecretSource) ProviderToken() (authToken, oauthToken string) {
	return os.Getenv(EnvAnthropicAPIKey), os.Getenv(EnvClaudeCodeOAuthToken)
}

// PublisherTokens returns the git and GitHub publication tokens from the
// process environment. Either may be empty.
func (e *EnvSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return os.Getenv(EnvAgentBuilderGitToken), os.Getenv(EnvAgentBuilderGitHubToken)
}

// NamedProviderToken resolves a named provider secret from the process environment.
// The env var is derived by uppercasing the ref and replacing hyphens with
// underscores, prefixed with AGENT_BUILDER_SECRET_. For example, "codex-token" →
// AGENT_BUILDER_SECRET_CODEX_TOKEN. Returns ErrSecretNotFound if the env var is unset.
func (e *EnvSecretSource) NamedProviderToken(ref string) (string, error) {
	envVarName := EnvSecretPrefix + strings.ToUpper(strings.ReplaceAll(ref, "-", "_"))
	token := os.Getenv(envVarName)
	if token == "" {
		return "", ErrSecretNotFound
	}
	return token, nil
}
