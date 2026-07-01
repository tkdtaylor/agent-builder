package secrets

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// DiskOAuthSecretSource reads the subscription OAuth token from
// ${HOME}/.claude/.credentials.json (the Claude CLI's on-disk credentials file).
// It reads only the claudeAiOauth.accessToken field. Missing/malformed/empty files
// return ("","") with no error — graceful fallback for hosts without on-disk login.
type DiskOAuthSecretSource struct {
	homePath string // typically os.Getenv("HOME") in production; injected for testing
}

// Compile-time assertion: DiskOAuthSecretSource must implement SecretSource.
var _ SecretSource = (*DiskOAuthSecretSource)(nil)

// NewDiskOAuthSecretSource constructs a DiskOAuthSecretSource that reads from
// ${HOME}/.claude/.credentials.json. homePath defaults to os.Getenv("HOME").
func NewDiskOAuthSecretSource() *DiskOAuthSecretSource {
	return &DiskOAuthSecretSource{
		homePath: os.Getenv("HOME"),
	}
}

// NewDiskOAuthSecretSourceWithHome is the test constructor that accepts a custom home path.
func NewDiskOAuthSecretSourceWithHome(homePath string) *DiskOAuthSecretSource {
	return &DiskOAuthSecretSource{
		homePath: homePath,
	}
}

// ProviderToken reads the OAuth token from the on-disk credentials file.
// It returns ("", oauthToken) where oauthToken is claudeAiOauth.accessToken.
// Missing/malformed/empty → ("","") with no error (graceful absence).
func (d *DiskOAuthSecretSource) ProviderToken() (authToken, oauthToken string) {
	if strings.TrimSpace(d.homePath) == "" {
		return "", ""
	}

	credPath := filepath.Join(d.homePath, ".claude", ".credentials.json")
	data, err := os.ReadFile(credPath)
	if err != nil {
		// File missing, unreadable, etc. → graceful absence, no error
		return "", ""
	}

	// Parse JSON structure: {"claudeAiOauth":{"accessToken":"...",...},...}
	var creds struct {
		ClaudeAiOAuth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if err := json.Unmarshal(data, &creds); err != nil {
		// Malformed JSON → graceful absence, no error
		return "", ""
	}

	// Return only the accessToken; never the refresh token (security boundary).
	// Empty token is treated as absence (graceful).
	return "", strings.TrimSpace(creds.ClaudeAiOAuth.AccessToken)
}

// PublisherTokens returns empty tokens (DiskOAuthSecretSource only handles provider OAuth).
func (d *DiskOAuthSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return "", ""
}

// NamedProviderToken is not supported by DiskOAuthSecretSource.
func (d *DiskOAuthSecretSource) NamedProviderToken(ref string) (string, error) {
	return "", ErrSecretNotFound
}

// ChainedSecretSource chains two sources: env → disk. It returns the env token when
// present; otherwise falls back to disk. Precedence: env token wins; disk only consulted
// when env is empty (ADR 033 preserved).
type ChainedSecretSource struct {
	env  SecretSource
	disk SecretSource
}

// Compile-time assertion: ChainedSecretSource must implement SecretSource.
var _ SecretSource = (*ChainedSecretSource)(nil)

// NewChainedSecretSource constructs a source that prefers env over disk.
func NewChainedSecretSource(env, disk SecretSource) *ChainedSecretSource {
	return &ChainedSecretSource{
		env:  env,
		disk: disk,
	}
}

// ProviderToken returns the env token when present; otherwise disk token.
// Env tokens (both authToken and oauthToken) are checked first; disk is only
// consulted when both env credentials are empty.
func (c *ChainedSecretSource) ProviderToken() (authToken, oauthToken string) {
	envAuth, envOAuth := c.env.ProviderToken()

	// If env provided any credential, use it (ADR 033 precedence: env wins).
	if strings.TrimSpace(envAuth) != "" || strings.TrimSpace(envOAuth) != "" {
		return envAuth, envOAuth
	}

	// Both env credentials empty → consult disk.
	return c.disk.ProviderToken()
}

// PublisherTokens delegates to the env source (disk does not provide publisher tokens).
func (c *ChainedSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return c.env.PublisherTokens()
}

// NamedProviderToken delegates to the env source (disk does not provide named tokens).
func (c *ChainedSecretSource) NamedProviderToken(ref string) (string, error) {
	return c.env.NamedProviderToken(ref)
}
