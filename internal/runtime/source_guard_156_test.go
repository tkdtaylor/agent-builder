package runtime

// Task 156 — source-inspection guards (L1) that lock in the comment/doc fixes so a
// future edit cannot silently reintroduce the misleading text this task removed.
// These read sibling source files by relative path (go test runs with cwd set to
// the package source dir, so ../supervisor and ../executor resolve).
//
// TC-156-04 / TC-156-05: the false comment ("the production box.Kill stops the real
//   process") is gone from internal/supervisor/cancel_test.go.
// TC-156-06: internal/runtime/run.go's sandboxBox.Kill/Teardown doc comments state
//   they are intentional no-ops and that termination rides the cancelled context.
// TC-156-07: internal/executor/ollama_native.go no longer carries the stale
//   "context should come from supervisor" TODO.
// TC-156-08: the full internal/supervisor, tests/supervisor, internal/executor, and
//   internal/runtime suites pass unchanged — asserted by running those suites (see
//   the task's Verification plan), not by a string guard here.

import (
	"os"
	"strings"
	"testing"
)

func readSource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TC-156-04 / TC-156-05: the false comment is no longer present.
func TestTC156_05_FalseBoxKillCommentRemoved(t *testing.T) {
	src := readSource(t, "../supervisor/cancel_test.go")
	if strings.Contains(src, "the production box.Kill stops the real process") {
		t.Fatal("TC-156-05: cancel_test.go still contains the false 'the production box.Kill stops the real process' comment")
	}
	// It must instead describe the real mechanism (context cancellation), so the
	// removal is a correction, not a deletion that leaves the reader with nothing.
	if !strings.Contains(src, "context being\n\t\t// cancelled") && !strings.Contains(src, "context being cancelled") {
		t.Fatal("TC-156-05: cancel_test.go no longer states the real termination mechanism (run-scoped context cancellation)")
	}
}

// TC-156-06: sandboxBox.Kill/Teardown doc comments accurately state the no-op rationale.
func TestTC156_06_SandboxBoxNoOpCommentsAccurate(t *testing.T) {
	src := readSource(t, "run.go")
	for _, want := range []string{
		"Kill is an intentional no-op",
		"Teardown is an intentional no-op",
		"run-scoped context",
	} {
		if !strings.Contains(src, want) {
			t.Fatalf("TC-156-06: run.go sandboxBox doc comments missing accurate no-op rationale: want substring %q", want)
		}
	}
}

// TC-156-07: the stale ollama TODO is gone.
func TestTC156_07_OllamaStaleTODORemoved(t *testing.T) {
	src := readSource(t, "../executor/ollama_native.go")
	if strings.Contains(src, "context should come from supervisor") {
		t.Fatal("TC-156-07: ollama_native.go still contains the stale 'context should come from supervisor' TODO")
	}
}
