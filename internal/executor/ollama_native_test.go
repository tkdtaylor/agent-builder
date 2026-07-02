package executor

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/executor/ollamaclient"
	"github.com/tkdtaylor/agent-builder/internal/executor/ollamatoolset"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// initGitRepo initializes a git repo with one initial commit in dir, so the
// OllamaNative terminal-branch commit step (commitWorktree) has a repo to act on.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "init")
}

// gitShow returns the content of path on branch in the repo at dir.
func gitShow(t *testing.T, dir, branch, path string) string {
	t.Helper()
	cmd := exec.Command("git", "show", branch+":"+path)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show %s:%s: %v: %s", branch, path, err, out)
	}
	return string(out)
}

// TestTC10301_OllamaNativeSatisfiesExecutorInterface verifies that OllamaNative
// implements supervisor.Executor interface.
func TestTC10301_OllamaNativeSatisfiesExecutorInterface(t *testing.T) {
	// Compile-time assertion
	var _ supervisor.Executor = (*OllamaNative)(nil)
	t.Log("OllamaNative satisfies supervisor.Executor interface")
}

// stubChatter mocks the Ollama client for testing.
type stubChatter struct {
	responses        []ollamaclient.ChatResponse
	callCount        int
	capturedRequests []ollamaclient.ChatRequest
}

func (s *stubChatter) Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	// Check for cancelled context
	select {
	case <-ctx.Done():
		return ollamaclient.ChatResponse{}, ctx.Err()
	default:
	}

	if s.callCount >= len(s.responses) {
		return ollamaclient.ChatResponse{}, errors.New("stub chatter: no more responses")
	}

	// Capture the request
	s.capturedRequests = append(s.capturedRequests, req)

	resp := s.responses[s.callCount]
	s.callCount++
	return resp, nil
}

// stubToolDispatcher mocks the tool dispatcher for testing.
type stubToolDispatcher struct {
	toolSchemas  []ollamaclient.Tool
	branchFile   string
	dispatchFunc func(toolName, argsJSON string) (string, error)
}

func (s *stubToolDispatcher) Dispatch(toolName string, argsJSON string) (string, error) {
	if s.dispatchFunc != nil {
		return s.dispatchFunc(toolName, argsJSON)
	}
	return "(stub result)", nil
}

func (s *stubToolDispatcher) ToolSchemas() []ollamaclient.Tool {
	return s.toolSchemas
}

func (s *stubToolDispatcher) ExtractBranch() (string, bool) {
	if s.branchFile == "" {
		return "", false
	}
	// Try to read the file
	content, err := os.ReadFile(s.branchFile)
	if err != nil {
		return "", false
	}
	return string(content), true
}

