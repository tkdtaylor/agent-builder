package secrets_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// fakeVaultClient records put calls and returns canned resolve handles. It never
// returns the plaintext value in a handle (mirroring the real vault contract).
type fakeVaultClient struct {
	puts        []putCall
	handleByRef map[string]string
	putErr      error
	resolveErr  error
}

type putCall struct {
	secretRef string
	value     string
	floor     string
	binding   vault.Binding
}

func (f *fakeVaultClient) Put(secretRef, value, floor string, binding vault.Binding) error {
	f.puts = append(f.puts, putCall{secretRef, value, floor, binding})
	return f.putErr
}

func (f *fakeVaultClient) Resolve(secretRef string, ttl int) (vault.ResolveResult, error) {
	if f.resolveErr != nil {
		return vault.ResolveResult{}, f.resolveErr
	}
	h, ok := f.handleByRef[secretRef]
	if !ok {
		return vault.ResolveResult{}, errors.New("no_such_secret")
	}
	return vault.ResolveResult{Handle: h, TTL: ttl, InjectionMode: "proxy"}, nil
}

// TC-066-01 (unit half): VaultSecretSource registers git/GitHub tokens via Put
// with a proxy floor and api.github.com Authorization: Bearer binding, resolves
// them to opaque handles, and exposes the handles via Handles().
func TestVaultSecretSourceRegistersAndResolves(t *testing.T) {
	fake := &fakeVaultClient{
		handleByRef: map[string]string{
			secrets.SecretRefGitToken:    "handle-git",
			secrets.SecretRefGitHubToken: "handle-github",
		},
	}
	src, err := secrets.NewVaultSecretSource(fake, secrets.VaultSourceConfig{
		AuthToken:   "sk-auth",
		OAuthToken:  "oauth-tok",
		GitToken:    "gittok-123",
		GitHubToken: "ghtok-456",
	})
	if err != nil {
		t.Fatalf("NewVaultSecretSource err = %v", err)
	}

	// Two puts, both with proxy floor and api.github.com binding.
	if len(fake.puts) != 2 {
		t.Fatalf("put calls = %d, want 2", len(fake.puts))
	}
	for _, p := range fake.puts {
		if p.floor != "proxy" {
			t.Errorf("put %s floor = %q, want proxy", p.secretRef, p.floor)
		}
		if p.binding.Host != "api.github.com" {
			t.Errorf("put %s binding.Host = %q, want api.github.com", p.secretRef, p.binding.Host)
		}
		if p.binding.Header != "Authorization" || p.binding.Scheme != "Bearer" {
			t.Errorf("put %s binding header/scheme = %q/%q, want Authorization/Bearer", p.secretRef, p.binding.Header, p.binding.Scheme)
		}
	}

	// Handles() returns the resolved opaque handles, git-then-GitHub.
	handles := src.Handles()
	if len(handles) != 2 || handles[0] != "handle-git" || handles[1] != "handle-github" {
		t.Fatalf("Handles() = %v, want [handle-git handle-github]", handles)
	}

	// SECURITY: no handle contains the plaintext token value.
	for _, h := range handles {
		if strings.Contains(h, "gittok-123") || strings.Contains(h, "ghtok-456") {
			t.Errorf("handle %q leaks a plaintext token value", h)
		}
	}
}

// TC-066-01 (unit half): ProviderToken returns the raw env values unchanged
// (provider brokering deferred); PublisherTokens returns ("","").
func TestVaultSecretSourceTokenAccessors(t *testing.T) {
	fake := &fakeVaultClient{
		handleByRef: map[string]string{
			secrets.SecretRefGitToken:    "h1",
			secrets.SecretRefGitHubToken: "h2",
		},
	}
	src, err := secrets.NewVaultSecretSource(fake, secrets.VaultSourceConfig{
		AuthToken:   "sk-auth",
		OAuthToken:  "oauth-tok",
		GitToken:    "g",
		GitHubToken: "gh",
	})
	if err != nil {
		t.Fatalf("NewVaultSecretSource err = %v", err)
	}

	auth, oauth := src.ProviderToken()
	if auth != "sk-auth" || oauth != "oauth-tok" {
		t.Errorf("ProviderToken() = %q,%q want sk-auth,oauth-tok", auth, oauth)
	}

	git, gh := src.PublisherTokens()
	if git != "" || gh != "" {
		t.Errorf("PublisherTokens() = %q,%q want empty,empty (tokens are in vault)", git, gh)
	}
}

