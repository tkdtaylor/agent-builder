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

// TC-091-03a: claudeEnv with baseURL set injects ANTHROPIC_BASE_URL and omits cloud auth.
// This is the primary env-contract assertion: directly tests the env-building function.
func TestClaudeEnv_LocalMode_SetsBaseURLAndOmitsCloudAuth(t *testing.T) {
	const proxyURL = "http://localhost:8080"
	baseEnv := []string{
		"PATH=/usr/bin",
		ClaudeCLIAuthEnv + "=old-api-key",  // must be stripped
		ClaudeCLIOAuthEnv + "=old-oauth",   // must be stripped
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

	// Assert cloud auth env vars are ABSENT (no-cloud-auth invariant).
	for _, e := range env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("cloud auth %s must be absent in local mode; got: %s", ClaudeCLIAuthEnv, e)
		}
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			t.Fatalf("cloud auth %s must be absent in local mode; got: %s", ClaudeCLIOAuthEnv, e)
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

	// Assert ANTHROPIC_API_KEY and CLAUDE_CODE_OAUTH_TOKEN are ABSENT (no-cloud-auth invariant).
	for _, e := range cmd.Env {
		if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
			t.Fatalf("cloud auth env %s must not be present for local entry; got: %s", ClaudeCLIAuthEnv, e)
		}
		if strings.HasPrefix(e, ClaudeCLIOAuthEnv+"=") {
			t.Fatalf("cloud auth env %s must not be present for local entry; got: %s", ClaudeCLIOAuthEnv, e)
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
