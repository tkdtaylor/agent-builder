package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// fakeSecretSource is a test double for secrets.SecretSource used in TC-065-04.
type fakeSecretSource struct {
	authToken   string
	oauthToken  string
	gitToken    string
	githubToken string
}

func (f *fakeSecretSource) ProviderToken() (string, string) {
	return f.authToken, f.oauthToken
}

func (f *fakeSecretSource) PublisherTokens() (string, string) {
	return f.gitToken, f.githubToken
}

func (f *fakeSecretSource) NamedProviderToken(ref string) (string, error) {
	return "", secrets.ErrSecretNotFound
}

// Compile-time assertion: fakeSecretSource satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*fakeSecretSource)(nil)

func TestNewClaudeCLIWithOAuthTokenOnly(t *testing.T) {
	// TC-059-01: OAuth token alone authenticates
	config := ClaudeCLIConfig{
		CLIPath:    "claude",
		Worktree:   "/tmp/work",
		OAuthToken: "oauth-token-123",
	}
	cli := NewClaudeCLI(config)

	if cli == nil {
		t.Fatal("NewClaudeCLI() returned nil")
	}
	if cli.oauthToken != "oauth-token-123" {
		t.Fatalf("oauthToken = %q, want %q", cli.oauthToken, "oauth-token-123")
	}
	if cli.authToken != "" {
		t.Fatalf("authToken = %q, want empty", cli.authToken)
	}
}

func TestNewClaudeCLIWithAuthTokenOnly(t *testing.T) {
	// TC-059-01: API key alone authenticates
	config := ClaudeCLIConfig{
		CLIPath:   "claude",
		Worktree:  "/tmp/work",
		AuthToken: "api-key-xyz",
	}
	cli := NewClaudeCLI(config)

	if cli == nil {
		t.Fatal("NewClaudeCLI() returned nil")
	}
	if cli.authToken != "api-key-xyz" {
		t.Fatalf("authToken = %q, want %q", cli.authToken, "api-key-xyz")
	}
	if cli.oauthToken != "" {
		t.Fatalf("oauthToken = %q, want empty", cli.oauthToken)
	}
}

func TestClaudeEnvInjectsExactlyOneAuthVar(t *testing.T) {
	// TC-059-02: claudeEnv injects exactly the chosen credential
	tests := []struct {
		name           string
		authToken      string
		oauthToken     string
		wantAuthEnv    bool
		wantOAuthEnv   bool
		wantAuthValue  string
		wantOAuthValue string
	}{
		{
			name:           "OAuth only",
			authToken:      "",
			oauthToken:     "oauth-123",
			wantAuthEnv:    false,
			wantOAuthEnv:   true,
			wantOAuthValue: "oauth-123",
		},
		{
			name:          "API key only",
			authToken:     "api-key",
			oauthToken:    "",
			wantAuthEnv:   true,
			wantOAuthEnv:  false,
			wantAuthValue: "api-key",
		},
		{
			name:           "OAuth preferred when both present",
			authToken:      "api-key",
			oauthToken:     "oauth-123",
			wantAuthEnv:    false,
			wantOAuthEnv:   true,
			wantOAuthValue: "oauth-123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseEnv := []string{
				"PATH=/usr/bin",
				ClaudeCLIAuthEnv + "=old-api-key",
				ClaudeCLIOAuthEnv + "=old-oauth",
			}
			env := claudeEnv(baseEnv, tt.authToken, tt.oauthToken, "", "/tmp/home")

			hasAuthVar := false
			hasOAuthVar := false
			var authValue, oauthValue string

			for _, e := range env {
				if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
					hasAuthVar = true
					authValue = strings.TrimPrefix(e, ClaudeCLIAuthEnv+"=")
				}
				if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
					hasOAuthVar = true
					oauthValue = strings.TrimPrefix(e, ClaudeCLIOAuthEnv+"=")
				}
			}

			if hasAuthVar != tt.wantAuthEnv {
				t.Fatalf("has %s = %v, want %v", ClaudeCLIAuthEnv, hasAuthVar, tt.wantAuthEnv)
			}
			if hasOAuthVar != tt.wantOAuthEnv {
				t.Fatalf("has %s = %v, want %v", ClaudeCLIOAuthEnv, hasOAuthVar, tt.wantOAuthEnv)
			}
			if tt.wantAuthEnv && authValue != tt.wantAuthValue {
				t.Fatalf("%s value = %q, want %q", ClaudeCLIAuthEnv, authValue, tt.wantAuthValue)
			}
			if tt.wantOAuthEnv && oauthValue != tt.wantOAuthValue {
				t.Fatalf("%s value = %q, want %q", ClaudeCLIOAuthEnv, oauthValue, tt.wantOAuthValue)
			}

			// Verify HOME/XDG wipe is intact
			hasHOME := false
			hasXDGConfig := false
			hasXDGCache := false
			for _, e := range env {
				if strings.HasPrefix(e, "HOME=") {
					hasHOME = true
				}
				if strings.HasPrefix(e, "XDG_CONFIG_HOME=") {
					hasXDGConfig = true
				}
				if strings.HasPrefix(e, "XDG_CACHE_HOME=") {
					hasXDGCache = true
				}
			}
			if !hasHOME || !hasXDGConfig || !hasXDGCache {
				t.Fatalf("HOME/XDG wipe incomplete: HOME=%v XDG_CONFIG=%v XDG_CACHE=%v", hasHOME, hasXDGConfig, hasXDGCache)
			}
		})
	}
}

