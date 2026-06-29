package supervisor_test

import (
	"context"
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
	).Run(context.Background())
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
	box := &recordBox{
		handle: supervisor.BoxHandle{ID: "box-019", Worktree: "/work/agent-builder"},
		onTeardown: func() error {
			content, err := os.ReadFile(recordPath)
			if err != nil {
				return err
			}
			if !strings.Contains(string(content), `"type":"run_finished"`) {
				return errors.New("run_finished was not flushed before teardown")
			}
			return nil
		},
	}
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
			if _, err := io.WriteString(streams.Stdout, "stdout-two\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Stderr, "stderr-two\n"); err != nil {
				return err
			}
			if _, err := io.WriteString(streams.Command, "make fitness\n"); err != nil {
				return err
			}
			content, err := os.ReadFile(recordPath)
			if err != nil {
				return err
			}
			return requireContentDuringRun(string(content), "stdout-one", "stderr-one", "go test ./...", "stdout-two", "stderr-two", "make fitness")
		},
	}

	err := supervisor.New(
		supervisor.WithTask(supervisor.Task{ID: "019", Repo: "agent-builder", Spec: "docs/tasks/completed/019-run-log-collection.md"}),
		supervisor.WithContainmentBox(box),
		supervisor.WithInBoxLoop(loop),
		supervisor.WithRunRecordPath(recordPath),
	).Run(context.Background())
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
	assertEventSequence(t, "TC-002-Stream-Capture", events, "stdout", "data", []string{"stdout-one\n", "stdout-two\n"})
	assertEventSequence(t, "TC-002-Stream-Capture", events, "stderr", "data", []string{"stderr-one\n", "stderr-two\n"})
	assertEventSequence(t, "TC-002-Stream-Capture", events, "command", "command", []string{"RunInside task 019", "go test ./...\n", "make fitness\n"})
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
	).Run(context.Background())
	if !errors.Is(err, loopErr) {
		t.Fatalf("TC-004-No-Post-Teardown-Readback: Run() error = %v, want %v", err, loopErr)
	}
	if !box.tornDown {
		t.Fatal("TC-004-No-Post-Teardown-Readback: fake box was not torn down")
	}
	if box.readbackCalls != 0 {
		t.Fatalf("TC-004-No-Post-Teardown-Readback: post-teardown readback calls = %d, want 0", box.readbackCalls)
	}

	events := readRunRecord(t, recordPath)
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "stdout", "data", "partial stdout\n")
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "stderr", "data", "partial stderr\n")
	assertContainsEvent(t, "TC-004-No-Post-Teardown-Readback", events, "command", "command", "make check\n")
	assertContainsEvent(t, "TC-005-RunRecord-Outcome-Values", events, "run_finished", "outcome", "failed")
}

type recordBox struct {
	handle        supervisor.BoxHandle
	tornDown      bool
	onTeardown    func() error
	readbackCalls int
}

func (b *recordBox) Create(supervisor.Task) (supervisor.BoxHandle, error) {
	return b.handle, nil
}

func (b *recordBox) Kill(supervisor.BoxHandle) error {
	return nil
}

func (b *recordBox) Teardown(supervisor.BoxHandle) error {
	if b.onTeardown != nil {
		if err := b.onTeardown(); err != nil {
			return err
		}
	}
	b.tornDown = true
	return nil
}

func (b *recordBox) ReadBackAfterTeardown() {
	b.readbackCalls++
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

func assertEventSequence(t *testing.T, marker string, events []map[string]any, eventType, field string, want []string) {
	t.Helper()

	got := []string{}
	for _, event := range events {
		if event["type"] == eventType {
			got = append(got, asString(event[field]))
		}
	}
	if len(got) != len(want) {
		t.Fatalf("%s: %s sequence length = %d (%v), want %d (%v)", marker, eventType, len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: %s sequence[%d] = %q, want %q; full sequence %v", marker, eventType, i, got[i], want[i], got)
		}
	}
}

func requireContentDuringRun(content string, values ...string) error {
	for _, value := range values {
		if !strings.Contains(content, value) {
			return errors.New(value + " was not visible during RunInside")
		}
	}
	return nil
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
