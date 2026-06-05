package publisher_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/publisher"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestBranchPRPublication(t *testing.T) {
	worktree := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	logPath := filepath.Join(t.TempDir(), "publish.log")
	git := writePublisherShim(t, bin, "git", fmt.Sprintf(`#!/bin/sh
set -eu
printf 'git %%s\n' "$*" >> %s
exit 0
`, shellQuote(logPath)))
	gh := writePublisherShim(t, bin, "gh", fmt.Sprintf(`#!/bin/sh
set -eu
printf 'gh %%s\n' "$*" >> %s
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
	exit 1
fi
printf 'https://github.com/acme/repo/pull/34\n'
`, shellQuote(logPath)))

	pub := publisher.NewGitHubCLI(publisher.GitHubCLIConfig{
		GitPath:  git,
		GHPath:   gh,
		Worktree: worktree,
		Remote:   "origin",
	})
	result, err := pub.Publish(context.Background(), publisher.Request{
		Task:   supervisor.Task{ID: "034", Repo: "agent-builder", Spec: "docs/tasks/backlog/034-branch-pr-publication.md"},
		Branch: "task/034-branch-pr-publication",
	})
	if err != nil {
		t.Fatalf("TC-001 Publish error = %v, want nil", err)
	}
	if result.PRURL != "https://github.com/acme/repo/pull/34" || result.PRID != "34" {
		t.Fatalf("TC-001 result = %+v, want PR URL and number", result)
	}
	log := readText(t, logPath)
	if !strings.Contains(log, "git push origin task/034-branch-pr-publication") {
		t.Fatalf("TC-001 git push log = %q, want branch push", log)
	}
	if !strings.Contains(log, "gh pr view --head task/034-branch-pr-publication") ||
		!strings.Contains(log, "gh pr create --head task/034-branch-pr-publication --fill") {
		t.Fatalf("TC-001 gh log = %q, want existing-PR check then create", log)
	}
	t.Log("TC-001 verified branch published as PR artifact")

	_, err = pub.Publish(context.Background(), publisher.Request{Branch: " \t "})
	if !errors.Is(err, publisher.ErrBlankBranch) {
		t.Fatalf("TC-002 blank branch Publish error = %v, want ErrBlankBranch", err)
	}
}

func TestPublisherFailureRedactsSecrets(t *testing.T) {
	worktree := t.TempDir()
	bin := filepath.Join(t.TempDir(), "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	gitToken := "git-secret-034"
	ghToken := "gh-secret-034"
	git := writePublisherShim(t, bin, "git", "#!/bin/sh\nexit 0\n")
	gh := writePublisherShim(t, bin, "gh", `#!/bin/sh
set -eu
if [ "$1" = "pr" ] && [ "$2" = "view" ]; then
	exit 1
fi
printf 'stdout token %s\n' "$GH_TOKEN"
printf 'stderr token %s\n' "$GITHUB_TOKEN" >&2
exit 42
`)

	pub := publisher.NewGitHubCLI(publisher.GitHubCLIConfig{
		GitPath:     git,
		GHPath:      gh,
		Worktree:    worktree,
		Remote:      "origin",
		GitToken:    gitToken,
		GitHubToken: ghToken,
	})
	_, err := pub.Publish(context.Background(), publisher.Request{Branch: "task/034-branch-pr-publication"})
	if err == nil {
		t.Fatalf("TC-003 Publish error = nil, want PR creation failure")
	}
	message := err.Error()
	if strings.Contains(message, gitToken) || strings.Contains(message, ghToken) {
		t.Fatalf("TC-004 publisher error leaked token: %s", message)
	}
	if !strings.Contains(message, "[REDACTED]") {
		t.Fatalf("TC-004 publisher error = %q, want redaction marker", message)
	}
	t.Log("TC-003 publication failure surfaced")
	t.Log("TC-004 publication secrets redacted")
}

func writePublisherShim(t *testing.T, dir, name, script string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write %s shim: %v", name, err)
	}
	return path
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