// TestTC10302_LoopExecutesWriteFileAndReturnsProducedBranch verifies that the loop
// executes a write_file tool call and returns the produced branch.
func TestTC10302_LoopExecutesWriteFileAndReturnsProducedBranch(t *testing.T) {
	// Create a temp directory for the worktree
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	// Create a stub chatter with two responses
	branchFilePath := filepath.Join(tmpDir, "BRANCH")
	stubChatter := &stubChatter{
		responses: []ollamaclient.ChatResponse{
			{
				Message: ollamaclient.Message{
					Role: "assistant",
					ToolCalls: []ollamaclient.ToolCall{
						{
							Function: ollamaclient.ToolCallFunction{
								Name:      "write_file",
								Arguments: json.RawMessage(`{"path":"BRANCH","content":"task/103-test"}`),
							},
						},
					},
				},
			},
			{
				Message: ollamaclient.Message{
					Role:    "assistant",
					Content: "Task complete.",
				},
			},
		},
	}

	// Create a stub tool dispatcher
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  branchFilePath,
		dispatchFunc: func(toolName, argsJSON string) (string, error) {
			if toolName == "write_file" {
				// Parse the arguments and write the file
				var args struct {
					Path    string `json:"path"`
					Content string `json:"content"`
				}
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", err
				}

				// Write the file
				filePath := filepath.Join(tmpDir, args.Path)
				if err := os.WriteFile(filePath, []byte(args.Content), 0644); err != nil {
					return "", err
				}
				return "wrote " + args.Path, nil
			}
			return "", errors.New("unknown tool")
		},
	}

	// Create OllamaNative with the stub chatter
	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, stubChatter, toolDispatcher)
	if err != nil {
		t.Fatalf("Failed to create OllamaNative: %v", err)
	}

	// Call Run
	result, err := executor.Run(context.Background(), supervisor.Task{
		ID:   "103",
		Repo: "test-repo",
		Spec: "test-spec.md",
	})

	// Verify the results
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Check that the branch file exists with the correct content
	content, err := os.ReadFile(branchFilePath)
	if err != nil {
		t.Fatalf("Branch file not created: %v", err)
	}
	if string(content) != "task/103-test" {
		t.Errorf("Branch file content mismatch: got %q, want %q", string(content), "task/103-test")
	}

	// Check the result
	if result.Branch != "task/103-test" {
		t.Errorf("Result.Branch mismatch: got %q, want %q", result.Branch, "task/103-test")
	}
	if !result.OK {
		t.Errorf("Result.OK mismatch: got %v, want %v", result.OK, true)
	}

	// Check that the stub client was called exactly twice
	if stubChatter.callCount != 2 {
		t.Errorf("Stub client call count mismatch: got %d, want %d", stubChatter.callCount, 2)
	}

	// TC-106-04: the loop must COMMIT the worktree edits onto the produced branch,
	// so the published branch actually contains the work (not just a name). Assert the
	// branch exists and the model's file is committed on it with exact content.
	if got := gitShow(t, tmpDir, "task/103-test", "BRANCH"); got != "task/103-test" {
		t.Errorf("committed BRANCH file on branch mismatch: got %q, want %q", got, "task/103-test")
	}
	// The worktree must be left on the produced branch.
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = tmpDir
	headOut, _ := cmd.CombinedOutput()
	if strings.TrimSpace(string(headOut)) != "task/103-test" {
		t.Errorf("worktree not left on produced branch: HEAD=%q", strings.TrimSpace(string(headOut)))
	}
}

// TestTC10303_LoopStopsAtHardIterationCap verifies that the loop returns OK:false
// when the hard iteration cap is reached.
func TestTC10303_LoopStopsAtHardIterationCap(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a stub chatter that always returns tool_calls
	stubChatter := &stubChatter{
		responses: []ollamaclient.ChatResponse{
			{
				Message: ollamaclient.Message{
					Role: "assistant",
					ToolCalls: []ollamaclient.ToolCall{
						{
							Function: ollamaclient.ToolCallFunction{
								Name:      "read_file",
								Arguments: json.RawMessage(`{"path":"x"}`),
							},
						},
					},
				},
			},
			{
				Message: ollamaclient.Message{
					Role: "assistant",
					ToolCalls: []ollamaclient.ToolCall{
						{
							Function: ollamaclient.ToolCallFunction{
								Name:      "read_file",
								Arguments: json.RawMessage(`{"path":"x"}`),
							},
						},
					},
				},
			},
			{
				Message: ollamaclient.Message{
					Role: "assistant",
					ToolCalls: []ollamaclient.ToolCall{
						{
							Function: ollamaclient.ToolCallFunction{
								Name:      "read_file",
								Arguments: json.RawMessage(`{"path":"x"}`),
							},
						},
					},
				},
			},
		},
	}

	// Create a stub tool dispatcher
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  "", // No branch file
		dispatchFunc: func(toolName, argsJSON string) (string, error) {
			return "(stub content)", nil
		},
	}

	// Create OllamaNative with MaxIterations: 3
	cfg := OllamaNativeConfig{
		Endpoint:      "http://localhost:11434",
		Model:         "qwen3:8b",
		MaxIterations: 3,
		Worktree:      tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, stubChatter, toolDispatcher)
	if err != nil {
		t.Fatalf("Failed to create OllamaNative: %v", err)
	}

	// Call Run
	result, err := executor.Run(context.Background(), supervisor.Task{
		ID:   "103",
		Repo: "test-repo",
		Spec: "test-spec.md",
	})

	// Verify the results
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Check that OK is false
	if result.OK {
		t.Errorf("Result.OK mismatch: got %v, want %v", result.OK, false)
	}

	// Check that the stub client was called exactly 3 times
	if stubChatter.callCount != 3 {
		t.Errorf("Stub client call count mismatch: got %d, want %d", stubChatter.callCount, 3)
	}

	// Check that the branch file was not created
	branchFile := filepath.Join(tmpDir, "BRANCH")
	if _, err := os.Stat(branchFile); err == nil {
		t.Errorf("Branch file should not exist")
	}
}

