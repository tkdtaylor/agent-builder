package supervisor_test

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func TestRunRecordWireFormatAndOutcomeVocabulary(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "run-record.ndjson")

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "019", Repo: "agent-builder", Spec: "docs/tasks/completed/019-run-log-collection.md"}),
		supervisor.WithContainmentBox(&recordBox{handle: supervisor.BoxHandle{ID: "box-019", Worktree: "/work/agent-builder"}}),
		supervisor.WithInBoxLoop(recordLoop{}),
		supervisor.WithRunRecordPath(recordPath),
	).Run()
	if err != nil {
		t.Fatalf("TC-001-RunRecord-Wire-Format: Run() error = %v, want nil", err)
	}

	events := readRunRecord(t, recordPath)
	if len(events) != 3 {
		t.Fatalf("TC-001-RunRecord-Wire-Format: event count = %d, want 3", len(events))
	}
	assertEventBasics(t, "TC-001-RunRecord-Wire-Format", events)
	if got := events[0]["type"]; got != "run_started" {
		t.Fatalf("TC-001-RunRecord-Wire-Format: first event type = %v, want run_started", got)
	}
	if got := events[1]["type"]; got != "command" {
		t.Fatalf("TC-001-RunRecord-Wire-Format: empty-output run command event type = %v, want command", got)
	}
	if got := events[2]["outcome"]; got != string(supervisor.RunOutcomeCompleted) {
		t.Fatalf("TC-005-RunRecord-Outcome-Values: outcome = %v, want completed", got)
	}
	if string(supervisor.RunOutcomeTimedOut) != "timed-out" {
		t.Fatalf("TC-005-RunRecord-Outcome-Values: timed-out outcome = %q, want timed-out", supervisor.RunOutcomeTimedOut)
	}
}

func TestRunRecordStreamsOutputAndPersistsAfterTeardown(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "run-record.ndjson")
	box := &recordBox{handle: supervisor.BoxHandle{ID: "box-019", Worktree: "/work/agent-builder"}}
	loop := recordLoop{
		duringRun: func(streams supervisor.RunStreams) error {
			if _, err := io.WriteString(streams.Stdout, "stdout-one\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Stderr, "stderr-one\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Command, "go test ./...\n"); err != nil {
				return err
			}
			content, err := os.ReadFile(recordPath)
			if err != nil {
				return err
			}
			if !strings.Contains(string(content), "stdout-one") {
				return errors.New("stdout was not visible during RunInside")
			}
			return nil
		},
	}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "019", Repo: "agent-builder", Spec: "docs/tasks/completed/019-run-log-collection.md"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(loop),
		supervisor.WithRunRecordPath(recordPath),
	).Run()
	if err != nil {
		t.Fatalf("TC-002-Stream-Capture: Run() error = %v, want nil", err)
	}
	if !box.tornDown {
		t.Fatal("TC-003-Persist-After-Teardown: fake box was not torn down")
	}

	events := readRunRecord(t, recordPath)
	assertContainsEvent(t, "TC-002-Stream-Capture", events, "stdout", "data", "stdout-one\n")
	assertContainsEvent(t, "TC-002-Stream-Capture", events, "stderr", "data", "stderr-one\n")
	assertContainsEvent(t, "TC-002-Stream-Capture", events, "command", "command", "go test ./...\n")
	assertContainsEvent(t, "TC-003-Persist-After-Teardown", events, "run_finished", "outcome", "completed")
	t.Logf("TC-003-Persist-After-Teardown sample persisted line: %s", firstLine(t, recordPath))
}

func TestRunRecordKeepsPartialStreamWhenLoopFails(t *testing.T) {
	recordPath := filepath.Join(t.TempDir(), "run-record.ndjson")
	loopErr := errors.New("loop failed after partial stream")
	box := &recordBox{handle: supervisor.BoxHandle{ID: "box-019", Worktree: "/work/agent-builder"}}
	loop := recordLoop{
		duringRun: func(streams supervisor.RunStreams) error {
			if _, err := io.WriteString(streams.Stdout, "partial stdout\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Stderr, "partial stderr\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Command, "make check\n"); err != nil {
				return err
			}
			return loopErr
		},
	}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "019"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(loop),
		supervisor.WithRunRecordPath(recordPath),
	).Run()
	if !errors.Is(err, loopErr) {
		t.Fatalf("TC-004-No-Post-Teardown-Readback: Run() error = %v, want %v", err, loopErr)
	}
	if !box.tornDown {
		t.Fatal("TC-004-No-Post-Teardown-Readback: fake box was not torn down")
	}

	events := readRunRecord(t, recordPath)
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "stdout", "data", "partial stdout\n")
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "stderr", "data", "partial stderr\n")
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "command", "command", "make check\n")
	assertContainsEvent(t, "TC-005-RunRecord-Outcome-Values", events, "run_finished", "outcome", "failed")
}

type recordBox struct {
	handle   supervisor.BoxHandle
	tornDown bool
}

func (b *recordBox) Create(supervisor.Task) (supervisor.BoxHandle, error) {
	return b.handle, nil
}

func (b *recordBox) Teardown(supervisor.BoxHandle) error {
	b.tornDown = true
	return nil
}

type recordLoop struct {
	duringRun func(supervisor.RunStreams) error
}

func (l recordLoop) RunInside(_ supervisor.BoxHandle, _ supervisor.Task, streams supervisor.RunStreams) error {
	if l.duringRun == nil {
		return nil
	}
	return l.duringRun(streams)
}

func readRunRecord(t *testing.T, path string) []map[string]any {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run record %s: %v", path, err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(content)), "[") {
		t.Fatalf("TC-001-RunRecord-Wire-Format: run record is array JSON, want NDJSON")
	}

	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	events := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("TC-001-RunRecord-Wire-Format: line is not parseable JSON: %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

func assertEventBasics(t *testing.T, marker string, events []map[string]any) {
	t.Helper()
	for _, event := range events {
		for _, field := range []string{"version", "type", "run_id", "timestamp"} {
			if strings.TrimSpace(asString(event[field])) == "" {
				t.Fatalf("%s: event missing %s: %#v", marker, field, event)
			}
		}
	}
}

func assertContainsEvent(t *testing.T, marker string, events []map[string]any, eventType, field, value string) {
	t.Helper()
	for _, event := range events {
		if event["type"] == eventType && event[field] == value {
			return
		}
	}
	t.Fatalf("%s: missing event type=%q %s=%q in %#v", marker, eventType, field, value, events)
}

func asString(value any) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}

func firstLine(t *testing.T, path string) string {
	t.Helper()

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read run record %s: %v", path, err)
	}
	line, _, _ := strings.Cut(string(content), "\n")
	return line
}
