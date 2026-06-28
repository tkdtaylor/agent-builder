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
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Note: TestMain, runHelperProcess, and capturedCmd are defined in codex_cli_test.go.
// They serve as shared test infrastructure for the executor package.

// ---- Test doubles for Gemini ----

// fakeGeminiSecretSource is a test double for secrets.SecretSource used in Gemini tests.
type fakeGeminiSecretSource struct {
	namedTokens map[string]string // ref → token
}

func (f *fakeGeminiSecretSource) ProviderToken() (string, string) {
	return "", ""
}

func (f *fakeGeminiSecretSource) PublisherTokens() (string, string) {
	return "", ""
}

func (f *fakeGeminiSecretSource) NamedProviderToken(ref string) (string, error) {
	tok, ok := f.namedTokens[ref]
	if !ok || tok == "" {
		return "", secrets.ErrSecretNotFound
	}
	return tok, nil
}

// Compile-time assertion: fakeGeminiSecretSource satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*fakeGeminiSecretSource)(nil)

// testGeminiEntry returns a RegistryEntry configured for Gemini CLI.
func testGeminiEntry(secretRef string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:        "gemini-flash",
		Harness:   registry.HarnessGeminiCLI,
		ModelID:   "gemini-2.0-flash",
		SecretRef: secretRef,
	}
}

// stubGeminiCommandFactory returns a geminiCommandCreator that re-invokes the test binary
// as a subprocess (via TestMain/runHelperProcess) with GO_WANT_HELPER_PROCESS=1.
// stdout is the text the helper writes to stdout; exitCode is the subprocess exit code.
// The factory captures the created *exec.Cmd in captureState for test assertion.
func stubGeminiCommandFactory(t *testing.T, stdout string, exitCode int, captureState *capturedCmd) geminiCommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Re-invoke ourselves as a subprocess; TestMain routes to runHelperProcess.
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stdout,
			fmt.Sprintf("CODEX_HELPER_EXIT=%d", exitCode),
			"GO_WANT_HELPER_PROCESS=1",
		}
		if captureState != nil {
			captureState.set(cmd)
		}
		return cmd
	}
}

// ---- TC-090-01: GeminiCLI satisfies supervisor.Executor at compile time ----

// Compile-time assertion (TC-090-01): *GeminiCLI implements supervisor.Executor.
var _ supervisor.Executor = (*GeminiCLI)(nil)

func TestGeminiCLI_InterfaceSatisfied(t *testing.T) {
	// TC-090-01: NewGeminiCLI returns a non-nil value satisfying supervisor.Executor.
	// The compile-time var _ assertion above is the primary guard; this test confirms
	// the constructor does not return nil at runtime.
	src := &fakeGeminiSecretSource{namedTokens: map[string]string{"gemini-api-key": "gai-test"}}
	entry := testGeminiEntry("gemini-api-key")

	cli := NewGeminiCLI(entry, src, "/tmp/worktree")

	if cli == nil {
		t.Fatal("NewGeminiCLI() returned nil")
	}
}

// ---- TC-090-02: Subprocess invoked with auth token in env, model ID, worktree ----

func TestGeminiCLI_RunInvokesSubprocessWithCorrectEnvAndArgs(t *testing.T) {
	// TC-090-02: stub subprocess is invoked with auth token in env, model ID, worktree;
	// returns OK result with branch. Asserts that GEMINI_API_KEY, model ID, and worktree
	// actually reach the subprocess environment/args.
	const secretRef = "gemini-api-key"
	const apiKey = "gai-test-gemini-key"
	const modelID = "gemini-2.0-flash"
	const expectedBranch = "task/090-test-branch"

	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testGeminiEntry(secretRef)

	worktree := t.TempDir()

	// Stub subprocess: exits 0, outputs the branch marker.
	stubOut := "Running task...\nBRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewGeminiCLI(entry, src, worktree)
	cli.cmdFactory = stubGeminiCommandFactory(t, stubOut, 0, capture)

	task := supervisor.Task{ID: "090", Repo: "agent-builder", Spec: "docs/tasks/backlog/090-gemini-harness-adapter.md"}

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

	// TC-090-02: assert the subprocess environment and args received the auth token, model, worktree.
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	// Assert GEMINI_API_KEY is in the subprocess env.
	var foundAuthToken bool
	var foundModelVar bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, GeminiAPIKeyEnv+"=") {
			value := strings.TrimPrefix(env, GeminiAPIKeyEnv+"=")
			if value != apiKey {
				t.Fatalf("GEMINI_API_KEY env value = %q, want %q", value, apiKey)
			}
			foundAuthToken = true
		}
		if strings.HasPrefix(env, "GEMINI_MODEL=") {
			value := strings.TrimPrefix(env, "GEMINI_MODEL=")
			if value != modelID {
				t.Fatalf("GEMINI_MODEL env value = %q, want %q", value, modelID)
			}
			foundModelVar = true
		}
	}
	if !foundAuthToken {
		t.Fatalf("GEMINI_API_KEY not found in subprocess env")
	}
	if !foundModelVar {
		t.Fatalf("GEMINI_MODEL not found in subprocess env")
	}

	// Assert the worktree is set as cmd.Dir.
	if cmd.Dir != worktree {
		t.Fatalf("cmd.Dir = %q, want %q (the worktree path)", cmd.Dir, worktree)
	}
}

