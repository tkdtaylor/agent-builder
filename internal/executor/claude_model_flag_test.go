package executor

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Task 144 (ADR 061): the Claude executor and completer pass `--model <ModelID>` when
// the selected registry entry sets one, and omit the flag when it does not (preserving
// the CLI default). These tests capture the real argv via a stubbed cmdFactory.

// stubClaudeExecFactory records the argv and satisfies RunContext's post-run branch read
// by writing the branch file synchronously from the prompt marker, then runs /bin/true.
func stubClaudeExecFactory(t *testing.T, branch string, cap *capturedCmd) claudeCommandCreator {
	t.Helper()
	return func(ctx context.Context, name string, args ...string) *exec.Cmd {
		cap.setAgyCommand(name, args)
		if len(args) >= 2 && args[0] == "-p" {
			const marker = "write only the produced branch name to this file:\n"
			if idx := strings.Index(args[1], marker); idx >= 0 {
				path := strings.TrimSpace(args[1][idx+len(marker):])
				_ = os.WriteFile(path, []byte(branch+"\n"), 0o600)
			}
		}
		cmd := exec.CommandContext(ctx, "/bin/true")
		cap.set(cmd)
		return cmd
	}
}

func runExecTask(t *testing.T, cli *ClaudeCLI) {
	t.Helper()
	task := supervisor.Task{ID: "144", Repo: "agent-builder", Spec: "docs/tasks/backlog/144-claude-honor-model-id.md"}
	result, err := cli.Run(task)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !result.OK {
		t.Fatal("result.OK = false, want true")
	}
}

// TC-144-01: model set → argv is `-p <prompt> --model <model>`.
func TestClaudeCLIRunPassesModelWhenSet(t *testing.T) {
	const model = "claude-haiku-4-5-20251001"
	cap := &capturedCmd{}
	cli := NewClaudeCLI(ClaudeCLIConfig{CLIPath: "claude", Worktree: t.TempDir(), AuthToken: "sk-test", Model: model})
	cli.cmdFactory = stubClaudeExecFactory(t, "task/144-x", cap)

	runExecTask(t, cli)

	_, args := cap.getAgyCommand()
	if len(args) != 4 || args[0] != "-p" || args[2] != "--model" || args[3] != model {
		t.Fatalf("args = %v, want [-p <prompt> --model %s]", args, model)
	}
}

// TC-144-02: empty model → argv is exactly `-p <prompt>`, no --model.
func TestClaudeCLIRunOmitsModelWhenEmpty(t *testing.T) {
	cap := &capturedCmd{}
	cli := NewClaudeCLI(ClaudeCLIConfig{CLIPath: "claude", Worktree: t.TempDir(), AuthToken: "sk-test", Model: ""})
	cli.cmdFactory = stubClaudeExecFactory(t, "task/144-y", cap)

	runExecTask(t, cli)

	_, args := cap.getAgyCommand()
	if len(args) != 2 || args[0] != "-p" {
		t.Fatalf("args = %v, want [-p <prompt>]", args)
	}
	for _, a := range args {
		if a == "--model" {
			t.Fatalf("args unexpectedly contains --model: %v", args)
		}
	}
}

// TC-144-03: NewClaudeCLIFromEntry threads entry.ModelID for both cloud and local entries.
func TestNewClaudeCLIFromEntryThreadsModelID(t *testing.T) {
	cases := []struct {
		name    string
		entry   registry.RegistryEntry
		wantMdl string
	}{
		{
			name:    "cloud",
			entry:   registry.RegistryEntry{ID: "claude-oauth", Harness: registry.HarnessClaudeCLI, ModelID: "claude-opus-4-8", SecretRef: "anthropic-key"},
			wantMdl: "claude-opus-4-8",
		},
		{
			name:    "local",
			entry:   registry.RegistryEntry{ID: "local-qwen", Harness: registry.HarnessClaudeCLI, ModelID: "qwen2.5-coder-7b", Endpoint: "http://localhost:4000", SecretRef: ""},
			wantMdl: "qwen2.5-coder-7b",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cap := &capturedCmd{}
			// Cloud entries resolve a token from the source; local entries use baseURL and
			// ignore it — a token-bearing source satisfies validate() in both cases.
			cli := NewClaudeCLIFromEntry(tc.entry, &fakeClaudeSecretSource{authToken: "sk-test"}, t.TempDir())
			cli.cmdFactory = stubClaudeExecFactory(t, "task/144-"+tc.name, cap)

			runExecTask(t, cli)

			_, args := cap.getAgyCommand()
			if len(args) != 4 || args[2] != "--model" || args[3] != tc.wantMdl {
				t.Fatalf("args = %v, want --model %s", args, tc.wantMdl)
			}
		})
	}
}

