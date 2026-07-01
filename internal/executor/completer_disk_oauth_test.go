package executor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/secrets"
)

// TC-143-04: Claude completer injects disk OAuth via CompleterForEntry
// (LIVE PATH TEST — exercises the chained source wiring with real stubbing)
func TestClaudeCompleterInjectsDiskOAuth(t *testing.T) {
	// Create a temporary home with on-disk OAuth credentials
	tempHome := t.TempDir()

	claudeDir := filepath.Join(tempHome, ".claude")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	credJSON := `{"claudeAiOauth":{"accessToken":"tok-disk-143","refreshToken":"refresh-DO-NOT-READ"}}`
	credFile := filepath.Join(claudeDir, ".credentials.json")
	if err := os.WriteFile(credFile, []byte(credJSON), 0o600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	// Save and restore environment
	oldHome := os.Getenv("HOME")
	oldAPIKey := os.Getenv(secrets.EnvAnthropicAPIKey)
	oldOAuth := os.Getenv(secrets.EnvClaudeCodeOAuthToken)
	defer func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv(secrets.EnvAnthropicAPIKey, oldAPIKey)
		_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, oldOAuth)
	}()

	// Set HOME to the temp dir so disk source reads the creds
	_ = os.Setenv("HOME", tempHome)
	// Clear env tokens to force disk fallback
	_ = os.Setenv(secrets.EnvAnthropicAPIKey, "")
	_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, "")

	// Create a cloud entry (non-empty SecretRef)
	entry := testClaudeEntry("cloud-secret-ref", "")

	// Call the LIVE construction path: CompleterForEntry
	completer, err := CompleterForEntry(entry)
	if err != nil {
		t.Fatalf("CompleterForEntry failed: %v", err)
	}

	// Cast to claudeCompleter (now possible in internal package)
	cc := completer.(*claudeCompleter)

	// Stub the cmdFactory to capture the command
	cap := &capturedCmd{}
	cc.cmdFactory = stubClaudeCompleterFactory(t, "Paris", 0, cap)

	// Run Complete
	_, err = cc.Complete(context.Background(), entry, "What is the capital of France?")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Get the captured command's environment
	env := cap.get().Env

	// (a) Disk token injected on the OAuth env var
	if !envContains(env, ClaudeCLIOAuthEnv+"=tok-disk-143") {
		t.Errorf("child env missing disk OAuth token; env=%v", env)
	}

	// (b) HOME still isolated temp dir, NOT the creds dir
	var home string
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			home = strings.TrimPrefix(e, "HOME=")
		}
	}
	if home == "" || home == tempHome {
		t.Errorf("HOME not isolated (got %q, creds dir %q)", home, tempHome)
	}

	// (c) Token absent from argv
	_, args := cap.getAgyCommand()
	for _, a := range args {
		if strings.Contains(a, "tok-disk-143") {
			t.Errorf("token in argv: %q", a)
		}
	}

	// (d) Only accessToken read, never refresh
	if envContains(env, ClaudeCLIOAuthEnv+"=refresh-DO-NOT-READ") {
		t.Error("refresh token leaked")
	}
}

