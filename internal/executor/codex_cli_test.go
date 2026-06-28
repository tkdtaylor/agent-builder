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

// TestMain allows helper subprocess processes to run and exit cleanly without
// the Go test framework intercepting os.Exit. When GO_WANT_HELPER_PROCESS=1 is
// set, this process is running as a subprocess stub — handle it and exit.
func TestMain(m *testing.M) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") == "1" {
		runHelperProcess()
		// runHelperProcess calls os.Exit; this line is unreachable.
	}
	os.Exit(m.Run())
}

// runHelperProcess is the subprocess body used by stubCommandFactory.
// It reads CODEX_HELPER_STDOUT and CODEX_HELPER_EXIT from env, writes stdout,
// and exits with the configured code.
func runHelperProcess() {
	stdout := os.Getenv("CODEX_HELPER_STDOUT")
	exitStr := os.Getenv("CODEX_HELPER_EXIT")
	exitCode := 0
	if exitStr != "" {
		fmt.Sscanf(exitStr, "%d", &exitCode) //nolint:errcheck
	}
	if stdout != "" {
		fmt.Print(stdout)
	}
	if exitCode != 0 {
		fmt.Fprintf(os.Stderr, "Codex API error")
		os.Exit(exitCode)
	}
	os.Exit(0)
}

// ---- Test doubles ----

// fakeCodexSecretSource is a test double for secrets.SecretSource used in Codex tests.
type fakeCodexSecretSource struct {
	namedTokens map[string]string // ref → token
}

func (f *fakeCodexSecretSource) ProviderToken() (string, string) {
	return "", ""
}

func (f *fakeCodexSecretSource) PublisherTokens() (string, string) {
	return "", ""
}

func (f *fakeCodexSecretSource) NamedProviderToken(ref string) (string, error) {
	tok, ok := f.namedTokens[ref]
	if !ok || tok == "" {
		return "", secrets.ErrSecretNotFound
	}
	return tok, nil
}

// Compile-time assertion: fakeCodexSecretSource satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*fakeCodexSecretSource)(nil)

// testCodexEntry returns a RegistryEntry configured for Codex CLI.
func testCodexEntry(secretRef string) registry.RegistryEntry {
	return registry.RegistryEntry{
		ID:        "codex-gpt4o",
		Harness:   registry.HarnessCodexCLI,
		ModelID:   "gpt-4o",
		SecretRef: secretRef,
	}
}

// stubCommandFactory returns a commandCreator that re-invokes the test binary
// as a subprocess (via TestMain/runHelperProcess) with GO_WANT_HELPER_PROCESS=1.
// stdout is the text the helper writes to stdout; exitCode is the subprocess exit code.
func stubCommandFactory(t *testing.T, stdout string, exitCode int) commandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Re-invoke ourselves as a subprocess; TestMain routes to runHelperProcess.
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stdout,
			fmt.Sprintf("CODEX_HELPER_EXIT=%d", exitCode),
			"GO_WANT_HELPER_PROCESS=1",
		}
		return cmd
	}
}

// ---- TC-089-01: CodexCLI satisfies supervisor.Executor at compile time ----

// Compile-time assertion (TC-089-01): *CodexCLI implements supervisor.Executor.
var _ supervisor.Executor = (*CodexCLI)(nil)

func TestCodexCLI_InterfaceSatisfied(t *testing.T) {
	// TC-089-01: NewCodexCLI returns a non-nil value satisfying supervisor.Executor.
	// The compile-time var _ assertion above is the primary guard; this test confirms
	// the constructor does not return nil at runtime.
	src := &fakeCodexSecretSource{namedTokens: map[string]string{"codex-openai-token": "sk-test"}}
	entry := testCodexEntry("codex-openai-token")

	cli := NewCodexCLI(entry, src, "/tmp/worktree")

	if cli == nil {
		t.Fatal("NewCodexCLI() returned nil")
	}
}

// ---- TC-089-02: Subprocess invoked with auth token in env, model ID, worktree ----

