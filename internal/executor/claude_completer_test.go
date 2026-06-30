package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/registry"
)

// testClaudeEntry returns a Claude-harness registry entry. An empty secretRef is a
// local (translation-proxy) entry; a non-empty secretRef is a cloud entry.
func testClaudeEntry(secretRef, endpoint string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:        "claude-test",
		Harness:   registry.HarnessClaudeCLI,
		ModelID:   "claude-opus-4-5",
		Endpoint:  endpoint,
		SecretRef: secretRef,
	}
}

// fakeClaudeSecretSource returns fixed provider tokens for cloud-entry tests.
type fakeClaudeSecretSource struct {
	authToken  string
	oauthToken string
}

func (f *fakeClaudeSecretSource) ProviderToken() (string, string)         { return f.authToken, f.oauthToken }
func (f *fakeClaudeSecretSource) PublisherTokens() (string, string)       { return "", "" }
func (f *fakeClaudeSecretSource) NamedProviderToken(string) (string, error) { return "", nil }

// stubClaudeCompleterFactory re-invokes the test binary as a stub subprocess
// (TestMain/runHelperProcess via GO_WANT_HELPER_PROCESS) and captures the real
// (name, args) plus the created *exec.Cmd for assertion.
func stubClaudeCompleterFactory(t *testing.T, stdout string, exitCode int, cap *capturedCmd) claudeCommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if cap != nil {
			cap.setAgyCommand(name, args)
		}
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stdout,
			fmt.Sprintf("CODEX_HELPER_EXIT=%d", exitCode),
			"GO_WANT_HELPER_PROCESS=1",
		}
		if cap != nil {
			cap.set(cmd)
		}
		return cmd
	}
}

// TC-135-01: CompleterForEntry returns a non-nil completer for HarnessClaudeCLI.
func TestCompleterForEntryClaudeReturnsCompleter(t *testing.T) {
	c, err := CompleterForEntry(testClaudeEntry("", "http://localhost:4000"))
	if err != nil {
		t.Fatalf("CompleterForEntry(claude) error = %v, want nil", err)
	}
	if c == nil {
		t.Fatal("CompleterForEntry(claude) returned nil completer")
	}
	if errors.Is(err, ErrSingleShotUnsupported) {
		t.Fatal("claude single-shot should be supported, got ErrSingleShotUnsupported")
	}
}

// TC-135-02: claudeCompleter runs `claude -p <prompt>` and returns trimmed stdout.
func TestClaudeCompleterRunsPrintModeAndReturnsStdout(t *testing.T) {
	cap := &capturedCmd{}
	comp := newClaudeCompleter(testClaudeEntry("", "http://localhost:4000"), &fakeClaudeSecretSource{})
	comp.cmdFactory = stubClaudeCompleterFactory(t, "  Paris\n", 0, cap)

	got, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "What is the capital of France?")
	if err != nil {
		t.Fatalf("Complete error = %v", err)
	}
	if got != "Paris" {
		t.Fatalf("Complete = %q, want %q", got, "Paris")
	}

	name, args := cap.getAgyCommand()
	if name != "claude" {
		t.Errorf("command name = %q, want %q", name, "claude")
	}
	wantArgs := []string{"-p", "What is the capital of France?"}
	if len(args) != len(wantArgs) || args[0] != wantArgs[0] || args[1] != wantArgs[1] {
		t.Errorf("args = %v, want %v", args, wantArgs)
	}
}

// TC-135-03: auth env mirrors NewClaudeCLIFromEntry — cloud injects the token; local
// injects ANTHROPIC_BASE_URL + the placeholder and no real key.
func TestClaudeCompleterAuthEnvCloudVsLocal(t *testing.T) {
	t.Run("cloud", func(t *testing.T) {
		cap := &capturedCmd{}
		comp := newClaudeCompleter(testClaudeEntry("anthropic-key", ""), &fakeClaudeSecretSource{authToken: "sk-test-123"})
		comp.cmdFactory = stubClaudeCompleterFactory(t, "ok", 0, cap)
		if _, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi"); err != nil {
			t.Fatalf("Complete error = %v", err)
		}
		env := cap.get().Env
		if !envContains(env, ClaudeCLIAuthEnv+"=sk-test-123") {
			t.Errorf("cloud env missing %s=sk-test-123; env=%v", ClaudeCLIAuthEnv, env)
		}
	})
	t.Run("local", func(t *testing.T) {
		cap := &capturedCmd{}
		comp := newClaudeCompleter(testClaudeEntry("", "http://localhost:4000"), &fakeClaudeSecretSource{})
		comp.cmdFactory = stubClaudeCompleterFactory(t, "ok", 0, cap)
		if _, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi"); err != nil {
			t.Fatalf("Complete error = %v", err)
		}
		env := cap.get().Env
		if !envContains(env, ClaudeCLIBaseURLEnv+"=http://localhost:4000") {
			t.Errorf("local env missing %s; env=%v", ClaudeCLIBaseURLEnv, env)
		}
		if !envContains(env, ClaudeCLIAuthTokenEnv+"="+LocalProxyAuthPlaceholder) {
			t.Errorf("local env missing placeholder %s; env=%v", ClaudeCLIAuthTokenEnv, env)
		}
		for _, e := range env {
			if strings.HasPrefix(e, ClaudeCLIAuthEnv+"=") {
				t.Errorf("local env unexpectedly set a real %s: %q", ClaudeCLIAuthEnv, e)
			}
		}
	})
}

// TC-135-04: non-zero exit returns a sanitized error with no raw token; result empty.
func TestClaudeCompleterNonZeroExitSanitizedError(t *testing.T) {
	cap := &capturedCmd{}
	comp := newClaudeCompleter(testClaudeEntry("anthropic-key", ""), &fakeClaudeSecretSource{authToken: "sk-secret-xyz"})
	// The helper prints stdout (which embeds the token) then exits non-zero.
	comp.cmdFactory = stubClaudeCompleterFactory(t, "boom sk-secret-xyz failed", 1, cap)

	got, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi")
	if err == nil {
		t.Fatal("expected error on non-zero exit")
	}
	if got != "" {
		t.Errorf("result = %q, want empty on error", got)
	}
	if !strings.Contains(err.Error(), "claude") {
		t.Errorf("error %q should name claude", err.Error())
	}
	if strings.Contains(err.Error(), "sk-secret-xyz") {
		t.Errorf("error leaked the auth token: %q", err.Error())
	}
}

// TC-135-05: an already-cancelled context propagates and yields an empty result.
func TestClaudeCompleterContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	comp := newClaudeCompleter(testClaudeEntry("", "http://localhost:4000"), &fakeClaudeSecretSource{})
	comp.cmdFactory = stubClaudeCompleterFactory(t, "Paris", 0, &capturedCmd{})

	got, err := comp.Complete(ctx, registry.RegistryEntry{}, "hi")
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
	if got != "" {
		t.Errorf("result = %q, want empty on error", got)
	}
}

// envContains reports whether want is present verbatim in env.
func envContains(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}