// TC-143-05: Claude completer failure redacts disk OAuth token via sanitizeCLIOutput
// (TESTS THE REAL REDACTION PATH)
func TestClaudeCompleterFailureRedactsDiskToken(t *testing.T) {
	// Create a temporary home with on-disk OAuth credentials
	tempHome := t.TempDir()

	claudeDir := filepath.Join(tempHome, ".claude")
	_ = os.MkdirAll(claudeDir, 0o700)

	credJSON := `{"claudeAiOauth":{"accessToken":"tok-disk-redact-143"}}`
	credFile := filepath.Join(claudeDir, ".credentials.json")
	_ = os.WriteFile(credFile, []byte(credJSON), 0o600)

	// Save and restore environment
	oldHome := os.Getenv("HOME")
	oldAPIKey := os.Getenv(secrets.EnvAnthropicAPIKey)
	oldOAuth := os.Getenv(secrets.EnvClaudeCodeOAuthToken)
	defer func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv(secrets.EnvAnthropicAPIKey, oldAPIKey)
		_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, oldOAuth)
	}()

	_ = os.Setenv("HOME", tempHome)
	_ = os.Setenv(secrets.EnvAnthropicAPIKey, "")
	_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, "")

	// Create a cloud entry
	entry := testClaudeEntry("cloud-secret-ref", "")

	// Get the completer via LIVE construction
	completer, err := CompleterForEntry(entry)
	if err != nil {
		t.Fatalf("CompleterForEntry failed: %v", err)
	}

	cc := completer.(*claudeCompleter)

	// Stub the CLI to ECHO the token and fail
	cc.cmdFactory = stubClaudeCompleterFactory(t, "boom tok-disk-redact-143 failed", 1, &capturedCmd{})

	// Run Complete — expect it to fail
	_, err = cc.Complete(context.Background(), entry, "hi")
	if err == nil {
		t.Fatal("expected error from Complete")
	}

	// The error string should NOT contain the raw disk token
	if strings.Contains(err.Error(), "tok-disk-redact-143") {
		t.Errorf("error leaked disk token: %q", err.Error())
	}
}

// TC-143-06: Local entries (empty SecretRef) do not access disk OAuth source
// (BOUNDARY TEST)
func TestLocalClaudeEntryDoesNotUseDiskOAuth(t *testing.T) {
	// Create a temporary home with on-disk OAuth credentials
	tempHome := t.TempDir()

	claudeDir := filepath.Join(tempHome, ".claude")
	_ = os.MkdirAll(claudeDir, 0o700)
	_ = os.WriteFile(filepath.Join(claudeDir, ".credentials.json"),
		[]byte(`{"claudeAiOauth":{"accessToken":"disk-token-local-test"}}`), 0o600)

	// Save and restore environment
	oldHome := os.Getenv("HOME")
	oldAPIKey := os.Getenv(secrets.EnvAnthropicAPIKey)
	oldOAuth := os.Getenv(secrets.EnvClaudeCodeOAuthToken)
	defer func() {
		_ = os.Setenv("HOME", oldHome)
		_ = os.Setenv(secrets.EnvAnthropicAPIKey, oldAPIKey)
		_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, oldOAuth)
	}()

	_ = os.Setenv("HOME", tempHome)
	_ = os.Setenv(secrets.EnvAnthropicAPIKey, "")
	_ = os.Setenv(secrets.EnvClaudeCodeOAuthToken, "")

	// Create a LOCAL entry (empty SecretRef)
	entry := testClaudeEntry("", "http://localhost:4000")

	// Get the completer via LIVE construction
	completer, err := CompleterForEntry(entry)
	if err != nil {
		t.Fatalf("CompleterForEntry failed: %v", err)
	}

	cc := completer.(*claudeCompleter)

	// Stub the cmdFactory to capture the command
	cap := &capturedCmd{}
	cc.cmdFactory = stubClaudeCompleterFactory(t, "ok", 0, cap)

	// Run Complete
	_, err = cc.Complete(context.Background(), entry, "test")
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	// Get the captured command's environment
	env := cap.get().Env

	// Local entries should NOT have disk OAuth token in env
	// (they use the translation proxy path instead)
	if envContains(env, ClaudeCLIOAuthEnv+"=disk-token-local-test") {
		t.Error("local entry should not access disk OAuth token")
	}

	// Local entries SHOULD have the placeholder auth token
	if !envContains(env, ClaudeCLIAuthTokenEnv+"="+LocalProxyAuthPlaceholder) {
		t.Error("local entry should have placeholder auth token")
	}

	// Local entries SHOULD have the custom endpoint
	if !envContains(env, ClaudeCLIBaseURLEnv+"=http://localhost:4000") {
		t.Error("local entry should have custom endpoint")
	}
}