func TestClaudeEnvStripsOldCredentials(t *testing.T) {
	// TC-059-02: old credentials are stripped from the environment
	baseEnv := []string{
		"PATH=/usr/bin",
		ClaudeCLIAuthEnv + "=old-api-key",
		ClaudeCLIOAuthEnv + "=old-oauth",
		"USER=testuser",
	}
	env := claudeEnv(baseEnv, "new-api-key", "", "", "/tmp/home")

	for _, e := range env {
		if strings.Contains(e, "old-api-key") {
			t.Fatalf("old API key found in env: %s", e)
		}
		if strings.Contains(e, "old-oauth") {
			t.Fatalf("old OAuth token found in env: %s", e)
		}
	}
}

func TestValidateMissingBothCredentialsFails(t *testing.T) {
	// TC-059-04: missing both credentials fails loudly
	config := ClaudeCLIConfig{
		CLIPath:    "claude",
		Worktree:   "/tmp/work",
		AuthToken:  "",
		OAuthToken: "",
	}
	cli := NewClaudeCLI(config)

	task := supervisor.Task{ID: "001", Spec: "test spec"}

	err := cli.validate(task)
	if err == nil {
		t.Fatal("validate() returned nil, want error")
	}
	if !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
		t.Fatalf("error doesn't mention ANTHROPIC_API_KEY: %v", err)
	}
	if !strings.Contains(err.Error(), "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("error doesn't mention CLAUDE_CODE_OAUTH_TOKEN: %v", err)
	}
}

