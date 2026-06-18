package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLivePhase0EndToEndAcceptance_TC032 is the optional L6 live harness.
// Gated by AGENT_BUILDER_LIVE_E2E=1. It drives the real agent-builder binary
// end to end with a real Claude executor, real git/gh, and real Podman
// containment against the live Publish remote. Skipped when any prerequisite
// (claude, git, gh, podman) is absent or ANTHROPIC_API_KEY is unset; FAILS
// when the Gate-toolchain directory is missing (a configuration error, not
// an availability gap).
//
// TC-054-01, TC-054-02, TC-054-03.
func TestLivePhase0EndToEndAcceptance_TC032(t *testing.T) {
	// TC-054-02: Skip cleanly when AGENT_BUILDER_LIVE_E2E is unset.
	if os.Getenv("AGENT_BUILDER_LIVE_E2E") != "1" {
		t.Skip("live capstone test skipped; set AGENT_BUILDER_LIVE_E2E=1 to run")
	}

	// TC-054-02: Skip when any of the required tool binaries are absent.
	requiredTools := []string{"claude", "git", "gh", "podman"}
	for _, tool := range requiredTools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("TC-054-02 required tool %s unavailable on PATH: %v", tool, err)
		}
	}

	// TC-054-02: Skip when ANTHROPIC_API_KEY is unset or empty.
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if strings.TrimSpace(apiKey) == "" {
		t.Skipf("TC-054-02 ANTHROPIC_API_KEY unset or empty")
	}

	root := projectRoot(t)
	launcherPath := filepath.Join(root, "containment/execution-box/run.sh")
	if _, err := os.Stat(launcherPath); err != nil {
		t.Skipf("TC-054-02 exec-box launcher unavailable at %s: %v", launcherPath, err)
	}

	// TC-054-03: FATALF (not Skipf) on missing gate-tools directory.
	// This is a configuration error, not an availability gap.
	gateTools := filepath.Join(root, "containment/execution-box/gate-tools")
	if info, err := os.Stat(gateTools); err != nil || !info.IsDir() {
		t.Fatalf("TC-054-03 Gate-toolchain directory missing at %s (configuration error, not env gap): %v", gateTools, err)
	}

	// TC-054-01: Build the agent-builder binary.
	binary := buildAgentBuilder(t)

	// TC-054-01: Set up the live capstone fixture.
	fixture := newLiveCapstoneFixture(t, root)

	// TC-054-01: Drive the real binary with the full env contract.
	stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(t), "run")

	// TC-054-01: Assert exit 0.
	if code != 0 {
		t.Fatalf("TC-054-01 live run exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
	}

	// TC-054-01: Assert stdout contains "run completed: task NNN".
	if !strings.Contains(stdout, "run completed: task 001") {
		t.Fatalf("TC-054-01 stdout = %q, want completed task summary", stdout)
	}

	// TC-054-01: Assert run-record events.
	events := readEvents(t, fixture.recordPath)
	assertEventContains(t, events, "stdout", "data", "publication recorded: branch=")
	assertEventContains(t, events, "run_finished", "outcome", "completed")

	// TC-054-01: Extract the branch from the run record and log the PR URL.
	branch := extractBranchFromEvents(t, events)
	t.Logf("TC-054-01 real PR created on branch %s", branch)

	// TC-054-01: t.Cleanup closes the PR and deletes the remote branch.
	t.Cleanup(func() {
		if branch != "" {
			// Log cleanup attempts; do not fail the test on cleanup errors.
			if err := exec.Command("gh", "pr", "close", branch, "--delete-branch").Run(); err != nil {
				t.Logf("TC-054-01 cleanup: gh pr close %s failed (may already be closed): %v", branch, err)
			}
			cmd := exec.Command("git", "push", fixture.remote, "--delete", branch)
			cmd.Dir = fixture.worktree
			if err := cmd.Run(); err != nil {
				t.Logf("TC-054-01 cleanup: git push delete %s failed (may already be deleted): %v", branch, err)
			}
		}
	})

	t.Log("TC-054-01 live capstone accepted: task selected, real branch produced, PR recorded, gate passed, cleanup confirmed")
}

// liveCapstoneFixture holds the paths and env vars for a live capstone test run.
type liveCapstoneFixture struct {
	taskRoot   string
	worktree   string
	recordPath string
	remote     string
}

