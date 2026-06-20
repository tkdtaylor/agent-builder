// Package secrets provides the SecretSource abstraction for token retrieval.
// It is a leaf package: no imports from other agent-builder/internal packages.
// The dependency direction is executor → secrets, not the reverse.
//
// VaultSecretSource (vault_source.go) implements the same SecretSource
// interface and is swapped in at construction time without changing call-site
// code in executor or runtime.
package secrets

import "os"

const (
	// EnvAnthropicAPIKey is the env var for the Claude Code CLI API key credential.
	EnvAnthropicAPIKey = "ANTHROPIC_API_KEY"
	// EnvClaudeCodeOAuthToken is the env var for the subscription OAuth token.
	EnvClaudeCodeOAuthToken = "CLAUDE_CODE_OAUTH_TOKEN"
	// EnvAgentBuilderGitToken is the env var for the git publication token.
	EnvAgentBuilderGitToken = "AGENT_BUILDER_GIT_TOKEN"
	// EnvAgentBuilderGitHubToken is the env var for the GitHub publication token.
	EnvAgentBuilderGitHubToken = "AGENT_BUILDER_GITHUB_TOKEN"
)

// SecretSource abstracts token retrieval so vault can be substituted for
// os.Getenv without changing call-site code.
type SecretSource interface {
	// ProviderToken returns the Claude auth token and OAuth token.
	// Either may be empty. OAuth is preferred when both are set (ADR 033).
	ProviderToken() (authToken, oauthToken string)

	// PublisherTokens returns the git and GitHub publication tokens.
	// Either may be empty.
	PublisherTokens() (gitToken, githubToken string)
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
