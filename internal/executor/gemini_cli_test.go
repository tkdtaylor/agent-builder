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
	// Run returns non-nil error containing the stderr text; Result.OK == false.
	const secretRef = "gemini-api-key"
	const apiKey = "gai-test-gemini-key"
	const stderrMsg = "Gemini API error"

	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testGeminiEntry(secretRef)

	// Stub subprocess: custom factory that exits 1 and captures stderr.
	// We can't use the shared runHelperProcess (it emits "Codex API error").
	// Instead, create a minimal subprocess that exits non-zero with Gemini-specific stderr.
	cli := NewGeminiCLI(entry, src, t.TempDir())
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		// Use a shell command that exits 1 and writes to stderr.
		cmd := exec.CommandContext(ctx, "sh", "-c", "echo 'Gemini API error' >&2; exit 1")
		return cmd
	}

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
	// TC-090-04: assert the error contains the subprocess failure indication.
	if !strings.Contains(err.Error(), "gemini CLI failed") {
		t.Fatalf("error does not mention 'gemini CLI failed': %v", err)
	}
	// TC-090-04: assert the error contains the subprocess stderr text (proves it was captured).
	if !strings.Contains(err.Error(), stderrMsg) {
		t.Fatalf("error does not contain stderr text %q: %v", stderrMsg, err)
	}
}

// ---- TC-090-05 is verified by make fitness-supervisor-isolation + make check ----
// The compile-time assertion above ensures no import direction violation.
// The fitness check enforces F-003 at the import-graph level.

// TestGeminiPromptIncludesFailureSectionWhenPriorFailureSet verifies that buildGeminiPrompt
// includes the gate-failure section when task.PriorFailure is non-empty.
// TC-108-05
func TestGeminiPromptIncludesFailureSectionWhenPriorFailureSet(t *testing.T) {
	task := supervisor.Task{
		ID:           "001",
		Repo:         "exec-sandbox",
		Spec:         "/tasks/001.md",
		PriorFailure: "Failed step: golangci-lint\nOutput:\nerr: unused variable\nFix these issues before producing the branch.",
	}
	prompt := buildGeminiPrompt(task, "/worktree")

	// Assert: contains "previous attempt"
	if !strings.Contains(prompt, "previous attempt") {
		t.Errorf("prompt missing 'previous attempt', got:\n%s", prompt)
	}

	// Assert: contains "verification gate"
	if !strings.Contains(prompt, "verification gate") {
		t.Errorf("prompt missing 'verification gate', got:\n%s", prompt)
	}

	// Assert: contains the step name from PriorFailure
	if !strings.Contains(prompt, "golangci-lint") {
		t.Errorf("prompt missing 'golangci-lint', got:\n%s", prompt)
	}

	// Assert: contains the step output from PriorFailure
	if !strings.Contains(prompt, "unused variable") {
		t.Errorf("prompt missing 'unused variable', got:\n%s", prompt)
	}
}

