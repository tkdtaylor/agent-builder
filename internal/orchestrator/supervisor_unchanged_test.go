package orchestrator_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TC-081-06: internal/supervisor must be unchanged by this task — the
// orchestrator is purely additive (REQ-081-06). This test asserts the structural
// diff of internal/supervisor against the branch merge-base is empty.
//
// It is a guard, not the primary evidence: the authoritative check is run in the
// pre-commit gate (`git diff <merge-base> -- internal/supervisor/`). The test
// skips (rather than fails) when git is unavailable or the merge-base cannot be
// resolved (e.g. a shallow CI checkout), so it never blocks a legitimate build;
// the gate output in the task report is the binding evidence.
func TestTC081_06_SupervisorUnchanged(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available; supervisor empty-diff is verified by the pre-commit gate")
	}

	mergeBase, err := run("git", "merge-base", "HEAD", "main")
	if err != nil {
		t.Skipf("cannot resolve merge-base with main (%v); empty-diff verified by the gate", err)
	}
	mergeBase = strings.TrimSpace(mergeBase)
	if mergeBase == "" {
		t.Skip("empty merge-base; empty-diff verified by the gate")
	}

	diff, err := run("git", "diff", "--stat", mergeBase, "HEAD", "--", "internal/supervisor/")
	if err != nil {
		t.Skipf("git diff failed (%v); empty-diff verified by the gate", err)
	}
	if strings.TrimSpace(diff) != "" {
		t.Fatalf("TC-081-06 violated: internal/supervisor changed since %s:\n%s", mergeBase, diff)
	}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}
