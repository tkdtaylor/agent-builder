package vault_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// liveVaultEnv is the gate flag for tests that need a real vault binary.
const liveVaultEnv = "AGENT_BUILDER_LIVE_VAULT"

// requireLiveVault skips unless AGENT_BUILDER_LIVE_VAULT=1 and a vault binary is
// resolvable. It returns the binary path and a master key fit for an ephemeral
// in-memory store.
func requireLiveVault(t *testing.T) (binPath, masterKey string) {
	t.Helper()
	if os.Getenv(liveVaultEnv) != "1" {
		t.Skipf("live vault test skipped; set %s=1 and AGENT_BUILDER_VAULT_BIN to run", liveVaultEnv)
	}
	binPath = strings.TrimSpace(os.Getenv("AGENT_BUILDER_VAULT_BIN"))
	if binPath == "" {
		t.Fatalf("%s=1 but AGENT_BUILDER_VAULT_BIN is unset", liveVaultEnv)
	}
	if _, err := os.Stat(binPath); err != nil {
		t.Fatalf("vault binary %q not found: %v", binPath, err)
	}
	// Any 32-byte hex value is a valid ephemeral master key.
	masterKey = strings.Repeat("ab", 32)
	t.Setenv(vault.EnvMasterKey, masterKey)
	return binPath, masterKey
}

// startDaemon starts a vault daemon on a temp socket for live tests.
func startDaemon(t *testing.T, binPath string) *vault.Daemon {
	t.Helper()
	socketPath := filepath.Join(t.TempDir(), "vault.sock")
	d := &vault.Daemon{BinPath: binPath, SocketPath: socketPath}
	// Daemon.Start uses exec.CommandContext, so the daemon process is killed when
	// ctx is cancelled. Cancel must therefore outlive the test body (not fire when
	// startDaemon returns) or every post-Start request hits a dead daemon and gets
	// "connection reset by peer". Bind cancel + Stop to t.Cleanup instead of defer.
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := d.Start(ctx); err != nil {
		t.Fatalf("daemon.Start err = %v", err)
	}
	t.Cleanup(func() { _ = d.Stop() })
	return d
}

// TC-066-01: VaultClient put/resolve round-trip against a real vault daemon.
func TestVaultClientPutResolveRoundTrip(t *testing.T) {
	binPath, _ := requireLiveVault(t)
	daemon := startDaemon(t, binPath)
	client := vault.NewClient(daemon.SocketPath)

	if err := client.Ping(); err != nil {
		t.Fatalf("Ping err = %v", err)
	}

	gitBinding := vault.Binding{Host: "api.github.com", Header: "Authorization", Scheme: "Bearer", EnvVar: "GIT_TOKEN"}

	// Sub-case A: put git token.
	if err := client.Put("vault://agent-builder/git-token", "gittok-123", "proxy", gitBinding); err != nil {
		t.Fatalf("Put err = %v", err)
	}

	// Sub-case B: resolve returns an opaque handle, never the plaintext.
	res, err := client.Resolve("vault://agent-builder/git-token", 300)
	if err != nil {
		t.Fatalf("Resolve err = %v", err)
	}
	if res.Handle == "" {
		t.Fatal("Resolve returned empty handle")
	}
	if strings.Contains(res.Handle, "gittok-123") {
		t.Errorf("handle %q leaks plaintext", res.Handle)
	}
	if res.InjectionMode != "proxy" {
		t.Errorf("InjectionMode = %q, want proxy", res.InjectionMode)
	}
	if res.TTL != 300 {
		t.Errorf("TTL = %d, want 300", res.TTL)
	}

	// Sub-case C: resolve of an unknown ref fails.
	if _, err := client.Resolve("vault://agent-builder/no-such-ref", 300); err == nil {
		t.Error("Resolve of unknown ref: expected error, got nil")
	}

	// Sub-case D: put with env floor → resolve returns injection_mode "env".
	envBinding := vault.Binding{Host: "api.github.com", Header: "Authorization", Scheme: "Bearer", EnvVar: "ENV_TOKEN"}
	if err := client.Put("vault://agent-builder/env-token", "envtok", "env", envBinding); err != nil {
		t.Fatalf("Put (env floor) err = %v", err)
	}
	envRes, err := client.Resolve("vault://agent-builder/env-token", 300)
	if err != nil {
		t.Fatalf("Resolve (env floor) err = %v", err)
	}
	if envRes.InjectionMode != "env" {
		t.Errorf("env-floor InjectionMode = %q, want env", envRes.InjectionMode)
	}
}
