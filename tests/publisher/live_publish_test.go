package publisher_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/publisher"
)

// TestLiveBranchPRPublication_TC034 tests live git push and PR creation against
// a real remote, when AGENT_BUILDER_LIVE_PUBLISH=1 is set. The test skips
// cleanly in CI when the flag is unset or prereq binaries are missing.
//
// TC-053-01: when flag set and all prereqs present, creates a temp git repo,
// commits on a unique branch, calls publisher.NewGitHubCLI(...).Publish(...),
// asserts a real PR URL and non-empty PRID, and self-cleans with t.Cleanup.
//
// TC-053-02: when flag unset, test skips with t.Skip.
//
// TC-053-03: when flag set but prereq is missing (git, gh, remote, or
// gh auth), test skips with t.Skipf naming the missing prereq.
func TestLiveBranchPRPublication_TC034(t *testing.T) {
	// TC-053-02: skip cleanly when env flag unset.
	if os.Getenv("AGENT_BUILDER_LIVE_PUBLISH") != "1" {
		t.Skip("live publisher test skipped; set AGENT_BUILDER_LIVE_PUBLISH=1 to run")
	}

	// TC-053-03: check prereq binaries are on PATH.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("TC-053-03 git unavailable on PATH: %v", err)
	}

	ghPath, err := exec.LookPath("gh")
	if err != nil {
		t.Skipf("TC-053-03 gh unavailable on PATH: %v", err)
	}

	// TC-053-03: check gh auth status (unauthenticated is a skip).
	if err := exec.Command("gh", "auth", "status").Run(); err != nil {
		t.Skipf("TC-053-03 gh auth not configured: %v", err)
	}

	// TC-053-03: check configured remote exists in main repo.
	remote := os.Getenv("AGENT_BUILDER_LIVE_PUBLISH_REMOTE")
	if remote == "" {
		remote = "l6"
	}
	remoteURL, err := getRemoteURL(remote)
	if err != nil {
		t.Skipf("TC-053-03 remote %q not configured in main repo: %v", remote, err)
	}

	// Create a temp git repo to simulate a published branch.
	tmpRepo := t.TempDir()
	if err := initGitRepo(t, tmpRepo); err != nil {
		t.Fatalf("failed to init temp git repo: %v", err)
	}

	// Add the real remote URL from the main repo.
	if err := exec.Command("git", "-C", tmpRepo, "remote", "add", remote, remoteURL).Run(); err != nil {
		t.Fatalf("failed to add remote %q to temp repo: %v", remote, err)
	}

	// Create unique branch name: task/034-live-<unix-timestamp>-<pid>.
	branchName := fmt.Sprintf("task/034-live-%d-%d", time.Now().Unix(), os.Getpid())

	// Create a branch and commit a file.
	if err := exec.Command("git", "-C", tmpRepo, "checkout", "-b", branchName).Run(); err != nil {
		t.Fatalf("failed to create branch %q: %v", branchName, err)
	}

	testFile := filepath.Join(tmpRepo, "live-test.txt")
	if err := os.WriteFile(testFile, []byte("live test marker\n"), 0o644); err != nil {
		t.Fatalf("failed to write test file: %v", err)
	}

	if err := exec.Command("git", "-C", tmpRepo, "add", ".").Run(); err != nil {
		t.Fatalf("failed to git add: %v", err)
	}

	if err := exec.Command("git", "-C", tmpRepo, "commit", "-m", "live test commit").Run(); err != nil {
		t.Fatalf("failed to git commit: %v", err)
	}

	// TC-053-01: call publisher.NewGitHubCLI(...).Publish(...).
	pub := publisher.NewGitHubCLI(publisher.GitHubCLIConfig{
		GitPath:  gitPath,
		GHPath:   ghPath,
		Worktree: tmpRepo,
		Remote:   remote,
	})

	result, err := pub.Publish(context.Background(), publisher.Request{
		Branch: branchName,
	})
	if err != nil {
		t.Fatalf("TC-053-01 Publish failed: %v", err)
	}

	// TC-053-01: assert PRURL matches github.com/.+/pull/\d+.
	prURLRegexp := regexp.MustCompile(`github\.com/.+/pull/\d+`)
	if !prURLRegexp.MatchString(result.PRURL) {
		t.Fatalf("TC-053-01 PRURL = %q, want match for github.com/.+/pull/\\d+", result.PRURL)
	}

	// TC-053-01: assert PRID is non-empty.
	if result.PRID == "" {
		t.Fatalf("TC-053-01 PRID empty, want non-empty")
	}

	// TC-053-01: log the real PR URL.
	t.Logf("TC-053-01 live PR created: %s", result.PRURL)

	// TC-053-01: t.Cleanup runs PR close + branch delete.
	t.Cleanup(func() {
		// Close the PR on the live remote. This may fail if the PR was already
		// closed; log but do not fail the test on cleanup failure.
		closePRCmd := exec.Command("gh", "pr", "close", branchName, "--delete-branch")
		closePRCmd.Dir = tmpRepo
		if err := closePRCmd.Run(); err != nil {
			t.Logf("cleanup: gh pr close failed (may already be closed): %v", err)
		}

		// Delete the branch from the remote as belt-and-suspenders. Again, may
		// fail if the branch is already gone; log and continue.
		deleteBranchCmd := exec.Command("git", "-C", tmpRepo, "push", remote, "--delete", branchName)
		if err := deleteBranchCmd.Run(); err != nil {
			t.Logf("cleanup: git push --delete failed (may already be deleted): %v", err)
		}
	})
}

// initGitRepo initializes a git repository in the given directory.
func initGitRepo(t *testing.T, dir string) error {
	t.Helper()
	cmd := exec.Command("git", "init")
	cmd.Dir = dir
	return cmd.Run()
}

// getRemoteURL retrieves the URL for a named remote in the main repo.
func getRemoteURL(remote string) (string, error) {
	mainRepo, err := findMainRepo()
	if err != nil {
		return "", err
	}

	cmd := exec.Command("git", "-C", mainRepo, "remote", "get-url", remote)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	// Trim whitespace and newlines.
	url := string(output)
	for len(url) > 0 && (url[len(url)-1] == '\n' || url[len(url)-1] == '\r' || url[len(url)-1] == ' ') {
		url = url[:len(url)-1]
	}
	return url, nil
}

// findMainRepo walks up from the test's runtime location to find the project root.
// It looks for go.mod to avoid resolving to the wrong root.
func findMainRepo() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		modFile := filepath.Join(cwd, "go.mod")
		if _, err := os.Stat(modFile); err == nil {
			return cwd, nil
		}

		parent := filepath.Dir(cwd)
		if parent == cwd {
			// Reached filesystem root without finding go.mod.
			return "", fmt.Errorf("go.mod not found")
		}
		cwd = parent
	}
}