// TC-144-04: completer with a model set → argv appends `--model <model>`.
func TestClaudeCompleterPassesModelWhenSet(t *testing.T) {
	const model = "claude-sonnet-5"
	cap := &capturedCmd{}
	entry := registry.RegistryEntry{ID: "claude-sonnet", Harness: registry.HarnessClaudeCLI, ModelID: model, Endpoint: "http://localhost:4000", SecretRef: ""}
	comp := newClaudeCompleter(entry, &fakeClaudeSecretSource{})
	comp.cmdFactory = stubClaudeCompleterFactory(t, "ok", 0, cap)

	if _, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi"); err != nil {
		t.Fatalf("Complete error = %v", err)
	}

	_, args := cap.getAgyCommand()
	want := []string{"-p", "hi", "--model", model}
	if len(args) != len(want) || args[0] != want[0] || args[1] != want[1] || args[2] != want[2] || args[3] != want[3] {
		t.Fatalf("args = %v, want %v", args, want)
	}
}

// TC-144-05: completer with an empty model → argv is exactly `-p <prompt>`.
func TestClaudeCompleterOmitsModelWhenEmpty(t *testing.T) {
	cap := &capturedCmd{}
	entry := registry.RegistryEntry{ID: "local", Harness: registry.HarnessClaudeCLI, ModelID: "", Endpoint: "http://localhost:4000", SecretRef: ""}
	comp := newClaudeCompleter(entry, &fakeClaudeSecretSource{})
	comp.cmdFactory = stubClaudeCompleterFactory(t, "ok", 0, cap)

	if _, err := comp.Complete(context.Background(), registry.RegistryEntry{}, "hi"); err != nil {
		t.Fatalf("Complete error = %v", err)
	}

	_, args := cap.getAgyCommand()
	if len(args) != 2 || args[0] != "-p" || args[1] != "hi" {
		t.Fatalf("args = %v, want [-p hi]", args)
	}
	for _, a := range args {
		if a == "--model" {
			t.Fatalf("args unexpectedly contains --model: %v", args)
		}
	}
}

// TC-144-06: setting the model does not disturb env construction — a cloud entry still
// injects its OAuth token and an isolated HOME (≠ worktree).
func TestClaudeModelFlagLeavesEnvUnchanged(t *testing.T) {
	entry := registry.RegistryEntry{ID: "claude-oauth", Harness: registry.HarnessClaudeCLI, ModelID: "claude-opus-4-8", SecretRef: "anthropic-oauth"}
	src := &fakeClaudeSecretSource{oauthToken: "tok-xyz"}
	worktree := t.TempDir()
	cap := &capturedCmd{}
	cli := NewClaudeCLIFromEntry(entry, src, worktree)
	cli.cmdFactory = stubClaudeExecFactory(t, "task/144-env", cap)

	runExecTask(t, cli)

	cmd := cap.get()
	if cmd == nil {
		t.Fatal("subprocess command was not captured")
	}
	var foundOAuth, foundHome bool
	for _, e := range cmd.Env {
		if e == ClaudeCLIOAuthEnv+"=tok-xyz" {
			foundOAuth = true
		}
		if strings.HasPrefix(e, "HOME=") && strings.TrimPrefix(e, "HOME=") != worktree {
			foundHome = true
		}
	}
	if !foundOAuth {
		t.Errorf("%s=tok-xyz not found in env: %v", ClaudeCLIOAuthEnv, cmd.Env)
	}
	if !foundHome {
		t.Errorf("isolated HOME (≠ worktree) not found in env: %v", cmd.Env)
	}
}