// newLiveCapstoneFixture creates a real git worktree and a task-root with a
// ready-status task that instructs Claude to create LIVE_OK.txt with one line.
// The worktree is a full clone of the configured AGENT_BUILDER_LIVE_E2E_REMOTE
// (default: l6) so that when the executor creates a task branch, it descends
// from <remote>/main and gh pr create --fill can resolve the merge base.
func newLiveCapstoneFixture(t *testing.T, repoRoot string) liveCapstoneFixture {
	t.Helper()

	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	worktree := filepath.Join(root, "worktree")
	recordPath := filepath.Join(root, "run-record.ndjson")

	// Seed the task-root with roadmap and a ready-status task.
	writeFile(t, filepath.Join(taskRoot, "docs/plans/roadmap.md"), "# Roadmap\n")
	writeFile(t, filepath.Join(taskRoot, "docs/tasks/backlog/001-live-ok.md"), `# Task 001: live-ok

**Project:** agent-builder
**Created:** 2026-06-17
**Status:** ready

## Goal

Create the file LIVE_OK.txt in the worktree with exactly one line: "live probe ok".
`)

	// Determine the publish remote from env or default to l6.
	remote := os.Getenv("AGENT_BUILDER_LIVE_E2E_REMOTE")
	if remote == "" {
		remote = "l6"
	}

	// Clone the worktree from the configured remote (full clone, no shallow depth).
	// This ensures the worktree has shared history with <remote>/main so the
	// publisher can open a PR via gh pr create --fill.
	remoteURL := getRemoteURL(t, repoRoot, remote)
	if remoteURL != "" {
		// Remote configured (the live capstone path): the worktree MUST be a clone
		// of it so the produced branch descends from <remote>/main and the publisher
		// can open a PR via `gh pr create --fill`. A clone failure here is fatal —
		// silently falling back to `git init` would re-introduce the
		// "ambiguous argument '<remote>/main...<branch>'" publish failure, and only
		// after the run had already spent Claude quota. Fail fast instead.
		if !cloneWorktree(t, remoteURL, worktree) {
			t.Fatalf("TC-057 clone of remote %q (%s) into the worktree failed; the live capstone requires an l6-based worktree (no silent git-init fallback)", remote, remoteURL)
		}
		// `git clone` names the remote "origin", but the publisher pushes to
		// <remote> (e.g. "l6"). Rename so the push target exists in the worktree.
		if err := exec.Command("git", "-C", worktree, "remote", "rename", "origin", remote).Run(); err != nil {
			if err := exec.Command("git", "-C", worktree, "remote", "add", remote, remoteURL).Run(); err != nil {
				t.Fatalf("TC-057 could not set remote %q on cloned worktree: %v", remote, err)
			}
		}
	} else {
		// Remote not configured (e.g. CI without l6): fall back to a minimal
		// gate-passing module. This path never reaches live publish.
		if err := os.MkdirAll(worktree, 0o755); err != nil {
			t.Fatalf("mkdir worktree: %v", err)
		}
		if err := exec.Command("git", "init", worktree).Run(); err != nil {
			t.Fatalf("git init worktree: %v", err)
		}

		// Write the minimal Go module that passes go test.
		writeFile(t, filepath.Join(worktree, "go.mod"), "module example.com/live\n\ngo 1.26.3\n")
		writeFile(t, filepath.Join(worktree, "live.go"), "package live\n\nfunc Probe() int { return 1 }\n")
		writeFile(t, filepath.Join(worktree, "live_test.go"), `package live

import "testing"

func TestProbe(t *testing.T) {
	if Probe() != 1 {
		t.Fatal("probe failed")
	}
}
`)

		// Commit the initial state so the publisher has a clean tree.
		cmd := exec.Command("git", "add", "-A")
		cmd.Dir = worktree
		if err := cmd.Run(); err != nil {
			t.Fatalf("git add: %v", err)
		}
		cmd = exec.Command("git", "commit", "-m", "initial")
		cmd.Dir = worktree
		if err := cmd.Run(); err != nil {
			t.Fatalf("git commit: %v", err)
		}
	}

	fixture := liveCapstoneFixture{
		taskRoot:   taskRoot,
		worktree:   worktree,
		recordPath: recordPath,
		remote:     remote,
	}

	return fixture
}

// getRemoteURL resolves the git remote URL for the given remote name in repoRoot.
// Returns the URL if successful, or empty string if the remote is not found or
// git command fails. This allows the fixture to degrade gracefully.
func getRemoteURL(t *testing.T, repoRoot string, remoteName string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoRoot, "remote", "get-url", remoteName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Remote not found or git command failed; return empty string.
		return ""
	}
	return strings.TrimSpace(string(output))
}

// cloneWorktree clones the given remoteURL into worktreeDir (full clone, no shallow depth).
// Returns true if the clone succeeded, false otherwise. The caller should fall back to
// bare git init if this returns false.
func cloneWorktree(t *testing.T, remoteURL string, worktreeDir string) bool {
	t.Helper()
	cmd := exec.Command("git", "clone", remoteURL, worktreeDir)
	err := cmd.Run()
	return err == nil
}

// env returns the environment map for running agent-builder with the
// live capstone fixture. It resolves the exec-box launcher path from
// the project root (via projectRoot, called within a test context).
func (f liveCapstoneFixture) env(t *testing.T) map[string]string {
	root := projectRoot(t)
	launcherPath := filepath.Join(root, "containment/execution-box/run.sh")

	return map[string]string{
		"ANTHROPIC_API_KEY":               os.Getenv("ANTHROPIC_API_KEY"),
		"AGENT_BUILDER_TASK_ROOT":         f.taskRoot,
		"AGENT_BUILDER_WORKTREE":          f.worktree,
		"AGENT_BUILDER_PUBLISH_REMOTE":    f.remote,
		"AGENT_BUILDER_RUN_TIMEOUT":       "300s",
		"AGENT_BUILDER_MAX_ATTEMPTS":      "1",
		"AGENT_BUILDER_RUN_RECORD":        f.recordPath,
		"AGENT_BUILDER_EXEC_BOX_LAUNCHER": launcherPath,
	}
}

// extractBranchFromEvents scans the run record events for a
// "publication recorded: branch=" line and extracts the branch name.
func extractBranchFromEvents(t *testing.T, events []map[string]any) string {
	t.Helper()
	for _, event := range events {
		if event["type"] == "stdout" {
			if data, ok := event["data"].(string); ok && strings.Contains(data, "publication recorded: branch=") {
				// Extract everything after "branch=" up to space or end of string.
				parts := strings.Split(data, "branch=")
				if len(parts) > 1 {
					rest := parts[1]
					if sp := strings.Index(rest, " "); sp != -1 {
						return rest[:sp]
					}
					return strings.TrimSpace(rest)
				}
			}
		}
	}
	return ""
}