func TestSanitizeRedactsOAuthToken(t *testing.T) {
	// TC-059-06: output sanitization redacts OAuth token
	stdout := "Successfully authenticated with token oauth-secret-123"
	stderr := "Warning: using token oauth-secret-123"
	authToken := ""
	oauthToken := "oauth-secret-123"

	result := sanitizeCLIOutput(stdout, stderr, authToken, oauthToken)

	if strings.Contains(result, "oauth-secret-123") {
		t.Fatalf("OAuth token not redacted in output: %s", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("token not replaced with [REDACTED]: %s", result)
	}
}

func TestSanitizeRedactsAuthToken(t *testing.T) {
	// TC-059-06: output sanitization redacts API key token
	stdout := "Successfully authenticated with token api-key-abc123"
	stderr := "Using API key api-key-abc123"
	authToken := "api-key-abc123"
	oauthToken := ""

	result := sanitizeCLIOutput(stdout, stderr, authToken, oauthToken)

	if strings.Contains(result, "api-key-abc123") {
		t.Fatalf("API key not redacted in output: %s", result)
	}
	if !strings.Contains(result, "[REDACTED]") {
		t.Fatalf("token not replaced with [REDACTED]: %s", result)
	}
}

func TestSanitizeRedactsBothTokens(t *testing.T) {
	// TC-059-06: both tokens are redacted when both present
	stdout := "Auth token api-key-abc123 and oauth-token-xyz"
	stderr := ""
	authToken := "api-key-abc123"
	oauthToken := "oauth-token-xyz"

	result := sanitizeCLIOutput(stdout, stderr, authToken, oauthToken)

	if strings.Contains(result, "api-key-abc123") {
		t.Fatalf("API key not redacted: %s", result)
	}
	if strings.Contains(result, "oauth-token-xyz") {
		t.Fatalf("OAuth token not redacted: %s", result)
	}
}

func TestNewClaudeCLIFromEnvReadsBothTokens(t *testing.T) {
	// TC-059-01: NewClaudeCLIFromEnv reads both tokens from environment
	oldAuthToken := os.Getenv(ClaudeCLIAuthEnv)
	oldOAuthToken := os.Getenv(ClaudeCLIOAuthEnv)
	defer func() {
		_ = os.Setenv(ClaudeCLIAuthEnv, oldAuthToken)
		_ = os.Setenv(ClaudeCLIOAuthEnv, oldOAuthToken)
	}()

	_ = os.Setenv(ClaudeCLIAuthEnv, "")
	_ = os.Setenv(ClaudeCLIOAuthEnv, "oauth-from-env")

	cli := NewClaudeCLIFromEnv("/tmp/work")

	if cli.oauthToken != "oauth-from-env" {
		t.Fatalf("oauthToken = %q, want %q", cli.oauthToken, "oauth-from-env")
	}
	if cli.authToken != "" {
		t.Fatalf("authToken = %q, want empty", cli.authToken)
	}
}

// TC-065-04: NewClaudeCLIFromSecretSource delegates to SecretSource (not direct os.Getenv)
// fakeLocalSecretSource is a test double for local entries (SecretRef == "").
// ProviderToken returns empty strings — local entries have no cloud auth.
type fakeLocalSecretSource struct{}

func (f *fakeLocalSecretSource) ProviderToken() (string, string)              { return "", "" }
func (f *fakeLocalSecretSource) PublisherTokens() (string, string)            { return "", "" }
func (f *fakeLocalSecretSource) NamedProviderToken(_ string) (string, error)  { return "", secrets.ErrSecretNotFound }

// Compile-time assertion: fakeLocalSecretSource satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*fakeLocalSecretSource)(nil)

// TC-091-03a (updated by TC-101): claudeEnv with baseURL set injects ANTHROPIC_BASE_URL
// and a placeholder sentinel as ANTHROPIC_AUTH_TOKEN (not the operator's real credentials).
// This is the primary env-contract assertion: directly tests the env-building function.
func TestClaudeEnv_LocalMode_SetsBaseURLAndOmitsCloudAuth(t *testing.T) {
	const proxyURL = "http://localhost:8080"
	baseEnv := []string{
		"PATH=/usr/bin",
		ClaudeCLIAuthEnv + "=old-api-key",      // must be stripped
		ClaudeCLIOAuthEnv + "=old-oauth",       // must be stripped
		ClaudeCLIAuthTokenEnv + "=old-auth-token", // must be stripped
	}

	env := claudeEnv(baseEnv, "", "", proxyURL, "/tmp/home")

	// Assert ANTHROPIC_BASE_URL IS present with the proxy URL.
	var foundBaseURL bool
	var baseURLValue string
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIBaseURLEnv+"=") {
			foundBaseURL = true
			baseURLValue = strings.TrimPrefix(e, ClaudeCLIBaseURLEnv+"=")
		}
	}
	if !foundBaseURL {
		t.Fatalf("%s not found in env; env = %v", ClaudeCLIBaseURLEnv, env)
	}
	if baseURLValue != proxyURL {
		t.Fatalf("%s = %q, want %q", ClaudeCLIBaseURLEnv, baseURLValue, proxyURL)
	}

	// Assert ANTHROPIC_AUTH_TOKEN is set to placeholder (TC-101: now required).
	var foundAuthToken bool
	var authTokenValue string
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			foundAuthToken = true
			authTokenValue = strings.TrimPrefix(e, ClaudeCLIAuthTokenEnv+"=")
		}
	}
	if !foundAuthToken {
		t.Fatalf("%s not found in local mode env; expected placeholder", ClaudeCLIAuthTokenEnv)
	}
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("%s = %q, want %q (placeholder)", ClaudeCLIAuthTokenEnv, authTokenValue, LocalProxyAuthPlaceholder)
	}

	// Assert ANTHROPIC_API_KEY is ABSENT (not used in local mode).
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("API key %s must be absent in local mode; got: %s", ClaudeCLIAuthEnv, e)
		}
	}

	// Assert CLAUDE_CODE_OAUTH_TOKEN is ABSENT (no OAuth in local mode).
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			t.Fatalf("OAuth token %s must be absent in local mode; got: %s", ClaudeCLIOAuthEnv, e)
		}
	}
}

