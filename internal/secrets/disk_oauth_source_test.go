package secrets_test

import (
	"os"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/secrets"
)

// TC-143-01: DiskOAuthSecretSource reads accessToken from .claude/.credentials.json
func TestDiskOAuthSourceReadsAccessToken(t *testing.T) {
	// Create a temporary home directory with .claude/.credentials.json
	tmpHome := t.TempDir()
	claudeDir := tmpHome + "/.claude"
	if err := os.MkdirAll(claudeDir, 0755); err != nil {
		t.Fatalf("failed to create .claude dir: %v", err)
	}

	credFile := claudeDir + "/.credentials.json"
	credJSON := `{"claudeAiOauth":{"accessToken":"tok-abc","refreshToken":"ref-xyz"}}`
	if err := os.WriteFile(credFile, []byte(credJSON), 0600); err != nil {
		t.Fatalf("failed to write credentials file: %v", err)
	}

	src := secrets.NewDiskOAuthSecretSourceWithHome(tmpHome)
	gotAuth, gotOAuth := src.ProviderToken()

	if gotAuth != "" {
		t.Fatalf("ProviderToken() authToken = %q, want empty", gotAuth)
	}
	if gotOAuth != "tok-abc" {
		t.Fatalf("ProviderToken() oauthToken = %q, want tok-abc", gotOAuth)
	}
}

// TC-143-02: DiskOAuthSecretSource gracefully handles absence (missing/malformed/empty)
func TestDiskOAuthSourceGracefulAbsence(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(tmpHome string) // prepare the home dir
		wantErr bool
	}{
		{
			name: "no credentials file",
			setup: func(tmpHome string) {
				// Leave .claude dir empty or non-existent
			},
			wantErr: false,
		},
		{
			name: "credentials dir does not exist",
			setup: func(tmpHome string) {
				// Do not create .claude directory at all
			},
			wantErr: false,
		},
		{
			name: "malformed JSON",
			setup: func(tmpHome string) {
				claudeDir := tmpHome + "/.claude"
				_ = os.MkdirAll(claudeDir, 0755)
				credFile := claudeDir + "/.credentials.json"
				_ = os.WriteFile(credFile, []byte("{invalid json"), 0600)
			},
			wantErr: false,
		},
		{
			name: "missing claudeAiOauth field",
			setup: func(tmpHome string) {
				claudeDir := tmpHome + "/.claude"
				_ = os.MkdirAll(claudeDir, 0755)
				credFile := claudeDir + "/.credentials.json"
				_ = os.WriteFile(credFile, []byte("{}"), 0600)
			},
			wantErr: false,
		},
		{
			name: "empty accessToken",
			setup: func(tmpHome string) {
				claudeDir := tmpHome + "/.claude"
				_ = os.MkdirAll(claudeDir, 0755)
				credFile := claudeDir + "/.credentials.json"
				_ = os.WriteFile(credFile, []byte(`{"claudeAiOauth":{"accessToken":""}}`), 0600)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpHome := t.TempDir()
			tt.setup(tmpHome)

			src := secrets.NewDiskOAuthSecretSourceWithHome(tmpHome)
			gotAuth, gotOAuth := src.ProviderToken()

			if (gotAuth != "" || gotOAuth != "") && !tt.wantErr {
				t.Fatalf("ProviderToken() = %q,%q, want empty,empty (no error)", gotAuth, gotOAuth)
			}
		})
	}
}

// TC-143-03: ChainedSecretSource precedence (env wins; disk only when env empty)
func TestChainedSourcePrecedence(t *testing.T) {
	tests := []struct {
		name        string
		envAuth     string
		envOAuth    string
		diskOAuth   string
		wantAuth    string
		wantOAuth   string
		desc        string
	}{
		{
			name:      "env OAuth + disk OAuth → env wins",
			envAuth:   "",
			envOAuth:  "env-tok",
			diskOAuth: "disk-tok",
			wantAuth:  "",
			wantOAuth: "env-tok",
			desc:      "env OAuth preferred",
		},
		{
			name:      "env API key only → no disk consult",
			envAuth:   "sk-key",
			envOAuth:  "",
			diskOAuth: "disk-tok",
			wantAuth:  "sk-key",
			wantOAuth: "",
			desc:      "env API key is credential; disk not consulted",
		},
		{
			name:      "env both empty + disk OAuth → disk used",
			envAuth:   "",
			envOAuth:  "",
			diskOAuth: "disk-tok",
			wantAuth:  "",
			wantOAuth: "disk-tok",
			desc:      "disk fallback when env empty",
		},
		{
			name:      "env + disk both empty → empty",
			envAuth:   "",
			envOAuth:  "",
			diskOAuth: "",
			wantAuth:  "",
			wantOAuth: "",
			desc:      "no credential available",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			envSrc := &FakeSecretSource{
				AuthToken:  tt.envAuth,
				OAuthToken: tt.envOAuth,
			}
			diskSrc := &FakeSecretSource{
				OAuthToken: tt.diskOAuth,
			}

			chain := secrets.NewChainedSecretSource(envSrc, diskSrc)
			gotAuth, gotOAuth := chain.ProviderToken()

			if gotAuth != tt.wantAuth || gotOAuth != tt.wantOAuth {
				t.Fatalf("%s: ProviderToken() = %q,%q, want %q,%q", tt.desc, gotAuth, gotOAuth, tt.wantAuth, tt.wantOAuth)
			}
		})
	}
}

// TC-143-02 and TC-143-04 helpers: empty home path should return no token
func TestDiskOAuthSourceEmptyHome(t *testing.T) {
	src := secrets.NewDiskOAuthSecretSourceWithHome("")
	gotAuth, gotOAuth := src.ProviderToken()

	if gotAuth != "" || gotOAuth != "" {
		t.Fatalf("ProviderToken() with empty home = %q,%q, want empty,empty", gotAuth, gotOAuth)
	}
}
