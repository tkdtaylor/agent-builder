package secrets

import (
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// Secret refs under which agent-builder registers its publication tokens in vault.
const (
	// SecretRefGitToken is the vault secret_ref for the git publication token.
	SecretRefGitToken = "vault://agent-builder/git-token"
	// SecretRefGitHubToken is the vault secret_ref for the GitHub publication token.
	SecretRefGitHubToken = "vault://agent-builder/github-token"
)

// gitHubHost is the allowlisted host the git/GitHub tokens are bound to. Both
// the REST API (api.github.com) and git-over-HTTPS use the same Authorization:
// Bearer header binding (ADR 036). The operator may add a second binding for the
// raw github.com host if git push uses that hostname; v0 scopes to api.github.com.
const gitHubHost = "api.github.com"

// vaultPutResolver is the subset of the vault client VaultSecretSource needs.
// Defining it as an interface keeps the source testable with a fake and avoids a
// hard construction-time dependency on a live daemon in unit tests.
type vaultPutResolver interface {
	Put(secretRef, value, floor string, binding vault.Binding) error
	Resolve(secretRef string, ttl int) (vault.ResolveResult, error)
}

// Compile-time assertion: the real *vault.Client satisfies vaultPutResolver.
var _ vaultPutResolver = (*vault.Client)(nil)

// VaultSecretSource implements SecretSource by registering the git/GitHub
// publication tokens with vault in proxy-injection mode and holding the resolved
// opaque handles in memory. The plaintext token values are passed to vault
// exactly once (at construction) and are not retained on the struct.
//
// ProviderToken returns the raw env provider values unchanged — provider-token
// brokering is deferred (ADR 036). PublisherTokens returns ("","") because the
// publication tokens now live in vault; the host-side publisher reads its tokens
// from Config directly, not through this source (see the host-publisher note in
// task 066).
type VaultSecretSource struct {
	authToken  string
	oauthToken string

	gitHandle    string
	githubHandle string
	handles      []string
}

// Compile-time assertion: *VaultSecretSource satisfies SecretSource.
var _ SecretSource = (*VaultSecretSource)(nil)

// VaultSourceConfig carries the inputs needed to construct a VaultSecretSource.
// The token values are consumed at construction (put into vault) and not stored.
type VaultSourceConfig struct {
	// AuthToken and OAuthToken are the provider credentials, returned unchanged
	// by ProviderToken (provider brokering deferred).
	AuthToken  string
	OAuthToken string

	// GitToken and GitHubToken are the publication tokens registered with vault.
	GitToken    string
	GitHubToken string

	// TTL is the resolve TTL in seconds for the handles.
	TTL int
}

// NewVaultSecretSource registers the git/GitHub tokens with vault and resolves
// them to opaque handles. A token that is empty is skipped (not registered, no
// handle). It fails loud if a non-empty token cannot be put or resolved.
//
// The client parameter is the vault put/resolve seam — production passes a
// *vault.Client; tests pass a fake.
func NewVaultSecretSource(client vaultPutResolver, cfg VaultSourceConfig) (*VaultSecretSource, error) {
	if client == nil {
		return nil, fmt.Errorf("vault secret source: nil vault client")
	}
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = 300
	}

	src := &VaultSecretSource{
		authToken:  cfg.AuthToken,
		oauthToken: cfg.OAuthToken,
	}

	if cfg.GitToken != "" {
		handle, err := registerToken(client, SecretRefGitToken, cfg.GitToken, "GIT_TOKEN", ttl)
		if err != nil {
			return nil, err
		}
		src.gitHandle = handle
		src.handles = append(src.handles, handle)
	}
	if cfg.GitHubToken != "" {
		handle, err := registerToken(client, SecretRefGitHubToken, cfg.GitHubToken, "GITHUB_TOKEN", ttl)
		if err != nil {
			return nil, err
		}
		src.githubHandle = handle
		src.handles = append(src.handles, handle)
	}

	return src, nil
}

// registerToken puts one token into vault with a proxy-floor api.github.com
// Authorization: Bearer binding and resolves it to an opaque handle. The token
// value is never logged or embedded in an error (the vault client guarantees this).
func registerToken(client vaultPutResolver, secretRef, value, envVar string, ttl int) (string, error) {
	binding := vault.Binding{
		Host:   gitHubHost,
		Header: "Authorization",
		Scheme: "Bearer",
		EnvVar: envVar,
	}
	if err := client.Put(secretRef, value, "proxy", binding); err != nil {
		// client.Put never embeds the value in its error.
		return "", fmt.Errorf("vault secret source: register %s: %w", secretRef, err)
	}
	result, err := client.Resolve(secretRef, ttl)
	if err != nil {
		return "", fmt.Errorf("vault secret source: resolve %s: %w", secretRef, err)
	}
	return result.Handle, nil
}

// ProviderToken returns the raw provider auth and OAuth tokens unchanged.
// Provider-token vault brokering is deferred (ADR 036).
func (s *VaultSecretSource) ProviderToken() (authToken, oauthToken string) {
	return s.authToken, s.oauthToken
}

// PublisherTokens returns ("","") — the publication tokens are registered with
// vault and reached through opaque handles. The host-side publisher reads its
// tokens from Config directly, not through this source.
func (s *VaultSecretSource) PublisherTokens() (gitToken, githubToken string) {
	return "", ""
}

// Handles returns the opaque vault handles for the registered tokens, in
// git-then-GitHub order. Handles are safe to log and are passed into
// sandbox.Request.Wiring.SecretRefs for proxy injection.
func (s *VaultSecretSource) Handles() []string {
	out := make([]string, len(s.handles))
	copy(out, s.handles)
	return out
}