// TC-091-03: ClaudeCLI from local entry sets ANTHROPIC_BASE_URL in subprocess env;
// no cloud auth (ANTHROPIC_API_KEY / CLAUDE_CODE_OAUTH_TOKEN) is injected.
//
// Design note on capturing cmd.Env:
// RunContext calls cmdFactory to get a *exec.Cmd, then OVERRIDES cmd.Env with
// claudeEnv(...). Because capture.set() stores a pointer to the same cmd, the
// captured cmd's Env after Run() returns reflects the OVERRIDDEN (claudeEnv) value —
// which is exactly what we want to assert against. The subprocess itself runs with
// that env (no GO_WANT_HELPER_PROCESS), so we use "/bin/true" (exit 0, instant) and
// write the branch file synchronously in the factory before returning the cmd.
func TestClaudeCLI_LocalEntry_SetsBaseURLAndNoCloudAuth(t *testing.T) {
	const proxyURL = "http://localhost:8080"
	const modelID = "qwen2.5-coder-7b-instruct"
	const expectedBranch = "task/091-local-test-branch"

	entry := registry.RegistryEntry{
		ID:        "local-qwen",
		Harness:   registry.HarnessClaudeCLI,
		ModelID:   modelID,
		Endpoint:  proxyURL,
		SecretRef: "", // no cloud auth
	}

	worktree := t.TempDir()
	src := &fakeLocalSecretSource{}
	capture := &capturedCmd{}

	cli := NewClaudeCLIFromEntry(entry, src, worktree)
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Extract the branch file path from the prompt arg ("-p <prompt>").
		// The prompt contains "write only the produced branch name to this file:\n<path>".
		// Write the branch file synchronously here so RunContext can read it after the
		// subprocess exits. The subprocess itself is "/bin/true" (exits 0 instantly).
		if len(args) >= 2 && args[0] == "-p" {
			prompt := args[1]
			const marker = "write only the produced branch name to this file:\n"
			if idx := strings.Index(prompt, marker); idx >= 0 {
				path := strings.TrimSpace(prompt[idx+len(marker):])
				_ = os.WriteFile(path, []byte(expectedBranch+"\n"), 0o600)
			}
		}
		// Use /bin/true: exits 0 instantly, does not read env, does not produce output.
		// RunContext will override cmd.Env after this factory returns — the captured cmd
		// pointer lets us read that overridden env in our assertions below.
		cmd := exec.CommandContext(ctx, "/bin/true")
		capture.set(cmd)
		return cmd
	}

	task := supervisor.Task{ID: "091", Repo: "agent-builder", Spec: "docs/tasks/backlog/091-local-entry-translation-proxy.md"}

	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK = false, want true")
	}
	if result.Branch != expectedBranch {
		t.Fatalf("result.Branch = %q, want %q", result.Branch, expectedBranch)
	}

	// Assert cmd.Env (as set by claudeEnv after factory returned) contains ANTHROPIC_BASE_URL.
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	var foundBaseURL bool
	var baseURLValue string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, ClaudeCLIBaseURLEnv+"=") {
			foundBaseURL = true
			baseURLValue = strings.TrimPrefix(e, ClaudeCLIBaseURLEnv+"=")
		}
	}
	if !foundBaseURL {
		t.Fatalf("%s not found in subprocess env; env was: %v", ClaudeCLIBaseURLEnv, cmd.Env)
	}
	if baseURLValue != proxyURL {
		t.Fatalf("%s = %q, want %q", ClaudeCLIBaseURLEnv, baseURLValue, proxyURL)
	}

	// Assert ANTHROPIC_AUTH_TOKEN is set to placeholder (TC-101: now required).
	// ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN must be ABSENT.
	var foundAuthToken bool
	var authTokenValue string
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			foundAuthToken = true
			authTokenValue = strings.TrimPrefix(e, ClaudeCLIAuthTokenEnv+"=")
		}
	}
	if !foundAuthToken {
		t.Fatalf("%s not found in subprocess env for local entry; expected placeholder", ClaudeCLIAuthTokenEnv)
	}
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("%s = %q, want %q (placeholder)", ClaudeCLIAuthTokenEnv, authTokenValue, LocalProxyAuthPlaceholder)
	}

	// Assert ANTHROPIC_API_KEY is ABSENT in local mode.
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("API key %s must not be present for local entry; got: %s", ClaudeCLIAuthEnv, e)
		}
	}

	// Assert CLAUDE_CODE_OAUTH_TOKEN is still ABSENT.
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			t.Fatalf("OAuth token %s must not be present for local entry; got: %s", ClaudeCLIOAuthEnv, e)
		}
	}
}

