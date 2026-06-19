package secrets_test

import (
	"os"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/secrets"
)

// FakeSecretSource is a test double for SecretSource.
// It is exported so other packages in the test suite (e.g. executor, runtime)
// can import and reuse it directly.
type FakeSecretSource struct {
	AuthToken   string
	OAuthToken  string
	GitToken    string
	GitHubToken string
}

// ProviderToken implements SecretSource.
func (f *FakeSecretSource) ProviderToken() (authToken, oauthToken string) {
	return f.AuthToken, f.OAuthToken
}

// PublisherTokens implements SecretSource.
func (f *FakeSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return f.GitToken, f.GitHubToken
}

// Compile-time assertion: FakeSecretSource satisfies SecretSource.
var _ secrets.SecretSource = (*FakeSecretSource)(nil)

// TC-065-06: internal/secrets is a leaf package (no internal deps; stdlib only).
// Verified by: go list -deps ./internal/secrets/... | grep 'agent-builder/internal/'
// Expected result: only github.com/tkdtaylor/agent-builder/internal/secrets itself appears
// (no other agent-builder internal imports). See Makefile / CI for the enforcing check.
//
// TC-065-07: make check exits 0; no behavior change from pre-refactor.
// Verified by running: make check → "All checks passed."
// and: go test -count=1 ./tests/e2e/... -run TestPhase0EndToEndAcceptance → PASS

// TC-065-01: SecretSource interface + EnvSecretSource shape
func TestEnvSecretSourceCompiles(t *testing.T) {
	// NewEnvSecretSource constructs a non-nil *EnvSecretSource.
	src := secrets.NewEnvSecretSource()
	if src == nil {
		t.Fatal("NewEnvSecretSource() returned nil")
	}
	// The compile-time assertion in secrets.go ensures *EnvSecretSource
	// satisfies the SecretSource interface. This test being compilable proves it.
	var _ secrets.SecretSource = src
}

// TC-065-02: EnvSecretSource.ProviderToken reads ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN
func TestEnvSecretSourceProviderToken(t *testing.T) {
	tests := []struct {
		name            string
		apiKey          string
		oauthToken      string
		wantAuthToken   string
		wantOAuthToken  string
	}{
		{
			name:           "API key only",
			apiKey:         "sk-123",
			oauthToken:     "",
			wantAuthToken:  "sk-123",
			wantOAuthToken: "",
		},
		{
			name:           "OAuth token only",
			apiKey:         "",
			oauthToken:     "oauth-tok",
			wantAuthToken:  "",
			wantOAuthToken: "oauth-tok",
		},
		{
			name:           "Both set",
			apiKey:         "sk-123",
			oauthToken:     "oauth-tok",
			wantAuthToken:  "sk-123",
			wantOAuthToken: "oauth-tok",
		},
		{
			name:           "Neither set",
			apiKey:         "",
			oauthToken:     "",
			wantAuthToken:  "",
			wantOAuthToken: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore environment.
			oldAPIKey := os.Getenv(secrets.EnvAnthropicAPIKey)
			oldOAuth := os.Getenv(secrets.EnvClaudeCodeOAuthToken)
			defer func() {
				_ = os.Setenv(secrets.EnvAnthropicAPIKey, oldAPIKey)
				_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, oldOAuth)
			}()

			_ = os.Setenv(secrets.EnvAnthropicAPIKey, tt.apiKey)
			_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, tt.oauthToken)

			src := secrets.NewEnvSecretSource()
			gotAuth, gotOAuth := src.ProviderToken()

			if gotAuth != tt.wantAuthToken {
				t.Fatalf("ProviderToken() authToken = %q, want %q", gotAuth, tt.wantAuthToken)
			}
			if gotOAuth != tt.wantOAuthToken {
				t.Fatalf("ProviderToken() oauthToken = %q, want %q", gotOAuth, tt.wantOAuthToken)
			}
		})
	}
}

// TC-065-03: EnvSecretSource.PublisherTokens reads AGENT_BUILDER_GIT_TOKEN and AGENT_BUILDER_GITHUB_TOKEN
func TestEnvSecretSourcePublisherTokens(t *testing.T) {
	tests := []struct {
		name            string
		gitToken        string
		githubToken     string
		wantGitToken    string
		wantGitHubToken string
	}{
		{
			name:            "Git token only",
			gitToken:        "gittok",
			githubToken:     "",
			wantGitToken:    "gittok",
			wantGitHubToken: "",
		},
		{
			name:            "GitHub token only",
			gitToken:        "",
			githubToken:     "ghtok",
			wantGitToken:    "",
			wantGitHubToken: "ghtok",
		},
		{
			name:            "Both set",
			gitToken:        "gittok",
			githubToken:     "ghtok",
			wantGitToken:    "gittok",
			wantGitHubToken: "ghtok",
		},
		{
			name:            "Neither set",
			gitToken:        "",
			githubToken:     "",
			wantGitToken:    "",
			wantGitHubToken: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save and restore environment.
			oldGit := os.Getenv(secrets.EnvAgentBuilderGitToken)
			oldGitHub := os.Getenv(secrets.EnvAgentBuilderGitHubToken)
			defer func() {
				_ = os.Setenv(secrets.EnvAgentBuilderGitToken, oldGit)
				_ = os.Setenv(secrets.EnvAgentBuilderGitHubToken, oldGitHub)
			}()

			_ = os.Setenv(secrets.EnvAgentBuilderGitToken, tt.gitToken)
			_ = os.Setenv(secrets.EnvAgentBuilderGitHubToken, tt.githubToken)

			src := secrets.NewEnvSecretSource()
			gotGit, gotGitHub := src.PublisherTokens()

			if gotGit != tt.wantGitToken {
				t.Fatalf("PublisherTokens() gitToken = %q, want %q", gotGit, tt.wantGitToken)
			}
			if gotGitHub != tt.wantGitHubToken {
				t.Fatalf("PublisherTokens() githubToken = %q, want %q", gotGitHub, tt.wantGitHubToken)
			}
		})
	}
}