// TestGeminiPromptOmitsFailureSectionWhenPriorFailureEmpty verifies that buildGeminiPrompt
// OMITS the gate-failure section when task.PriorFailure is empty.
// TC-108-06
func TestGeminiPromptOmitsFailureSectionWhenPriorFailureEmpty(t *testing.T) {
	task := supervisor.Task{
		ID:   "001",
		Repo: "exec-sandbox",
		Spec: "/tasks/001.md",
		// PriorFailure is zero-value ""
	}
	prompt := buildGeminiPrompt(task, "/worktree")

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

// ---- TC-132-01..06: Gemini subscription/OAuth auth path tests ----

// TestGeminiSubscriptionModeSkipsKeyAndRuns tests that subscription mode (SecretRef == "")
// does NOT call NamedProviderToken, does NOT inject GEMINI_API_KEY, and runs with inherited env.
// TC-132-01, REQ-132-01
func TestGeminiSubscriptionModeSkipsKeyAndRuns(t *testing.T) {
	const modelID = "gemini-2.0-flash"
	const expectedBranch = "task/132-test"

	// Secret source whose NamedProviderToken must NOT be called.
	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{}, // empty; if consulted, test will fail
	}

	// Subscription entry: empty SecretRef.
	entry := testGeminiEntry("")

	worktree := t.TempDir()

	// Stub subprocess: exits 0, outputs the branch marker.
	stubOut := "Running Gemini subscription...\nBRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewGeminiCLI(entry, src, worktree)

	// Replace the secret source with one that fails if NamedProviderToken is called.
	tokenCalled := false
	cli.secretSource = &testGeminiSecretSourceThatFailsIfCalled{&tokenCalled}

	cli.cmdFactory = stubGeminiCommandFactory(t, stubOut, 0, capture)

	task := supervisor.Task{ID: "132", Repo: "agent-builder", Spec: "docs/tasks/backlog/132-gemini-subscription-oauth-auth.md"}

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

	// TC-132-01: assert the subprocess environment has NO GEMINI_API_KEY.
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	var foundAPIKey bool
	var foundModelVar bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, GeminiAPIKeyEnv+"=") {
			foundAPIKey = true
			t.Fatalf("GEMINI_API_KEY found in subscription mode env: %s", env)
		}
		if strings.HasPrefix(env, "GEMINI_MODEL=") {
			value := strings.TrimPrefix(env, "GEMINI_MODEL=")
			if value != modelID {
				t.Fatalf("GEMINI_MODEL env value = %q, want %q", value, modelID)
			}
			foundModelVar = true
		}
	}
	if foundAPIKey {
		t.Fatal("GEMINI_API_KEY must not be in subprocess env in subscription mode")
	}
	if !foundModelVar {
		t.Fatalf("GEMINI_MODEL not found in subprocess env")
	}

	// TC-132-01: assert the worktree is set as cmd.Dir.
	if cmd.Dir != worktree {
		t.Fatalf("cmd.Dir = %q, want %q (the worktree path)", cmd.Dir, worktree)
	}
}

// testGeminiSecretSourceThatFailsIfCalled is a test double that fails if NamedProviderToken is called.
type testGeminiSecretSourceThatFailsIfCalled struct {
	called *bool
}

func (f *testGeminiSecretSourceThatFailsIfCalled) ProviderToken() (string, string) {
	return "", ""
}

func (f *testGeminiSecretSourceThatFailsIfCalled) PublisherTokens() (string, string) {
	return "", ""
}

func (f *testGeminiSecretSourceThatFailsIfCalled) NamedProviderToken(ref string) (string, error) {
	*f.called = true
	panic("NamedProviderToken should not be called in subscription mode")
}

// Compile-time assertion: testGeminiSecretSourceThatFailsIfCalled satisfies secrets.SecretSource.
var _ secrets.SecretSource = (*testGeminiSecretSourceThatFailsIfCalled)(nil)

// TestGeminiSubscriptionModeStripsStrayApiKey tests that subscription mode strips any
// pre-existing GEMINI_API_KEY from the base environment (force OAuth).
// TC-132-02, REQ-132-02
func TestGeminiSubscriptionModeStripsStrayApiKey(t *testing.T) {
	const modelID = "gemini-2.0-flash"
	const expectedBranch = "task/132-test-strip"

	src := &fakeGeminiSecretSource{}
	entry := testGeminiEntry("")

	worktree := t.TempDir()

	stubOut := "BRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewGeminiCLI(entry, src, worktree)

	// Override cmdFactory to set a base env with a stray GEMINI_API_KEY.
	cli.cmdFactory = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cmd := exec.CommandContext(ctx, os.Args[0])
		cmd.Env = []string{
			"CODEX_HELPER_STDOUT=" + stubOut,
			"CODEX_HELPER_EXIT=0",
			"GO_WANT_HELPER_PROCESS=1",
			"GEMINI_API_KEY=stray",
			"OTHER_VAR=value",
		}
		if capture != nil {
			capture.set(cmd)
		}
		return cmd
	}

	task := supervisor.Task{ID: "132", Repo: "agent-builder", Spec: "docs/tasks/backlog/132-gemini-subscription-oauth-auth.md"}

	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() returned unexpected error: %v", err)
	}
	if !result.OK {
		t.Fatalf("result.OK = false, want true")
	}

	// TC-132-02: assert the subprocess environment has NO GEMINI_API_KEY entry (stripped).
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	var foundAPIKey bool
	var foundModelVar bool
	var foundOtherVar bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, GeminiAPIKeyEnv+"=") {
			foundAPIKey = true
			t.Fatalf("GEMINI_API_KEY found after stripping in subscription mode: %s", env)
		}
		if strings.HasPrefix(env, "GEMINI_MODEL=") {
			value := strings.TrimPrefix(env, "GEMINI_MODEL=")
			if value != modelID {
				t.Fatalf("GEMINI_MODEL env value = %q, want %q", value, modelID)
			}
			foundModelVar = true
		}
		if strings.HasPrefix(env, "OTHER_VAR=") {
			foundOtherVar = true
		}
	}
	if foundAPIKey {
		t.Fatal("GEMINI_API_KEY must be stripped in subscription mode")
	}
	if !foundModelVar {
		t.Fatalf("GEMINI_MODEL not found in subprocess env")
	}
	if !foundOtherVar {
		t.Fatalf("OTHER_VAR was unexpectedly stripped")
	}
}