// TC-091-05: NewClaudeCLIFromEntry compiles; existing call sites unchanged.
func TestClaudeCLIFromEntry_CompilationAndBackwardCompat(t *testing.T) {
	// TC-091-05: NewClaudeCLIFromEntry accepts a RegistryEntry and SecretSource.
	// This compile-time test confirms the signature is correct.
	entry := registry.RegistryEntry{
		ID:        "local-qwen",
		Harness:   registry.HarnessClaudeCLI,
		ModelID:   "qwen2.5-coder-7b-instruct",
		Endpoint:  "http://localhost:8080",
		SecretRef: "",
	}
	src := &fakeLocalSecretSource{}
	cli := NewClaudeCLIFromEntry(entry, src, "/tmp/worktree")
	if cli == nil {
		t.Fatal("NewClaudeCLIFromEntry() returned nil")
	}

	// TC-091-05: existing NewClaudeCLI still compiles (backward compat).
	config := ClaudeCLIConfig{
		CLIPath:    "claude",
		Worktree:   "/tmp/work",
		OAuthToken: "oauth-token-123",
	}
	cli2 := NewClaudeCLI(config)
	if cli2 == nil {
		t.Fatal("NewClaudeCLI() returned nil (backward compat check)")
	}

	// TC-091-05: local entry has baseURL set from Endpoint; cloud entry has no baseURL.
	if cli.baseURL != "http://localhost:8080" {
		t.Errorf("local entry baseURL = %q, want %q", cli.baseURL, "http://localhost:8080")
	}
	if cli2.baseURL != "" {
		t.Errorf("cloud entry baseURL = %q, want empty", cli2.baseURL)
	}
}

func TestNewClaudeCLIFromSecretSourceDelegatesToSecretSource(t *testing.T) {
	tests := []struct {
		name           string
		authToken      string
		oauthToken     string
		wantAuthToken  string
		wantOAuthToken string
	}{
		{
			name:           "API key from fake source",
			authToken:      "sk-fake",
			oauthToken:     "",
			wantAuthToken:  "sk-fake",
			wantOAuthToken: "",
		},
		{
			name:           "OAuth token from fake source",
			authToken:      "",
			oauthToken:     "oauth-fake",
			wantAuthToken:  "",
			wantOAuthToken: "oauth-fake",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeSecretSource{
				authToken:  tt.authToken,
				oauthToken: tt.oauthToken,
			}
			cli := NewClaudeCLIFromSecretSource("/tmp/work", fake)

			if cli == nil {
				t.Fatal("NewClaudeCLIFromSecretSource() returned nil")
			}
			if cli.authToken != tt.wantAuthToken {
				t.Fatalf("authToken = %q, want %q", cli.authToken, tt.wantAuthToken)
			}
			if cli.oauthToken != tt.wantOAuthToken {
				t.Fatalf("oauthToken = %q, want %q", cli.oauthToken, tt.wantOAuthToken)
			}
		})
	}
}