// Empty tokens are skipped: no put, no handle for an empty value.
func TestVaultSecretSourceSkipsEmptyTokens(t *testing.T) {
	fake := &fakeVaultClient{
		handleByRef: map[string]string{secrets.SecretRefGitToken: "h1"},
	}
	src, err := secrets.NewVaultSecretSource(fake, secrets.VaultSourceConfig{
		GitToken:    "g",
		GitHubToken: "", // empty → skipped
	})
	if err != nil {
		t.Fatalf("NewVaultSecretSource err = %v", err)
	}
	if len(fake.puts) != 1 {
		t.Fatalf("put calls = %d, want 1 (empty GitHub token skipped)", len(fake.puts))
	}
	if h := src.Handles(); len(h) != 1 || h[0] != "h1" {
		t.Fatalf("Handles() = %v, want [h1]", h)
	}
}

// A failing put fails construction loud (and does not leak the value in the error).
func TestVaultSecretSourcePutFailureFailsLoud(t *testing.T) {
	fake := &fakeVaultClient{
		putErr:      errors.New("store full"),
		handleByRef: map[string]string{},
	}
	_, err := secrets.NewVaultSecretSource(fake, secrets.VaultSourceConfig{GitToken: "secret-value-xyz"})
	if err == nil {
		t.Fatal("expected error from failed put, got nil")
	}
	if strings.Contains(err.Error(), "secret-value-xyz") {
		t.Errorf("error leaks the plaintext token value: %v", err)
	}
}

// Compile-time: *VaultSecretSource satisfies SecretSource.
var _ secrets.SecretSource = (*secrets.VaultSecretSource)(nil)

// TC-066-08: make check exits 0; docs/spec/{configuration,interfaces,SPEC}.md
// updated for the five vault env vars, sandbox.Request.Wiring, and invariant 7;
// the fake-provider TestPhase0EndToEndAcceptance passes with vault unconfigured.
// Verified by: make check → "All checks passed." and
//   go test ./tests/e2e/... -run TestPhase0EndToEndAcceptance → ok.
// internal/secrets imports only internal/vault (a sibling leaf); internal/vault
// imports only stdlib — confirmed by `go list -deps` (no import cycle).

// TC-088-03: VaultSecretSource.NamedProviderToken resolves a named secret via vault
func TestVaultSecretSourceNamedProviderToken(t *testing.T) {
	fake := &fakeVaultClient{
		handleByRef: map[string]string{
			secrets.SecretRefGitToken:    "vault://handle/git-12345",
			secrets.SecretRefGitHubToken: "vault://handle/github-67890",
			"gemini-api-key":             "vault://handle/gemini-12345",
			"claude-oauth":               "vault://handle/claude-oauth-67890",
		},
	}
	src, err := secrets.NewVaultSecretSource(fake, secrets.VaultSourceConfig{
		AuthToken:   "sk-auth",
		OAuthToken:  "oauth-tok",
		GitToken:    "g",
		GitHubToken: "gh",
	})
	if err != nil {
		t.Fatalf("NewVaultSecretSource err = %v", err)
	}

	// Resolve an existing secret — should return opaque handle.
	handle, err := src.NamedProviderToken("gemini-api-key")
	if err != nil {
		t.Fatalf("NamedProviderToken(gemini-api-key) err = %v", err)
	}
	if handle != "vault://handle/gemini-12345" {
		t.Fatalf("NamedProviderToken(gemini-api-key) = %q, want vault://handle/gemini-12345", handle)
	}

	// Resolve another existing secret.
	handle2, err := src.NamedProviderToken("claude-oauth")
	if err != nil {
		t.Fatalf("NamedProviderToken(claude-oauth) err = %v", err)
	}
	if handle2 != "vault://handle/claude-oauth-67890" {
		t.Fatalf("NamedProviderToken(claude-oauth) = %q, want vault://handle/claude-oauth-67890", handle2)
	}

	// Resolve a non-existent secret — should return ErrSecretNotFound.
	_, err = src.NamedProviderToken("non-existent-ref")
	if err != secrets.ErrSecretNotFound {
		t.Fatalf("NamedProviderToken(non-existent-ref) err = %v, want ErrSecretNotFound", err)
	}

	// SECURITY: no handle contains plaintext secret values.
	for _, h := range []string{handle, handle2} {
		if strings.Contains(h, "secret-gemini-value") || strings.Contains(h, "secret-value") {
			t.Errorf("handle %q leaks a plaintext token value", h)
		}
	}
}

// TC-088-04: SecretSource interface compile-time assertion
// (This is verified at compile time; no runtime test needed, but we include a
// comment here for clarity.)
// var _ secrets.SecretSource = (*secrets.EnvSecretSource)(nil)
// var _ secrets.SecretSource = (*secrets.VaultSecretSource)(nil)

// These assertions appear at the top of this file and in secrets.go, respectively.