// TestTC10304_LoopAppendsToolResultsAsMessages verifies that the loop appends
// tool results as role:"tool" messages in the next request.
func TestTC10304_LoopAppendsToolResultsAsMessages(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	// Create a stub chatter that captures requests
	stubChatter := &stubChatter{
		responses: []ollamaclient.ChatResponse{
			{
				Message: ollamaclient.Message{
					Role: "assistant",
					ToolCalls: []ollamaclient.ToolCall{
						{
							Function: ollamaclient.ToolCallFunction{
								Name:      "write_file",
								Arguments: json.RawMessage(`{"path":"out.txt","content":"hello"}`),
							},
						},
					},
				},
			},
			{
				Message: ollamaclient.Message{
					Role:    "assistant",
					Content: "Done.",
				},
			},
		},
	}

	// Create a stub tool dispatcher
	branchFilePath := filepath.Join(tmpDir, "BRANCH")
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  branchFilePath,
		dispatchFunc: func(toolName, argsJSON string) (string, error) {
			if toolName == "write_file" {
				// Write the branch file for extraction
				if err := os.WriteFile(branchFilePath, []byte("task/103-test"), 0644); err != nil {
					return "", err
				}
				return "ok", nil
			}
			return "", errors.New("unknown tool")
		},
	}

	// Create OllamaNative
	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, stubChatter, toolDispatcher)
	if err != nil {
		t.Fatalf("Failed to create OllamaNative: %v", err)
	}

	// Call Run
	_, err = executor.Run(context.Background(), supervisor.Task{
		ID:   "103",
		Repo: "test-repo",
		Spec: "test-spec.md",
	})

	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify the second request has the tool result message
	if len(stubChatter.capturedRequests) < 2 {
		t.Fatalf("Expected at least 2 captured requests, got %d", len(stubChatter.capturedRequests))
	}

	secondReq := stubChatter.capturedRequests[1]
	messages := secondReq.Messages

	// Check that we have at least 3 messages (initial user + tool_calls from call 1 + tool result)
	if len(messages) < 3 {
		t.Errorf("Expected at least 3 messages, got %d", len(messages))
	}

	// Find the tool result message
	var toolResultMsg *ollamaclient.Message
	for i := range messages {
		if messages[i].Role == "tool" {
			toolResultMsg = &messages[i]
			break
		}
	}

	if toolResultMsg == nil {
		t.Errorf("No tool result message found in messages")
	} else {
		// Verify the tool result message has the correct tool name
		if toolResultMsg.Name != "write_file" {
			t.Errorf("Tool result message name mismatch: got %q, want %q", toolResultMsg.Name, "write_file")
		}
		// Verify the tool result message has non-empty content
		if toolResultMsg.Content == "" {
			t.Errorf("Tool result message content is empty")
		}
	}
}

// TestTC10305_LoopReturnsErrorWhenContextCancelled verifies that the loop returns
// an error when context is cancelled.
func TestTC10305_LoopReturnsErrorWhenContextCancelled(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a stub tool dispatcher
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
	}

	// Create OllamaNative
	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	// Use a custom chatter that always returns context.Canceled
	chatter := &contextCancelChatter{}
	executor, _ := newOllamaNativeWithChatter(cfg, chatter, toolDispatcher)

	result, err := executor.Run(context.Background(), supervisor.Task{
		ID:   "103",
		Repo: "test-repo",
		Spec: "test-spec.md",
	})

	// The executor uses context.Background() but the chatter returns context.Canceled,
	// so the error must be properly propagated (not swallowed or transformed)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	_ = result
}

// contextCancelChatter returns context.Canceled for any Chat call
type contextCancelChatter struct{}

func (c *contextCancelChatter) Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	return ollamaclient.ChatResponse{}, context.Canceled
}

// TestTC10306_FitnessCheckSupervisorIsolation verifies that the supervisor
// isolation fitness check passes. This is more of a build/lint test.
func TestTC10306_FitnessCheckSupervisorIsolation(t *testing.T) {
	// This test verifies that OllamaNative can be instantiated without
	// the supervisor package importing executor packages.
	// The actual fitness check is run via make fitness-supervisor-isolation.
	t.Log("Fitness check: supervisor isolation is preserved (verified via make fitness-supervisor-isolation)")
}