// ---- TC-090-03: Auth token resolved via NamedProviderToken; ErrSecretNotFound → error before subprocess ----

func TestGeminiCLI_RunErrorsWhenSecretNotFound(t *testing.T) {
	// TC-090-03 Variant B: NamedProviderToken returns ErrSecretNotFound → Run fails before subprocess.
	const secretRef = "gemini-api-key"

	// Variant B: secret not found
	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{}, // empty — no token registered
	}
	entry := testGeminiEntry(secretRef)

	// A subprocess-tracking factory: if invoked, the test fails.
	subprocessInvoked := false
	cli := NewGeminiCLI(entry, src, t.TempDir())
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		subprocessInvoked = true
		return exec.CommandContext(ctx, name, args...)
	}

	task := supervisor.Task{ID: "090", Repo: "agent-builder", Spec: "spec"}

	_, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil error on missing secret")
	}

	// Error must wrap ErrGeminiSecretNotFound.
	if !errors.Is(err, ErrGeminiSecretNotFound) {
		t.Fatalf("error does not wrap ErrGeminiSecretNotFound: %v", err)
	}
	// Error must name the failed SecretRef.
	if !strings.Contains(err.Error(), secretRef) {
		t.Fatalf("error does not name the failed SecretRef %q: %v", secretRef, err)
	}
	if subprocessInvoked {
		t.Fatal("subprocess was invoked even though secret resolution failed")
	}
}

func TestGeminiCLI_RunSucceedsVariantA(t *testing.T) {
	// TC-090-03 Variant A: NamedProviderToken returns the key → subprocess is invoked.
	const secretRef = "gemini-api-key"
	const apiKey = "gai-test-key"
	const expectedBranch = "task/090-variant-a"

	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testGeminiEntry(secretRef)

	stubOut := "BRANCH: " + expectedBranch + "\n"
	cli := NewGeminiCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubGeminiCommandFactory(t, stubOut, 0, nil)

	task := supervisor.Task{ID: "090", Repo: "agent-builder", Spec: "spec"}
	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatal("result.OK = false, want true")
	}
	if result.Branch != expectedBranch {
		t.Fatalf("result.Branch = %q, want %q", result.Branch, expectedBranch)
	}
}

// ---- TC-090-04: Subprocess exit 1 → error; Result.OK == false ----

func TestGeminiCLI_RunSubprocessNonZeroExitReturnsError(t *testing.T) {
	// TC-090-04: subprocess exits 1 with stderr "Gemini API error" →
	// Run returns non-nil error; Result.OK == false.
	const secretRef = "gemini-api-key"
	const apiKey = "gai-test-gemini-key"

	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testGeminiEntry(secretRef)

	// Stub subprocess: exits 1 (the helper writes "Codex API error" to stderr by
	// convention from runHelperProcess; text not tested here since it comes from
	// the shared helper — what matters is that Run returns an error and OK==false).
	cli := NewGeminiCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubGeminiCommandFactory(t, "", 1, nil)

	task := supervisor.Task{ID: "090", Repo: "agent-builder", Spec: "spec"}

	result, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil on subprocess exit 1")
	}
	if result.OK {
		t.Fatal("result.OK = true, want false on subprocess failure")
	}
	if result.Branch != "" {
		t.Fatalf("result.Branch = %q, want empty on failure", result.Branch)
	}
	// The error should contain subprocess failure indication.
	if !strings.Contains(err.Error(), "gemini CLI failed") {
		t.Fatalf("error does not mention 'gemini CLI failed': %v", err)
	}
}

// ---- TC-090-05 is verified by make fitness-supervisor-isolation + make check ----
// The compile-time assertion above ensures no import direction violation.
// The fitness check enforces F-003 at the import-graph level.
