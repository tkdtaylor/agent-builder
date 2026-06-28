package router

import (
	"errors"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
)

// fakeSecrets is a minimal SecretSource for resolver tests — it never touches the
// real environment or vault.
type fakeSecrets struct{}

func (fakeSecrets) ProviderToken() (string, string)           { return "api-key", "" }
func (fakeSecrets) PublisherTokens() (string, string)         { return "", "" }
func (fakeSecrets) NamedProviderToken(string) (string, error) { return "named-token", nil }

var _ secrets.SecretSource = fakeSecrets{}

// ResolveExecutor selects an entry and hands back a concrete supervisor.Executor
// for the entry's harness. This is the executor-side boundary: the caller gets a
// supervisor.Executor seam plus the selected entry (to drive the fallback axes).
func TestResolveExecutorReturnsExecutorForHarness(t *testing.T) {
	cases := []struct {
		name    string
		harness registry.HarnessDriver
		assert  func(t *testing.T, e interface{})
	}{
		{
			name:    "claude",
			harness: registry.HarnessClaudeCLI,
			assert: func(t *testing.T, e interface{}) {
				if _, ok := e.(*executor.ClaudeCLI); !ok {
					t.Fatalf("expected *executor.ClaudeCLI, got %T", e)
				}
			},
		},
		{
			name:    "codex",
			harness: registry.HarnessCodexCLI,
			assert: func(t *testing.T, e interface{}) {
				if _, ok := e.(*executor.CodexCLI); !ok {
					t.Fatalf("expected *executor.CodexCLI, got %T", e)
				}
			},
		},
		{
			name:    "gemini",
			harness: registry.HarnessGeminiCLI,
			assert: func(t *testing.T, e interface{}) {
				if _, ok := e.(*executor.GeminiCLI); !ok {
					t.Fatalf("expected *executor.GeminiCLI, got %T", e)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := entry("e1", 1, 1, 100, "ref")
			e.Harness = tc.harness
			r := New(catalogOf(e))

			exec, sel, err := r.ResolveExecutor(RoutingSpec{MinCapability: 1}, fakeSecrets{}, "/tmp/worktree")
			if err != nil {
				t.Fatalf("ResolveExecutor unexpected error: %v", err)
			}
			if sel.ID != "e1" {
				t.Fatalf("expected selected entry %q, got %q", "e1", sel.ID)
			}
			if exec == nil {
				t.Fatal("ResolveExecutor returned a nil executor")
			}
			tc.assert(t, exec)
		})
	}
}

// An unknown harness driver yields ErrUnknownHarness, not a nil-pointer panic.
func TestResolveExecutorUnknownHarness(t *testing.T) {
	e := entry("weird", 1, 1, 100, "ref")
	e.Harness = registry.HarnessDriver("not-a-real-harness")
	r := New(catalogOf(e))

	_, _, err := r.ResolveExecutor(RoutingSpec{MinCapability: 1}, fakeSecrets{}, "/tmp/worktree")
	if !errors.Is(err, ErrUnknownHarness) {
		t.Fatalf("expected ErrUnknownHarness, got %v", err)
	}
}

// ResolveExecutor propagates ErrNoEligibleExecutor when selection finds nothing.
func TestResolveExecutorNoEligible(t *testing.T) {
	r := New(catalogOf(entry("local", 1, 1, 0, "")))

	_, _, err := r.ResolveExecutor(RoutingSpec{MinCapability: 9}, fakeSecrets{}, "/tmp/worktree")
	if !errors.Is(err, ErrNoEligibleExecutor) {
		t.Fatalf("expected ErrNoEligibleExecutor, got %v", err)
	}
}