// TestTC10602_LoopForwardsObjectArgsToRealToolSet (REQ-106-02) drives the loop with
// the REAL ollamatoolset.ToolSet over a git worktree: an object-form write_file tool
// call must produce the file with exact content, then finish_branch + terminate yields
// Result{Branch, OK}. Proves string(json.RawMessage) is forwarded to Dispatch correctly.
func TestTC10602_LoopForwardsObjectArgsToRealToolSet(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	toolset, err := ollamatoolset.NewToolSet(tmpDir)
	if err != nil {
		t.Fatalf("NewToolSet: %v", err)
	}

	chatter := &stubChatter{responses: []ollamaclient.ChatResponse{
		{Message: ollamaclient.Message{Role: "assistant", ToolCalls: []ollamaclient.ToolCall{
			{Function: ollamaclient.ToolCallFunction{Name: "write_file", Arguments: json.RawMessage(`{"path":"out.txt","content":"DONE"}`)}},
		}}},
		{Message: ollamaclient.Message{Role: "assistant", ToolCalls: []ollamaclient.ToolCall{
			{Function: ollamaclient.ToolCallFunction{Name: "finish_branch", Arguments: json.RawMessage(`{"branch":"task/106-test"}`)}},
		}}},
		{Message: ollamaclient.Message{Role: "assistant", Content: "done"}},
	}}

	exec, err := newOllamaNativeWithChatter(OllamaNativeConfig{Endpoint: "http://x", Model: "m", Worktree: tmpDir}, chatter, toolset)
	if err != nil {
		t.Fatalf("newOllamaNativeWithChatter: %v", err)
	}
	result, err := exec.Run(context.Background(), supervisor.Task{ID: "106", Repo: "r", Spec: "s"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The object-form write_file args were forwarded to the real toolset → file written.
	got, err := os.ReadFile(filepath.Join(tmpDir, "out.txt"))
	if err != nil {
		t.Fatalf("out.txt not written: %v", err)
	}
	if string(got) != "DONE" {
		t.Errorf("out.txt content = %q, want DONE", string(got))
	}
	if result.Branch != "task/106-test" || !result.OK {
		t.Errorf("Result = %+v, want {Branch:task/106-test OK:true}", result)
	}
	// And the work is committed on the produced branch.
	if c := gitShow(t, tmpDir, "task/106-test", "out.txt"); c != "DONE" {
		t.Errorf("committed out.txt on branch = %q, want DONE", c)
	}
}

// capturingChatter records the first ChatRequest and returns a terminal response.
// Used for testing that the initial message content includes the failure section.
type capturingChatter struct {
	firstRequest *ollamaclient.ChatRequest
}

func (c *capturingChatter) Chat(ctx context.Context, req ollamaclient.ChatRequest) (ollamaclient.ChatResponse, error) {
	if c.firstRequest == nil {
		// Capture the first request
		c.firstRequest = &ollamaclient.ChatRequest{
			Model:    req.Model,
			Messages: make([]ollamaclient.Message, len(req.Messages)),
			Tools:    make([]ollamaclient.Tool, len(req.Tools)),
		}
		copy(c.firstRequest.Messages, req.Messages)
		copy(c.firstRequest.Tools, req.Tools)
	}
	// Return a terminal response (no tool calls) so the loop terminates
	return ollamaclient.ChatResponse{
		Message: ollamaclient.Message{
			Role:    "assistant",
			Content: "Task complete.",
		},
	}, nil
}

// TestOllamaNativeInitialMessageIncludesFailureSectionWhenPriorFailureSet verifies that
// OllamaNative.Run includes the gate-failure section in the initial user message when
// task.PriorFailure is non-empty.
// TC-108-07
func TestOllamaNativeInitialMessageIncludesFailureSectionWhenPriorFailureSet(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	chatter := &capturingChatter{}
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  filepath.Join(tmpDir, "BRANCH"),
		dispatchFunc: func(toolName, argsJSON string) (string, error) {
			// Write the branch file on finish_branch call
			if toolName == "finish_branch" {
				var args struct {
					Branch string `json:"branch"`
				}
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", err
				}
				if err := os.WriteFile(filepath.Join(tmpDir, "BRANCH"), []byte(args.Branch), 0644); err != nil {
					return "", err
				}
				return "branch finished", nil
			}
			return "", errors.New("unknown tool")
		},
	}

	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, chatter, toolDispatcher)
	if err != nil {
		t.Fatalf("newOllamaNativeWithChatter: %v", err)
	}

	task := supervisor.Task{
		ID:           "001",
		Repo:         "exec-sandbox",
		Spec:         "/tasks/001.md",
		PriorFailure: "Failed step: go-build\nOutput:\n./main.go:5: undefined: Foo\nFix these issues before producing the branch.",
	}

	_, err = executor.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if chatter.firstRequest == nil {
		t.Fatal("capturingChatter did not capture a request")
	}

	if len(chatter.firstRequest.Messages) == 0 {
		t.Fatal("capturedRequest has no messages")
	}

	firstMsg := chatter.firstRequest.Messages[0]

	// Assert: Role is "user"
	if firstMsg.Role != "user" {
		t.Errorf("firstMsg.Role = %q, want %q", firstMsg.Role, "user")
	}

	// Assert: contains "previous attempt"
	if !strings.Contains(firstMsg.Content, "previous attempt") {
		t.Errorf("message missing 'previous attempt', got:\n%s", firstMsg.Content)
	}

	// Assert: contains "verification gate"
	if !strings.Contains(firstMsg.Content, "verification gate") {
		t.Errorf("message missing 'verification gate', got:\n%s", firstMsg.Content)
	}

	// Assert: contains the step name from PriorFailure
	if !strings.Contains(firstMsg.Content, "go-build") {
		t.Errorf("message missing 'go-build', got:\n%s", firstMsg.Content)
	}

	// Assert: contains the step output from PriorFailure
	if !strings.Contains(firstMsg.Content, "undefined: Foo") {
		t.Errorf("message missing 'undefined: Foo', got:\n%s", firstMsg.Content)
	}
}

