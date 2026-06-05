package tasksource_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

func TestStatusWriterUpdatesAllowedStatuses(t *testing.T) {
	for _, target := range []tasksource.WritableStatus{
		tasksource.WritableStatusDone,
		tasksource.WritableStatusBlocked,
		tasksource.WritableStatusNeedsHuman,
	} {
		t.Run(string(target), func(t *testing.T) {
			root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "backlog"))

			result, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", target)
			if err != nil {
				t.Fatalf("TC-001 WriteStatus() error = %v", err)
			}
			if result.Path != taskPath {
				t.Fatalf("TC-001 result.Path = %q, want %q", result.Path, taskPath)
			}
			if !result.Changed {
				t.Fatal("TC-001 result.Changed = false, want true")
			}

			got := readFile(t, taskPath)
			wantLine := "**Status:** " + string(target)
			if statusLine(got) != wantLine {
				t.Fatalf("TC-001 status line = %q, want %q", statusLine(got), wantLine)
			}
		})
	}
}

func TestStatusWriterOutputIsReadableByTaskSource(t *testing.T) {
	body := strings.ReplaceAll(taskBody("011", "backlog"), "- Dependencies: 010\n", "- Dependencies: none\n")
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", body)
	writeRoadmapFixture(t, root)

	if _, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusDone); err != nil {
		t.Fatalf("TC-001 WriteStatus() error = %v", err)
	}

	candidates, err := tasksource.New(os.DirFS(root), tasksource.DefaultRoadmapPath, tasksource.DefaultTaskDirs...).Candidates()
	if err != nil {
		t.Fatalf("TC-001 Source.Candidates() after status write error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("TC-001 candidates = %d, want 1", len(candidates))
	}
	if candidates[0].Task.Spec != filepath.ToSlash(strings.TrimPrefix(taskPath, root+string(os.PathSeparator))) {
		t.Fatalf("TC-001 Task.Spec = %q, want written task path", candidates[0].Task.Spec)
	}
	if candidates[0].Status != tasksource.StatusCompleted {
		t.Fatalf("TC-001 Status after written done marker = %q, want %q", candidates[0].Status, tasksource.StatusCompleted)
	}
}

func TestStatusWriterRefusesMissingTaskID(t *testing.T) {
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "backlog"))
	before := readFile(t, taskPath)

	_, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("999", tasksource.WritableStatusDone)
	if err == nil {
		t.Fatal("TC-001A WriteStatus() error = nil, want missing task error")
	}
	if !strings.Contains(err.Error(), "999") {
		t.Fatalf("TC-001A WriteStatus() error = %v, want task ID 999", err)
	}
	assertFileBytes(t, "TC-001A", taskPath, before)
}

func TestStatusWriterIsIdempotentWhenStatusAlreadyMatches(t *testing.T) {
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "blocked"))
	before := readFile(t, taskPath)

	result, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusBlocked)
	if err != nil {
		t.Fatalf("TC-001B WriteStatus() error = %v", err)
	}
	if result.Path != taskPath {
		t.Fatalf("TC-001B result.Path = %q, want %q", result.Path, taskPath)
	}
	if result.Changed {
		t.Fatal("TC-001B result.Changed = true, want false")
	}
	assertFileBytes(t, "TC-001B", taskPath, before)
}

func TestStatusWriterOnlyChangesStatusLine(t *testing.T) {
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "backlog"))
	before := readFile(t, taskPath)

	if _, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusNeedsHuman); err != nil {
		t.Fatalf("TC-002 WriteStatus() error = %v", err)
	}
	after := readFile(t, taskPath)

	assertOnlyStatusLineChanged(t, "TC-002", before, after)
}

func TestStatusWriterPreservesTrailingWhitespaceAndFinalNewline(t *testing.T) {
	tests := map[string]struct {
		body             string
		wantFinalNewline bool
	}{
		"with final newline": {
			body: "# Task 011: Example\n" +
				"\n" +
				"**Project:** agent-builder   \n" +
				"**Status:** backlog\n" +
				"\n" +
				"## Context  \n" +
				"- Dependencies: 010\t\n",
			wantFinalNewline: true,
		},
		"without final newline": {
			body: "# Task 011: Example\n" +
				"\n" +
				"**Project:** agent-builder   \n" +
				"**Status:** backlog\n" +
				"\n" +
				"## Context  \n" +
				"- Dependencies: 010\t",
			wantFinalNewline: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", tc.body)
			before := readFile(t, taskPath)

			if _, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusDone); err != nil {
				t.Fatalf("TC-002A WriteStatus() error = %v", err)
			}
			after := readFile(t, taskPath)

			assertOnlyStatusLineChanged(t, "TC-002A", before, after)
			if bytes.HasSuffix(after, []byte("\n")) != tc.wantFinalNewline {
				t.Fatalf("TC-002A final newline preservation = %v, want %v", bytes.HasSuffix(after, []byte("\n")), tc.wantFinalNewline)
			}
		})
	}
}