// TestGeminiApiKeyModeUnchanged tests that API-key mode (SecretRef != "") is unchanged from task 090.
// TC-132-03, REQ-132-03
func TestGeminiApiKeyModeUnchanged(t *testing.T) {
	const secretRef = "gemini-api-key"
	const apiKey = "gai-test"
	const modelID = "gemini-2.0-flash"
	const expectedBranch = "task/132-api-key-unchanged"

	src := &fakeGeminiSecretSource{
		namedTokens: map[string]string{secretRef: apiKey},
	}
	entry := testGeminiEntry(secretRef)

	worktree := t.TempDir()

	stubOut := "BRANCH: " + expectedBranch + "\n"
	capture := &capturedCmd{}
	cli := NewGeminiCLI(entry, src, worktree)
	cli.cmdFactory = stubGeminiCommandFactory(t, stubOut, 0, capture)

	task := supervisor.Task{ID: "132", Repo: "agent-builder", Spec: "docs/tasks/backlog/132-gemini-subscription-oauth-auth.md"}

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

	// TC-132-03: assert GEMINI_API_KEY and GEMINI_MODEL are in the env (unchanged from 090).
	cmd := capture.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}

	var foundAPIKey, foundModelVar bool
	for _, env := range cmd.Env {
		if strings.HasPrefix(env, GeminiAPIKeyEnv+"=") {
			value := strings.TrimPrefix(env, GeminiAPIKeyEnv+"=")
			if value != apiKey {
				t.Fatalf("GEMINI_API_KEY env value = %q, want %q", value, apiKey)
			}
			foundAPIKey = true
		}
		if strings.HasPrefix(env, "GEMINI_MODEL=") {
			value := strings.TrimPrefix(env, "GEMINI_MODEL=")
			if value != modelID {
				t.Fatalf("GEMINI_MODEL env value = %q, want %q", value, modelID)
			}
			foundModelVar = true
		}
	}
	if !foundAPIKey {
		t.Fatal("GEMINI_API_KEY not found in API-key mode env")
	}
	if !foundModelVar {
		t.Fatal("GEMINI_MODEL not found in API-key mode env")
	}
}

// TestGeminiApiKeyModeStillErrorsOnMissingSecret tests that API-key mode still errors
// when the secret is not found (regression guard for task 090).
// TC-132-04, REQ-132-03
func TestGeminiApiKeyModeStillErrorsOnMissingSecret(t *testing.T) {
	const secretRef = "gemini-api-key"

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

	task := supervisor.Task{ID: "132", Repo: "agent-builder", Spec: "docs/tasks/backlog/132-gemini-subscription-oauth-auth.md"}

	_, err := cli.Run(task)
	if err == nil {
		t.Fatal("Run() returned nil error, want non-nil error on missing secret in API-key mode")
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

// TestSanitizeGeminiOutputSafeWithEmptyKey tests that sanitizeGeminiOutput is safe
// when called with an empty API key (subscription mode).
// TC-132-06, REQ-132-05
func TestSanitizeGeminiOutputSafeWithEmptyKey(t *testing.T) {
	output := "Some Gemini output\nNext line"
	apiKey := ""

	result := sanitizeGeminiOutput(output, "", apiKey)

	// TC-132-06: output should be unchanged; no panic, no spurious redaction.
	expectedOutput := output
	if result != expectedOutput {
		t.Fatalf("sanitizeGeminiOutput with empty apiKey returned %q, want %q", result, expectedOutput)
	}
}
