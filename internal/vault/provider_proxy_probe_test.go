package vault_test

import (
	"os"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/execsandbox"
	"github.com/tkdtaylor/agent-builder/internal/vault"
)

// TC-066-07: feasibility probe — can the Claude CLI authenticate inside the box
// with the provider token ABSENT from the box env and present only on the egress
// proxy as Authorization: Bearer? This is NOT a regression gate; its outcome
// (PASS/BLOCK) is recorded as evidence for the follow-on provider-token
// brokering task. Task 066 is approved regardless of this result.
//
// Gated on AGENT_BUILDER_VAULT_PROVIDER_PROBE=1 (operator-run only).
func TestProviderTokenProxyFeasibility(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_VAULT_PROVIDER_PROBE") != "1" {
		t.Skip("provider proxy feasibility probe skipped; set AGENT_BUILDER_VAULT_PROVIDER_PROBE=1 to run")
	}
	binPath, _ := requireLiveVault(t)
	execBin := requireLiveExecSandbox(t)

	token := strings.TrimSpace(os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY"))
	}
	if token == "" {
		t.Fatal("probe requires CLAUDE_CODE_OAUTH_TOKEN or ANTHROPIC_API_KEY set")
	}

	daemon := startDaemon(t, binPath)
	client := vault.NewClient(daemon.SocketPath)

	binding := vault.Binding{
		Host:   "api.anthropic.com",
		Header: "Authorization",
		Scheme: "Bearer",
		EnvVar: "CLAUDE_CODE_OAUTH_TOKEN",
	}
	if err := client.Put("vault://agent-builder/claude-oauth", token, "proxy", binding); err != nil {
		t.Fatalf("Put claude-oauth err = %v", err)
	}
	res, err := client.Resolve("vault://agent-builder/claude-oauth", 300)
	if err != nil {
		t.Fatalf("Resolve claude-oauth err = %v", err)
	}

	runner := execsandbox.New(execBin)
	// Payload invokes the Claude CLI with NO provider token in the box env; the
	// token reaches api.anthropic.com only via the proxy-injected header.
	req := sandbox.Request{
		Command:  []string{"sh", "-c", `claude -p "Reply with exactly the word: PROXY_OK"`},
		Worktree: t.TempDir(),
		Limits: sandbox.Limits{
			EgressAllowlist: []string{"api.anthropic.com:443"},
		},
		Wiring: sandbox.RunWiring{
			VaultSocket:   daemon.SocketPath,
			SecretRefs:    []string{res.Handle},
			InjectionMode: "proxy",
		},
	}

	result, exitCode, runErr := runner.Run(req)

	// Both outcomes are valid evidence — record, do not fail the build.
	if runErr == nil && exitCode == 0 && strings.Contains(result.Stdout, "PROXY_OK") {
		t.Logf("TC-066-07 L6 PASS: Claude CLI authenticated via proxy-injected OAuth token with token absent from sandbox env; stdout=%q", result.Stdout)
		t.Log("RESULT: PASS — unlocks the follow-on provider-token brokering task")
		return
	}
	t.Logf("TC-066-07 L6 BLOCKED: Claude CLI did not authenticate via proxy alone; exit=%d runErr=%v stdout=%q stderr=%q", exitCode, runErr, result.Stdout, result.Stderr)
	t.Log("RESULT: BLOCK — provider-token brokering deferred; git/GitHub brokering (TC-066-05/06) unaffected")
}