func TestStatusWriterRefusesDuplicateStatusMarkers(t *testing.T) {
	body := taskBody("011", "backlog") + "**Status:** blocked\n"
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", body)
	before := readFile(t, taskPath)

	_, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusDone)
	if err == nil {
		t.Fatal("TC-002B WriteStatus() error = nil, want duplicate status error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "multiple status") {
		t.Fatalf("TC-002B WriteStatus() error = %v, want multiple status", err)
	}
	assertFileBytes(t, "TC-002B", taskPath, before)
}

func TestStatusWriterRefusesInvalidStatusValues(t *testing.T) {
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "backlog"))
	before := readFile(t, taskPath)

	_, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatus("priority: high"))
	if err == nil {
		t.Fatal("TC-003 WriteStatus() error = nil, want invalid status error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid writable status") {
		t.Fatalf("TC-003 WriteStatus() error = %v, want invalid writable status", err)
	}
	assertFileBytes(t, "TC-003", taskPath, before)
}

func TestStatusWriterAPIAcceptsOnlyTaskIDAndStatus(t *testing.T) {
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", taskBody("011", "backlog"))
	before := readFile(t, taskPath)
	writer := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...)

	result, err := writer.WriteStatus("011", tasksource.WritableStatusBlocked)
	if err != nil {
		t.Fatalf("TC-003A WriteStatus(taskID, status) error = %v", err)
	}
	if result.Path == "" {
		t.Fatal("TC-003A result.Path is empty")
	}
	after := readFile(t, taskPath)
	assertOnlyStatusLineChanged(t, "TC-003A", before, after)
}

func TestStatusWriterRefusesMissingStatusLine(t *testing.T) {
	body := strings.ReplaceAll(taskBody("011", "backlog"), "**Status:** backlog\n", "")
	root, taskPath := writeTaskFixture(t, "backlog", "011-task-status-writer.md", body)
	before := readFile(t, taskPath)

	_, err := tasksource.NewStatusWriter(root, tasksource.DefaultTaskDirs...).WriteStatus("011", tasksource.WritableStatusDone)
	if err == nil {
		t.Fatal("TC-003B WriteStatus() error = nil, want missing status error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "missing status") {
		t.Fatalf("TC-003B WriteStatus() error = %v, want missing status", err)
	}
	assertFileBytes(t, "TC-003B", taskPath, before)
}

func writeTaskFixture(t *testing.T, stateDir, filename, body string) (string, string) {
	t.Helper()

	root := t.TempDir()
	for _, dir := range tasksource.DefaultTaskDirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", dir, err)
		}
	}

	taskPath := filepath.Join(root, "docs", "tasks", stateDir, filename)
	if err := os.WriteFile(taskPath, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", taskPath, err)
	}
	return root, taskPath
}

func writeRoadmapFixture(t *testing.T, root string) {
	t.Helper()

	roadmapPath := filepath.Join(root, filepath.FromSlash(tasksource.DefaultRoadmapPath))
	if err := os.MkdirAll(filepath.Dir(roadmapPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(roadmap dir) error = %v", err)
	}
	if err := os.WriteFile(roadmapPath, []byte("# Roadmap\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", roadmapPath, err)
	}
}

func taskBody(id, status string) string {
	return "# Task " + id + ": Example\n" +
		"\n" +
		"**Project:** agent-builder\n" +
		"**Status:** " + status + "\n" +
		"\n" +
		"## Goal\n" +
		"Keep the prose stable.\n" +
		"\n" +
		"## Requirements\n" +
		"| Req ID | Description | Priority |\n" +
		"|--------|-------------|----------|\n" +
		"| REQ-001 | Do the focused thing | must have |\n" +
		"\n" +
		"## Context\n" +
		"- Dependencies: 010\n"
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	return body
}

func statusLine(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, "**Status:**") {
			return line
		}
	}
	return ""
}

func assertFileBytes(t *testing.T, marker string, path string, want []byte) {
	t.Helper()

	got := readFile(t, path)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s file bytes changed:\n got: %q\nwant: %q", marker, got, want)
	}
}

func assertOnlyStatusLineChanged(t *testing.T, marker string, before, after []byte) {
	t.Helper()

	beforeLines := strings.SplitAfter(string(before), "\n")
	afterLines := strings.SplitAfter(string(after), "\n")
	if len(beforeLines) != len(afterLines) {
		t.Fatalf("%s line count changed from %d to %d", marker, len(beforeLines), len(afterLines))
	}

	changed := 0
	for i := range beforeLines {
		beforeIsStatus := strings.HasPrefix(strings.TrimSuffix(beforeLines[i], "\n"), "**Status:**")
		afterIsStatus := strings.HasPrefix(strings.TrimSuffix(afterLines[i], "\n"), "**Status:**")
		if beforeLines[i] == afterLines[i] {
			continue
		}
		if !beforeIsStatus || !afterIsStatus {
			t.Fatalf("%s non-status line %d changed:\n got: %q\nwant: %q", marker, i+1, afterLines[i], beforeLines[i])
		}
		changed++
	}
	if changed != 1 {
		t.Fatalf("%s changed status lines = %d, want 1", marker, changed)
	}
}