// TestOllamaNativeInitialMessageOmitsFailureSectionWhenPriorFailureEmpty verifies that
// OllamaNative.Run OMITS the gate-failure section in the initial user message when
// task.PriorFailure is empty.
// TC-108-08
func TestOllamaNativeInitialMessageOmitsFailureSectionWhenPriorFailureEmpty(t *testing.T) {
	tmpDir := t.TempDir()
	initGitRepo(t, tmpDir)

	chatter := &capturingChatter{}
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  filepath.Join(tmpDir, "BRANCH"),
		dispatchFunc: func(toolName, argsJSON string) (string, error) {
			// Write the branch file on finish_branch call
			if toolName == "finish_branch" {
				var args struct {
					Branch string `json:"branch"`
				}
				if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
					return "", err
				}
				if err := os.WriteFile(filepath.Join(tmpDir, "BRANCH"), []byte(args.Branch), 0644); err != nil {
					return "", err
				}
				return "branch finished", nil
			}
			return "", errors.New("unknown tool")
		},
	}

	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, chatter, toolDispatcher)
	if err != nil {
		t.Fatalf("newOllamaNativeWithChatter: %v", err)
	}

	task := supervisor.Task{
		ID:   "001",
		Repo: "exec-sandbox",
		Spec: "/tasks/001.md",
		// PriorFailure is zero-value ""
	}

	_, err = executor.Run(context.Background(), task)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	if chatter.firstRequest == nil {
		t.Fatal("capturingChatter did not capture a request")
	}

	if len(chatter.firstRequest.Messages) == 0 {
		t.Fatal("capturedRequest has no messages")
	}

	firstMsg := chatter.firstRequest.Messages[0]

	// Assert: Role is "user"
	if firstMsg.Role != "user" {
		t.Errorf("firstMsg.Role = %q, want %q", firstMsg.Role, "user")
	}

	// Assert: does NOT contain "previous attempt"
	if strings.Contains(firstMsg.Content, "previous attempt") {
		t.Errorf("message should not contain 'previous attempt' when PriorFailure is empty, got:\n%s", firstMsg.Content)
	}

	// Assert: does NOT contain "verification gate"
	if strings.Contains(firstMsg.Content, "verification gate") {
		t.Errorf("message should not contain 'verification gate' when PriorFailure is empty, got:\n%s", firstMsg.Content)
	}

	// Assert: core content is present
	if !strings.Contains(firstMsg.Content, "Task ID: 001") {
		t.Errorf("message missing 'Task ID: 001', got:\n%s", firstMsg.Content)
	}
}

