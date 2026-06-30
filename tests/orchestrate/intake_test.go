package orchestrate_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "agent-builder")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate current test file")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))

	cmd := exec.Command("go", "build", "-o", binary, "./cmd/agent-builder")
	cmd.Dir = repoRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build runtime binary: %v\n%s", err, output)
	}
	return binary
}

// TC-128-07: Orchestrate command intake interactive session (stdin/stdout/exit)
func TestTC128_07_IntakeInteractiveSession(t *testing.T) {
	binary := buildBinary(t)

	tmpDir := t.TempDir()
	shims := []string{"git", "dep-scan", "code-scanner", "golangci-lint", "armor", "gods"}
	for _, shim := range shims {
		shimPath := filepath.Join(tmpDir, shim)
		script := "#!/bin/sh\nexit 0\n"
		if err := os.WriteFile(shimPath, []byte(script), 0o755); err != nil {
			t.Fatalf("write shim %s: %v", shim, err)
		}
	}

	taskRoot := t.TempDir()
	roadmapDir := filepath.Join(taskRoot, "docs/plans")
	if err := os.MkdirAll(roadmapDir, 0o755); err != nil {
		t.Fatalf("mkdir roadmap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roadmapDir, "roadmap.md"), []byte("# Roadmap\n"), 0o644); err != nil {
		t.Fatalf("write roadmap: %v", err)
	}

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskRoot, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("write dummy go.mod: %v", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	hexKey := make([]byte, 128)
	const hexdigits = "0123456789abcdef"
	for i, b := range priv {
		hexKey[i*2] = hexdigits[b>>4]
		hexKey[i*2+1] = hexdigits[b&0x0f]
	}
	keyPath := filepath.Join(tmpDir, "signing.key")
	if err := os.WriteFile(keyPath, hexKey, 0o600); err != nil {
		t.Fatalf("write signing key: %v", err)
	}

	cmd := exec.Command(binary, "orchestrate")
	cmd.Env = append(os.Environ(),
		"AGENT_BUILDER_GOAL_SPEC=fix bugs",
		"AGENT_BUILDER_GOAL_ID=goal-1",
		"AGENT_BUILDER_INBOUND=env",
		"AGENT_BUILDER_TASK_ROOT="+taskRoot,
		"AGENT_BUILDER_WORKTREE="+worktree,
		"AGENT_BUILDER_PUBLISH_REMOTE=origin",
		"AGENT_BUILDER_RUN_TIMEOUT=5m",
		"AGENT_BUILDER_MAX_ATTEMPTS=2",
		"ANTHROPIC_API_KEY=test-key-not-used",
		"AGENT_BUILDER_WORKER_SIGNING_KEY="+keyPath,
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENT_BUILDER_POLICY_RISK=low",
		"AGENT_BUILDER_POLICY_SOCKET=",
		"AGENT_BUILDER_VAULT_SOCKET=",
	)

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start cmd: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var stdoutStr string
	for time.Now().Before(deadline) {
		stdoutStr = stdoutBuf.String()
		if strings.Contains(stdoutStr, "repository") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(stdoutStr, "repository") {
		t.Fatalf("expected stdout to prompt for repository, got:\n%s\nstderr:\n%s", stdoutStr, stderrBuf.String())
	}

	_, err = io.WriteString(stdinPipe, "info goal-1 repo: github.com/tkdtaylor/exec-sandbox\n")
	if err != nil {
		t.Fatalf("write info: %v", err)
	}

	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		stdoutStr = stdoutBuf.String()
		if strings.Contains(stdoutStr, "confirm goal-1") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !strings.Contains(stdoutStr, "confirm goal-1") {
		t.Fatalf("expected stdout to show ready prompt, got:\n%s\nstderr:\n%s", stdoutStr, stderrBuf.String())
	}

	_, err = io.WriteString(stdinPipe, "confirm goal-1\n")
	if err != nil {
		t.Fatalf("write confirm: %v", err)
	}
	_ = stdinPipe.Close()

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cmd exited with error: %v. Stderr:\n%s\nStdout:\n%s", err, stderrBuf.String(), stdoutBuf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for command exit")
	}
}

// TC-128-08: Orchestrate command intake auto escape hatch
func TestTC128_08_IntakeAutoEscapeHatch(t *testing.T) {
	binary := buildBinary(t)

	tmpDir := t.TempDir()
	shims := []string{"git", "dep-scan", "code-scanner", "golangci-lint", "armor", "gods"}
	for _, shim := range shims {
		shimPath := filepath.Join(tmpDir, shim)
		script := "#!/bin/sh\nexit 0\n"
		if err := os.WriteFile(shimPath, []byte(script), 0o755); err != nil {
			t.Fatalf("write shim %s: %v", shim, err)
		}
	}

	taskRoot := t.TempDir()
	roadmapDir := filepath.Join(taskRoot, "docs/plans")
	if err := os.MkdirAll(roadmapDir, 0o755); err != nil {
		t.Fatalf("mkdir roadmap: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roadmapDir, "roadmap.md"), []byte("# Roadmap\n"), 0o644); err != nil {
		t.Fatalf("write roadmap: %v", err)
	}

	worktree := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskRoot, "go.mod"), []byte("module test\n"), 0o644); err != nil {
		t.Fatalf("write dummy go.mod: %v", err)
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	hexKey := make([]byte, 128)
	const hexdigits = "0123456789abcdef"
	for i, b := range priv {
		hexKey[i*2] = hexdigits[b>>4]
		hexKey[i*2+1] = hexdigits[b&0x0f]
	}
	keyPath := filepath.Join(tmpDir, "signing.key")
	if err := os.WriteFile(keyPath, hexKey, 0o600); err != nil {
		t.Fatalf("write signing key: %v", err)
	}

	cmd := exec.Command(binary, "orchestrate")
	cmd.Env = append(os.Environ(),
		"AGENT_BUILDER_GOAL_SPEC=repo: github.com/tkdtaylor/exec-sandbox\nspec: fix bug",
		"AGENT_BUILDER_GOAL_ID=goal-1",
		"AGENT_BUILDER_INTAKE=auto",
		"AGENT_BUILDER_INBOUND=env",
		"AGENT_BUILDER_TASK_ROOT="+taskRoot,
		"AGENT_BUILDER_WORKTREE="+worktree,
		"AGENT_BUILDER_PUBLISH_REMOTE=origin",
		"AGENT_BUILDER_RUN_TIMEOUT=5m",
		"AGENT_BUILDER_MAX_ATTEMPTS=2",
		"ANTHROPIC_API_KEY=test-key-not-used",
		"AGENT_BUILDER_WORKER_SIGNING_KEY="+keyPath,
		"PATH="+tmpDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENT_BUILDER_POLICY_RISK=low",
		"AGENT_BUILDER_POLICY_SOCKET=",
		"AGENT_BUILDER_VAULT_SOCKET=",
	)

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		t.Fatalf("start cmd: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cmd exited with error: %v. Stderr:\n%s\nStdout:\n%s", err, stderrBuf.String(), stdoutBuf.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for command exit under AGENT_BUILDER_INTAKE=auto")
	}

	stdoutStr := stdoutBuf.String()
	if strings.Contains(stdoutStr, "clarifying") || strings.Contains(stdoutStr, "ready") {
		t.Fatalf("did not expect clarification output in auto mode, got:\n%s", stdoutStr)
	}
}