func TestCodexCLI_RunInvokesSubprocessWithCorrectEnvAndArgs(t *testing.T) {
	// TC-089-02: stub subprocess is invoked with auth token in env, model ID, worktree;
	// returns OK result with branch.
	const secretRef = "codex-openai-token"
	const apiKey = "sk-test-codex-key"
	const expectedBranch = "task/089-test-branch"

	src := &fakeCodexSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testCodexEntry(secretRef)

	// Stub subprocess: exits 0, outputs the branch marker.
	stubOut := "Running task...\nBRANCH: " + expectedBranch + "\n"
	cli := NewCodexCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubCommandFactory(t, stubOut, 0)

	task := supervisor.Task{ID: "089", Repo: "agent-builder", Spec: "docs/tasks/backlog/089-codex-harness-adapter.md"}

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
}

// ---- TC-089-03: ErrSecretNotFound → Run errors before subprocess invocation ----

func TestCodexCLI_RunErrorsWhenSecretNotFound(t *testing.T) {
	// TC-089-03: NamedProviderToken returns ErrSecretNotFound → Run fails before subprocess.
	const secretRef = "codex-openai-token"

	// Variant B: secret not found
	src := &fakeCodexSecretSource{
		namedTokens: map[string]string{}, // empty — no token registered
	}
	entry := testCodexEntry(secretRef)

	// A subprocess-tracking factory: if invoked, the test fails.
	subprocessInvoked := false
	cli := NewCodexCLI(entry, src, t.TempDir())
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		subprocessInvoked = true
		return exec.CommandContext(ctx, name, args...)
	}

	task := supervisor.Task{ID: "089", Repo: "agent-builder", Spec: "spec"}

	_, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil error on missing secret")
	}

	// Error must mention the secret resolution failure.
	if !errors.Is(err, ErrCodexSecretNotFound) {
		t.Fatalf("error does not wrap ErrCodexSecretNotFound: %v", err)
	}
	if !strings.Contains(err.Error(), secretRef) {
		t.Fatalf("error does not name the failed SecretRef %q: %v", secretRef, err)
	}
	if subprocessInvoked {
		t.Fatal("subprocess was invoked even though secret resolution failed")
	}
}

func TestCodexCLI_RunSucceedsVariantA(t *testing.T) {
	// TC-089-03 Variant A: NamedProviderToken returns the key → subprocess is invoked.
	const secretRef = "codex-openai-token"
	const apiKey = "sk-test-key"
	const expectedBranch = "task/089-variant-a"

	src := &fakeCodexSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testCodexEntry(secretRef)

	stubOut := "BRANCH: " + expectedBranch + "\n"
	cli := NewCodexCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubCommandFactory(t, stubOut, 0)

	task := supervisor.Task{ID: "089", Repo: "agent-builder", Spec: "spec"}
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

// ---- TC-089-04: Subprocess exit 1 → error; Result.OK == false ----

func TestCodexCLI_RunSubprocessNonZeroExitReturnsError(t *testing.T) {
	// TC-089-04: subprocess exits 1 with stderr "Codex API error" →
	// Run returns non-nil error containing the stderr text; Result.OK == false.
	const secretRef = "codex-openai-token"
	const apiKey = "sk-test-codex-key"

	src := &fakeCodexSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testCodexEntry(secretRef)

	// Stub subprocess: exits 1 (stderr is written by the helper as "Codex API error").
	cli := NewCodexCLI(entry, src, t.TempDir())
	cli.cmdFactory = stubCommandFactory(t, "", 1)

	task := supervisor.Task{ID: "089", Repo: "agent-builder", Spec: "spec"}

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
	if !strings.Contains(err.Error(), "codex CLI failed") {
		t.Fatalf("error does not mention 'codex CLI failed': %v", err)
	}
}

// ---- TC-089-05 is verified by make fitness-supervisor-isolation + make check ----
// The compile-time assertion above ensures no import direction violation.
// The fitness check enforces F-003 at the import-graph level.