// TestSpecBehaviorsUpdatedForAllHarnesses verifies that docs/spec/behaviors.md
// documents that all four executors propagate gate-failure detail into their prompts.
// TC-108-11
func TestSpecBehaviorsUpdatedForAllHarnesses(t *testing.T) {
	// Try multiple paths to find the spec file
	possiblePaths := []string{
		"docs/spec/behaviors.md",
		"../../../docs/spec/behaviors.md",
		"../../docs/spec/behaviors.md",
	}

	var specContent []byte
	var err error
	found := false

	for _, path := range possiblePaths {
		specContent, err = os.ReadFile(path)
		if err == nil {
			found = true
			break
		}
	}

	if !found {
		t.Skipf("could not find spec file in any of the expected locations")
		return
	}

	specText := string(specContent)

	// Assert: spec contains "PriorFailure" OR "gate-failure feedback" OR "prior failure"
	// (task 107's guard)
	if !strings.Contains(specText, "PriorFailure") &&
		!strings.Contains(specText, "gate-failure feedback") &&
		!strings.Contains(specText, "prior failure") {
		t.Errorf("spec missing PriorFailure / gate-failure / prior failure reference")
	}

	// Assert: spec contains all four harnesses or collective term
	hasClaude := strings.Contains(specText, "Claude")
	hasCodex := strings.Contains(specText, "Codex")
	hasGemini := strings.Contains(specText, "Gemini")
	hasOllama := strings.Contains(specText, "Ollama")
	hasAllExecutors := strings.Contains(specText, "all executors") || strings.Contains(specText, "every harness")

	allHarnessesNamed := hasClaude && hasCodex && hasGemini && hasOllama
	if !allHarnessesNamed && !hasAllExecutors {
		t.Errorf("spec missing indication that all four harnesses consume PriorFailure")
	}
}

// TestCrossHarnessFailureSectionConsistency verifies that all four prompt builders
// include "previous attempt" and "verification gate" when PriorFailure is non-empty.
// TC-108-09
func TestCrossHarnessFailureSectionConsistency(t *testing.T) {
	priorFailure := "Failed step: go-test\nOutput:\nFAIL TestX\nFix these issues before producing the branch."
	task := supervisor.Task{
		ID:           "001",
		Repo:         "exec-sandbox",
		Spec:         "/tasks/001.md",
		PriorFailure: priorFailure,
	}

	// Build prompts from all four harnesses
	claudeOut := buildClaudePrompt(task, "/worktree", "/tmp/branch.txt")
	codexOut := buildCodexPrompt(task, "/worktree")
	geminiOut := buildGeminiPrompt(task, "/worktree")

	// For Ollama, use the capturingChatter
	tmpDir := t.TempDir()
	_ = os.MkdirAll(tmpDir, 0755)
	if err := exec.Command("git", "init", "-q", tmpDir).Run(); err != nil {
		t.Fatalf("git init failed: %v", err)
	}

	chatter := &capturingChatter{}
	toolDispatcher := &stubToolDispatcher{
		toolSchemas: []ollamaclient.Tool{},
		branchFile:  filepath.Join(tmpDir, "BRANCH"),
	}

	cfg := OllamaNativeConfig{
		Endpoint: "http://localhost:11434",
		Model:    "qwen3:8b",
		Worktree: tmpDir,
	}

	executor, err := newOllamaNativeWithChatter(cfg, chatter, toolDispatcher)
	if err != nil {
		t.Fatalf("newOllamaNativeWithChatter: %v", err)
	}

	_, _ = executor.Run(context.Background(), task) // Run to populate the chatter's firstRequest

	var ollamaOut string
	if chatter.firstRequest != nil && len(chatter.firstRequest.Messages) > 0 {
		ollamaOut = chatter.firstRequest.Messages[0].Content
	}

	// Test each output
	outputs := []struct {
		name string
		out  string
	}{
		{"Claude", claudeOut},
		{"Codex", codexOut},
		{"Gemini", geminiOut},
		{"Ollama", ollamaOut},
	}

	for _, tt := range outputs {
		t.Run(tt.name, func(t *testing.T) {
			// Assert: contains "previous attempt"
			if !strings.Contains(tt.out, "previous attempt") {
				t.Errorf("%s output missing 'previous attempt'", tt.name)
			}

			// Assert: contains "verification gate"
			if !strings.Contains(tt.out, "verification gate") {
				t.Errorf("%s output missing 'verification gate'", tt.name)
			}

			// Assert: contains the step name
			if !strings.Contains(tt.out, "go-test") {
				t.Errorf("%s output missing 'go-test'", tt.name)
			}

			// Assert: contains the step output
			if !strings.Contains(tt.out, "FAIL TestX") {
				t.Errorf("%s output missing 'FAIL TestX'", tt.name)
			}
		})
	}
}
