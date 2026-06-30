package cli

import (
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// TC-130-03: Compile-time type assertion to verify that reporterStatusWriter
// satisfies the loop.StatusWriter interface.
var _ loop.StatusWriter = (*reporterStatusWriter)(nil)

func TestReporterStatusWriterSatisfiesLoopStatusWriterInterface(t *testing.T) {
	var writer loop.StatusWriter = &reporterStatusWriter{
		reporter: &recordingReporter{},
	}
	_ = writer
}

// TC-130-01: Verifies that reporterStatusWriter correctly formats the needs-human line.
func TestReporterStatusWriterFormatsNeedsHumanLine(t *testing.T) {
	rep := &recordingReporter{}
	w := &reporterStatusWriter{reporter: rep}

	res, err := w.WriteStatus("goal-7", tasksource.WritableStatusNeedsHuman)
	if err != nil {
		t.Fatalf("WriteStatus failed: %v", err)
	}

	if !res.Changed {
		t.Error("expected StatusWriteResult.Changed to be true")
	}
	if res.Path != "" {
		t.Errorf("expected StatusWriteResult.Path to be empty (no backing file), got %q", res.Path)
	}

	reported := rep.all()
	if len(reported) != 1 {
		t.Fatalf("expected exactly 1 report, got %d", len(reported))
	}

	// Exact expected format: "needs-human: goal goal-7 escalated (needs-human)"
	expected := "needs-human: goal goal-7 escalated (needs-human)"
	if reported[0] != expected {
		t.Errorf("reported text = %q, want %q", reported[0], expected)
	}

	if !strings.Contains(reported[0], "needs-human") {
		t.Errorf("expected reported message to contain 'needs-human': %q", reported[0])
	}
	if !strings.Contains(reported[0], "goal-7") {
		t.Errorf("expected reported message to contain 'goal-7': %q", reported[0])
	}
}

// TC-130-02: Verifies that reporterStatusWriter works successfully for synthetic goal IDs
// with no backing files and returns no filesystem error.
func TestReporterStatusWriterSyntheticGoalID(t *testing.T) {
	rep := &recordingReporter{}
	w := &reporterStatusWriter{reporter: rep}

	// Test with a synthetic goal ID that has no backing task file on disk
	res, err := w.WriteStatus("goal-42", tasksource.WritableStatusNeedsHuman)
	if err != nil {
		t.Fatalf("WriteStatus with synthetic goal ID failed: %v", err)
	}

	if !res.Changed {
		t.Error("expected Changed to be true")
	}

	reported := rep.all()
	if len(reported) != 1 {
		t.Fatalf("expected exactly 1 report, got %d", len(reported))
	}

	expected := "needs-human: goal goal-42 escalated (needs-human)"
	if reported[0] != expected {
		t.Errorf("reported text = %q, want %q", reported[0], expected)
	}
}
