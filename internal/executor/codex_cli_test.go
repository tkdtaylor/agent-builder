package executor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
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

// capturedCmd holds the last *exec.Cmd created by the stubbing factory,
// allowing tests to assert the subprocess environment and arguments.
// It also captures the real (name, args) passed to executor factories for argv assertion.
type capturedCmd struct {
	mu       sync.Mutex
	cmd      *exec.Cmd
	cmdName  string   // real command name (e.g. "agy")
	cmdArgs  []string // real command args passed by executor
}

func (c *capturedCmd) set(cmd *exec.Cmd) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cmd = cmd
}

func (c *capturedCmd) get() *exec.Cmd {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmd
}

// setAgyCommand records the real agy command name and args for assertion.
func (c *capturedCmd) setAgyCommand(name string, args []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cmdName = name
	c.cmdArgs = append([]string{}, args...) // copy args to avoid mutation
}

// getAgyCommand retrieves the recorded agy command name and args.
func (c *capturedCmd) getAgyCommand() (string, []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cmdName, append([]string{}, c.cmdArgs...)
}

// stubCommandFactory returns a commandCreator that re-invokes the test binary
// as a subprocess (via TestMain/runHelperProcess) with GO_WANT_HELPER_PROCESS=1.
// stdout is the text the helper writes to stdout; exitCode is the subprocess exit code.
// The factory captures the created *exec.Cmd in captureState for test assertion.
func stubCommandFactory(t *testing.T, stdout string, exitCode int, captureState *capturedCmd) commandCreator {
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
	// returns OK result with branch. Asserts that OPENAI_API_KEY, model ID, and worktree
	// actually reach the subprocess environment/args.
	const secretRef = "codex-openai-token"
	const apiKey = "sk-test-codex-key"
	const modelID = "gpt-4o"
	const expectedBranch = "task/089-test-branch"

	src := &fakeCodexSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testCodexEntry(secretRef)

	worktree := t.TempDir()

	// Stub subprocess: exits 0, outputs the branch marker.
	stubOut := "Running task...\nBRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewCodexCLI(entry, src, worktree)
	cli.cmdFactory = stubCommandFactory(t, stubOut, 0, capture)

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

	// TC-089-02: assert the subprocess environment and args received the auth token, model, worktree.
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	// Assert OPENAI_API_KEY is in the subprocess env.
	var foundAuthToken bool
	var foundModelVar bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, CodexAPIKeyEnv+"=") {
			value := strings.TrimPrefix(env, CodexAPIKeyEnv+"=")
			if value != apiKey {
				t.Fatalf("OPENAI_API_KEY env value = %q, want %q", value, apiKey)
			}
			foundAuthToken = true
		}
		if strings.HasPrefix(env, "CODEX_MODEL=") {
			value := strings.TrimPrefix(env, "CODEX_MODEL=")
			if value != modelID {
				t.Fatalf("CODEX_MODEL env value = %q, want %q", value, modelID)
			}
			foundModelVar = true
		}
	}
	if !foundAuthToken {
		t.Fatalf("OPENAI_API_KEY not found in subprocess env")
	}
	if !foundModelVar {
		t.Fatalf("CODEX_MODEL not found in subprocess env")
	}

	// Assert the worktree is set as cmd.Dir.
	if cmd.Dir != worktree {
		t.Fatalf("cmd.Dir = %q, want %q (the worktree path)", cmd.Dir, worktree)
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
	cli.cmdFactory = stubCommandFactory(t, stubOut, 0, nil)

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
	cli.cmdFactory = stubCommandFactory(t, "", 1, nil)

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

// TestCodexPromptIncludesFailureSectionWhenPriorFailureSet verifies that buildCodexPrompt
// includes the gate-failure section when task.PriorFailure is non-empty.
// TC-108-03
func TestCodexPromptIncludesFailureSectionWhenPriorFailureSet(t *testing.T) {
	task := supervisor.Task{
		ID:           "001",
		Repo:         "exec-sandbox",
		Spec:         "/tasks/001.md",
		PriorFailure: "Failed step: go-test\nOutput:\nFAIL TestBar\nFix these issues before producing the branch.",
	}
	prompt := buildCodexPrompt(task, "/worktree")

	// Assert: contains "previous attempt"
	if !strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt missing 'previous attempt', got:\n%s", prompt)
	}

	// Assert: contains "verification gate"
	if !strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt missing 'verification gate', got:\n%s", prompt)
	}

	// Assert: contains the step name from PriorFailure
	if !strings.Contains(prompt, "go-test") {
		t.Errorf("prompt missing 'go-test', got:\n%s", prompt)
	}

	// Assert: contains the step output from PriorFailure
	if !strings.Contains(prompt, "FAIL TestBar") {
		t.Errorf("prompt missing 'FAIL TestBar', got:\n%s", prompt)
	}
}

// TestCodexPromptOmitsFailureSectionWhenPriorFailureEmpty verifies that buildCodexPrompt
// OMITS the gate-failure section when task.PriorFailure is empty.
// TC-108-04
func TestCodexPromptOmitsFailureSectionWhenPriorFailureEmpty(t *testing.T) {
	task := supervisor.Task{
		ID:   "001",
		Repo: "exec-sandbox",
		Spec: "/tasks/001.md",
		// PriorFailure is zero-value ""
	}
	prompt := buildCodexPrompt(task, "/worktree")

	// Assert: does NOT contain "previous attempt"
	if strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt should not contain 'previous attempt' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: does NOT contain "verification gate"
	if strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt should not contain 'verification gate' when PriorFailure is empty, got:\n%s", prompt)
	}

	// Assert: core content is present
	if !strings.Contains(prompt, "Task ID: 001") {
		t.Errorf("core prompt missing 'Task ID: 001', got:\n%s", prompt)
	}
}
