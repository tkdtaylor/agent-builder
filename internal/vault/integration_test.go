package vault_test

import (
	"os"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/execsandbox"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// liveExecSandboxEnv gates tests that need the real exec-sandbox binary.
const liveExecSandboxEnv = "AGENT_BUILDER_LIVE_EXEC_SANDBOX"

// requireLiveExecSandbox returns the exec-sandbox binary path or skips.
func requireLiveExecSandbox(t *testing.T) string {
	t.Helper()
	if os.Getenv(liveExecSandboxEnv) != "1" {
		t.Skipf("live exec-sandbox test skipped; set %s=1 and AGENT_BUILDER_EXEC_SANDBOX_BIN to run", liveExecSandboxEnv)
	}
	bin := strings.TrimSpace(os.Getenv("AGENT_BUILDER_EXEC_SANDBOX_BIN"))
	if bin == "" {
		t.Fatalf("%s=1 but AGENT_BUILDER_EXEC_SANDBOX_BIN is unset", liveExecSandboxEnv)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("exec-sandbox binary %q not found: %v", bin, err)
	}
	return bin
}

// TC-066-05: git/GitHub token brokered through vault proxy; the raw token value
// never appears in the run, and secrets_injected is non-empty.
//
// This test exercises the full host-side path: real vault daemon → register git
// token → resolve handle → sandbox.Request with handle in Wiring.SecretRefs and
// injection_mode="proxy" → real exec-sandbox binary. The raw token plaintext is
// asserted absent from the sandbox result (stdout/stderr) — it lives only in
// vault and is injected by the proxy edge inside the box, never in the request.
func TestVaultGitHubTokenProxyRoundTrip(t *testing.T) {
	binPath, _ := requireLiveVault(t)
	execBin := requireLiveExecSandbox(t)

	rawToken := strings.TrimSpace(os.Getenv("AGENT_BUILDER_GIT_TOKEN"))
	if rawToken == "" {
		rawToken = "live-git-token-sentinel-value"
	}

	daemon := startDaemon(t, binPath)
	client := vault.NewClient(daemon.SocketPath)

	src, err := secrets.NewVaultSecretSource(client, secrets.VaultSourceConfig{GitToken: rawToken})
	if err != nil {
		t.Fatalf("NewVaultSecretSource err = %v", err)
	}
	handles := src.Handles()
	if len(handles) != 1 {
		t.Fatalf("Handles() = %v, want 1 handle", handles)
	}

	// SECURITY: the handle must not contain the raw token.
	if strings.Contains(handles[0], rawToken) {
		t.Fatalf("handle leaks the raw token")
	}

	runner := execsandbox.New(execBin)
	req := sandbox.Request{
		Command:  []string{"sh", "-c", "echo token-check-placeholder"},
		Worktree: t.TempDir(),
		Wiring: sandbox.RunWiring{
			VaultSocket:   daemon.SocketPath,
			SecretRefs:    handles,
			InjectionMode: "proxy",
		},
	}

	result, exitCode, err := runner.Run(req)
	if err != nil {
		t.Fatalf("exec-sandbox Run err = %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%s", exitCode, result.Stderr)
	}

	// The raw token must not appear in any surfaced output.
	if strings.Contains(result.Stdout, rawToken) || strings.Contains(result.Stderr, rawToken) {
		t.Errorf("raw token leaked into sandbox output")
	}

	t.Logf("TC-066-05 sandbox status=%q tier=%q", result.Status, result.Tier)
	// Operator note: assert sandbox_status.secrets_injected is non-empty via the
	// run record / block output when running this live; the typed Result does not
	// surface secrets_injected, so the operator confirms it from the block log.
}