// TC-101-01: claudeEnv (local mode) injects placeholder sentinel as ANTHROPIC_AUTH_TOKEN
func TestClaudeEnvLocalModeInjectsPlaceholder(t *testing.T) {
	baseEnv := []string{
		"PATH=/usr/bin",
		ClaudeCLIAuthEnv + "=old-api-key",
		ClaudeCLIOAuthEnv + "=old-oauth",
	}
	env := claudeEnv(baseEnv, "real-operator-key", "real-oauth", "http://localhost:8080", "/tmp/h")

	// Verify ANTHROPIC_AUTH_TOKEN is set to the placeholder
	authTokenFound := false
	authTokenValue := ""
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			authTokenFound = true
			authTokenValue = strings.TrimPrefix(e, ClaudeCLIAuthTokenEnv+"=")
		}
	}

	if !authTokenFound {
		t.Fatal("ANTHROPIC_AUTH_TOKEN not found in env")
	}
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want %q", authTokenValue, LocalProxyAuthPlaceholder)
	}

	// Verify ANTHROPIC_BASE_URL is set
	baseURLFound := false
	baseURLValue := ""
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIBaseURLEnv+"=") {
			baseURLFound = true
			baseURLValue = strings.TrimPrefix(e, ClaudeCLIBaseURLEnv+"=")
		}
	}

	if !baseURLFound {
		t.Fatal("ANTHROPIC_BASE_URL not found in env")
	}
	if baseURLValue != "http://localhost:8080" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", baseURLValue, "http://localhost:8080")
	}

	// Verify ANTHROPIC_API_KEY is NOT present
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("ANTHROPIC_API_KEY should not be in local mode env: %s", e)
		}
	}

	// Verify CLAUDE_CODE_OAUTH_TOKEN is NOT present
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN should not be in local mode env: %s", e)
		}
	}

	// Verify real credentials are not in the env
	envJoined := strings.Join(env, " ")
	if strings.Contains(envJoined, "real-operator-key") {
		t.Fatal("real operator auth token found in env")
	}
	if strings.Contains(envJoined, "real-oauth") {
		t.Fatal("real oauth token found in env")
	}
}

// TC-101-02: claudeEnv (local mode) placeholder is distinct from any real operator token
func TestClaudeEnvLocalModePlaceholderNotRealKey(t *testing.T) {
	baseEnv := []string{"PATH=/usr/bin"}
	env := claudeEnv(baseEnv, "sk-ant-realkey", "", "http://localhost:8080", "/tmp/h")

	authTokenValue := ""
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			authTokenValue = strings.TrimPrefix(e, ClaudeCLIAuthTokenEnv+"=")
		}
	}

	// Assert the placeholder value equals LocalProxyAuthPlaceholder
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want %q", authTokenValue, LocalProxyAuthPlaceholder)
	}

	// Assert the placeholder is NOT the operator's real key (security invariant)
	if authTokenValue == "sk-ant-realkey" {
		t.Fatal("injectedToken == realOperatorKey: security invariant violated")
	}
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("injectedToken != LocalProxyAuthPlaceholder: %q != %q", authTokenValue, LocalProxyAuthPlaceholder)
	}
}

// TC-101-03: claudeEnv (cloud mode) is unchanged: no placeholder, real credential injected
func TestClaudeEnvCloudModeUnchanged(t *testing.T) {
	// Test 1: API key in cloud mode (baseURL empty)
	baseEnv := []string{"PATH=/usr/bin"}
	env := claudeEnv(baseEnv, "sk-ant-prod", "", "", "/tmp/h")

	// Should have real API key, not placeholder
	apiKeyFound := false
	apiKeyValue := ""
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			apiKeyFound = true
			apiKeyValue = strings.TrimPrefix(e, ClaudeCLIAuthEnv+"=")
		}
	}

	if !apiKeyFound {
		t.Fatal("ANTHROPIC_API_KEY not found in cloud mode")
	}
	if apiKeyValue != "sk-ant-prod" {
		t.Fatalf("ANTHROPIC_API_KEY = %q, want %q", apiKeyValue, "sk-ant-prod")
	}

	// Should NOT have placeholder
	if apiKeyValue == LocalProxyAuthPlaceholder {
		t.Fatal("cloud mode should not have placeholder")
	}

	// Should NOT have ANTHROPIC_BASE_URL
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIBaseURLEnv+"=") {
			t.Fatalf("ANTHROPIC_BASE_URL should not be in cloud mode env: %s", e)
		}
	}

	// Should NOT have ANTHROPIC_AUTH_TOKEN in cloud mode
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be in cloud mode env: %s", e)
		}
	}

	// Test 2: OAuth token in cloud mode (OAuth preferred)
	env2 := claudeEnv(baseEnv, "sk-ant-prod", "oauth-tok", "", "/tmp/h")

	oauthFound := false
	oauthValue := ""
	for _, e := range env2 {
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			oauthFound = true
			oauthValue = strings.TrimPrefix(e, ClaudeCLIOAuthEnv+"=")
		}
	}

	if !oauthFound {
		t.Fatal("CLAUDE_CODE_OAUTH_TOKEN not found in cloud mode with OAuth")
	}
	if oauthValue != "oauth-tok" {
		t.Fatalf("CLAUDE_CODE_OAUTH_TOKEN = %q, want %q", oauthValue, "oauth-tok")
	}

	// Should NOT have ANTHROPIC_API_KEY (OAuth preferred)
	for _, e := range env2 {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("ANTHROPIC_API_KEY should not be present when OAuth is preferred: %s", e)
		}
	}

	// Should NOT have ANTHROPIC_AUTH_TOKEN in cloud mode
	for _, e := range env2 {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			t.Fatalf("ANTHROPIC_AUTH_TOKEN should not be in cloud mode env: %s", e)
		}
	}

	// Should NOT have placeholder
	if oauthValue == LocalProxyAuthPlaceholder {
		t.Fatal("cloud mode should not have placeholder")
	}
}

// TC-101-06 (L2 part): End-to-end intent with NewClaudeCLIFromEntry local mode
func TestNewClaudeCLIFromEntryLocalModeEnv(t *testing.T) {
	// Create a local entry (SecretRef == "")
	localEntry := registry.RegistryEntry{
		ID:        "local",
		Endpoint:  "http://localhost:8080",
		ModelID:   "qwen2.5-coder:7b",
		SecretRef: "", // Empty = local entry
	}

	// Create a fake secret source (should not be used for local entries)
	fakeSource := &fakeSecretSource{
		authToken:  "sk-should-not-be-used",
		oauthToken: "oauth-should-not-be-used",
	}

	// Create the CLI from the local entry
	cli := NewClaudeCLIFromEntry(localEntry, fakeSource, "/tmp/work")

	if cli == nil {
		t.Fatal("NewClaudeCLIFromEntry() returned nil")
	}

	// Verify the CLI has the correct baseURL
	if cli.baseURL != "http://localhost:8080" {
		t.Fatalf("cli.baseURL = %q, want %q", cli.baseURL, "http://localhost:8080")
	}

	// Verify the CLI has no real credentials (they should not be stored for local entries)
	// For local entries, authToken and oauthToken are empty in the CLI object
	if cli.authToken != "" {
		t.Fatalf("cli.authToken should be empty for local entry, got %q", cli.authToken)
	}
	if cli.oauthToken != "" {
		t.Fatalf("cli.oauthToken should be empty for local entry, got %q", cli.oauthToken)
	}

	// Create the environment to verify the placeholder is injected at Run time
	tempHome := t.TempDir()
	testEnv := claudeEnv(os.Environ(), cli.authToken, cli.oauthToken, cli.baseURL, tempHome)

	// Verify ANTHROPIC_AUTH_TOKEN is set to placeholder
	authTokenFound := false
	authTokenValue := ""
	for _, e := range testEnv {
		if strings.HasPrefix(e, ClaudeCLIAuthTokenEnv+"=") {
			authTokenFound = true
			authTokenValue = strings.TrimPrefix(e, ClaudeCLIAuthTokenEnv+"=")
		}
	}

	if !authTokenFound {
		t.Fatal("ANTHROPIC_AUTH_TOKEN not found when using local entry")
	}
	if authTokenValue != LocalProxyAuthPlaceholder {
		t.Fatalf("ANTHROPIC_AUTH_TOKEN = %q, want %q", authTokenValue, LocalProxyAuthPlaceholder)
	}

	// Verify ANTHROPIC_BASE_URL is set to the local endpoint
	baseURLFound := false
	baseURLValue := ""
	for _, e := range testEnv {
		if strings.HasPrefix(e, ClaudeCLIBaseURLEnv+"=") {
			baseURLFound = true
			baseURLValue = strings.TrimPrefix(e, ClaudeCLIBaseURLEnv+"=")
		}
	}

	if !baseURLFound {
		t.Fatal("ANTHROPIC_BASE_URL not found when using local entry")
	}
	if baseURLValue != "http://localhost:8080" {
		t.Fatalf("ANTHROPIC_BASE_URL = %q, want %q", baseURLValue, "http://localhost:8080")
	}

	// Verify ANTHROPIC_API_KEY is NOT present in local mode
	for _, e := range testEnv {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("ANTHROPIC_API_KEY should not be present in local mode: %s", e)
		}
	}
}

// TestClaudePromptIncludesFailureSectionWhenPriorFailureSet verifies that buildClaudePrompt
// includes the gate-failure section when task.PriorFailure is non-empty.
// TC-108-01
func TestClaudePromptIncludesFailureSectionWhenPriorFailureSet(t *testing.T) {
	task := supervisor.Task{
		ID:           "001",
		Repo:         "exec-sandbox",
		Spec:         "/tasks/001.md",
		PriorFailure: "Failed step: go-fmt\nOutput:\nbad_file.go\nFix these issues before producing the branch.",
	}
	prompt := buildClaudePrompt(task, "/worktree", "/tmp/branch.txt")

	// Assert: contains "previous attempt"
	if !strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt missing 'previous attempt', got:\n%s", prompt)
	}

	// Assert: contains "verification gate"
	if !strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt missing 'verification gate', got:\n%s", prompt)
	}

	// Assert: contains the step name from PriorFailure
	if !strings.Contains(prompt, "go-fmt") {
		t.Errorf("prompt missing 'go-fmt', got:\n%s", prompt)
	}

	// Assert: contains the step output from PriorFailure
	if !strings.Contains(prompt, "bad_file.go") {
		t.Errorf("prompt missing 'bad_file.go', got:\n%s", prompt)
	}

	// Assert: contains "Fix these issues"
	if !strings.Contains(prompt, "Fix these issues") {
		t.Errorf("prompt missing 'Fix these issues', got:\n%s", prompt)
	}
}

// TestClaudePromptOmitsFailureSectionWhenPriorFailureEmpty verifies that buildClaudePrompt
// OMITS the gate-failure section when task.PriorFailure is empty.
// TC-108-02
func TestClaudePromptOmitsFailureSectionWhenPriorFailureEmpty(t *testing.T) {
	task := supervisor.Task{
		ID:   "001",
		Repo: "exec-sandbox",
		Spec: "/tasks/001.md",
		// PriorFailure is zero-value ""
	}
	prompt := buildClaudePrompt(task, "/worktree", "/tmp/branch.txt")

	// Assert: does NOT contain "previous attempt"
	if strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt should not contain 'previous attempt' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: does NOT contain "verification gate"
	if strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt should not contain 'verification gate' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: does NOT contain "Fix these issues"
	if strings.Contains(prompt, "Fix these issues") {
		t.Errorf("prompt should not contain 'Fix these issues' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: core content is present
	if !strings.Contains(prompt, "Task ID: 001") {
		t.Errorf("core prompt missing 'Task ID: 001', got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "/worktree") {
		t.Errorf("core prompt missing '/worktree', got:\n%s", prompt)
	}
}
