package tasksource

import (
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCandidatesParseTaskFiles(t *testing.T) {
	source := New(fixtureFS(map[string]string{
		"docs/tasks/completed/001-one.md":   taskFile("001", "agent-builder", "completed (verified L5)", "none"),
		"docs/tasks/backlog/010-ready.md":   taskFile("010", "exec-sandbox", "backlog", "001, 002"),
		"docs/tasks/completed/002-two.md":   taskFile("002", "agent-builder", "completed", "No blocking tasks"),
		"docs/tasks/active/011-active.md":   taskFile("011", "vault", "active", "010"),
		"docs/tasks/backlog/012-blocked.md": taskFile("012", "policy-engine", "⚠️ blocked", "010"),
		"docs/tasks/completed/013-done.md":  taskFile("013", "agent-builder", "done", "010"),
		"docs/tasks/backlog/014-human.md":   taskFile("014", "agent-builder", "needs-human", "010"),
	}), DefaultRoadmapPath, DefaultTaskDirs...)

	candidates, err := source.Candidates()
	if err != nil {
		t.Fatalf("Candidates() error = %v", err)
	}

	gotIDs := candidateIDs(candidates)
	wantIDs := []string{"001", "002", "010", "011", "012", "013", "014"}
	if !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("candidate IDs = %v, want %v", gotIDs, wantIDs)
	}
	if len(candidates[0].Dependencies) != 0 {
		t.Fatalf("Dependencies for none = %v, want empty", candidates[0].Dependencies)
	}
	if len(candidates[1].Dependencies) != 0 {
		t.Fatalf("Dependencies for No blocking tasks = %v, want empty", candidates[1].Dependencies)
	}

	got := candidates[2]
	if got.Task.ID != "010" {
		t.Fatalf("Task.ID = %q, want 010", got.Task.ID)
	}
	if got.Task.Repo != "exec-sandbox" {
		t.Fatalf("Task.Repo = %q, want exec-sandbox", got.Task.Repo)
	}
	if got.Task.Spec != "docs/tasks/backlog/010-ready.md" {
		t.Fatalf("Task.Spec = %q", got.Task.Spec)
	}
	if got.Status != StatusReady {
		t.Fatalf("Status = %q, want %q", got.Status, StatusReady)
	}
	if !reflect.DeepEqual(got.Dependencies, []string{"001", "002"}) {
		t.Fatalf("Dependencies = %v, want [001 002]", got.Dependencies)
	}
	blocked := candidates[4]
	if blocked.Status != StatusBlocked {
		t.Fatalf("blocked Status = %q, want %q", blocked.Status, StatusBlocked)
	}
	done := candidates[5]
	if done.Status != StatusCompleted {
		t.Fatalf("done Status = %q, want %q", done.Status, StatusCompleted)
	}
	human := candidates[6]
	if human.Status != StatusNeedsHuman {
		t.Fatalf("needs-human Status = %q, want %q", human.Status, StatusNeedsHuman)
	}
}

func TestNextReturnsDeterministicFirstReadyTask(t *testing.T) {
	source := New(fixtureFS(map[string]string{
		"docs/tasks/completed/001-one.md": taskFile("001", "agent-builder", "completed", "none"),
		"docs/tasks/backlog/010-ready.md": taskFile("010", "exec-sandbox", "backlog", "001"),
		"docs/tasks/backlog/011-ready.md": taskFile("011", "vault", "ready", "001"),
	}), DefaultRoadmapPath, DefaultTaskDirs...)

	var previous string
	for i := 0; i < 5; i++ {
		task, ok, err := source.Next()
		if err != nil {
			t.Fatalf("Next() run %d error = %v", i, err)
		}
		if !ok {
			t.Fatalf("Next() run %d ok = false, want true", i)
		}
		if task.ID != "010" {
			t.Fatalf("Next() run %d ID = %q, want 010", i, task.ID)
		}
		if i > 0 && task.Spec != previous {
			t.Fatalf("Next() run %d Spec = %q, prior %q", i, task.Spec, previous)
		}
		previous = task.Spec
	}
}

func TestNextSkipsBlockedAndNonReadyTasks(t *testing.T) {
	source := New(fixtureFS(map[string]string{
		"docs/tasks/completed/001-one.md":   taskFile("001", "agent-builder", "completed", "none"),
		"docs/tasks/backlog/010-blocked.md": taskFile("010", "exec-sandbox", "backlog", "011"),
		"docs/tasks/active/011-active.md":   taskFile("011", "exec-sandbox", "active", "001"),
		"docs/tasks/completed/012-done.md":  taskFile("012", "exec-sandbox", "completed", "001"),
		"docs/tasks/backlog/013-ready.md":   taskFile("013", "vault", "❌ ready", "001"),
	}), DefaultRoadmapPath, DefaultTaskDirs...)

	task, ok, err := source.Next()
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if !ok {
		t.Fatal("Next() ok = false, want true")
	}
	if task.ID != "013" {
		t.Fatalf("Next() ID = %q, want 013", task.ID)
	}
}

func TestNextReturnsEmptyWhenNoTaskIsReady(t *testing.T) {
	tests := map[string]fstest.MapFS{
		"all blocked": fixtureFS(map[string]string{
			"docs/tasks/backlog/010-blocked.md": taskFile("010", "exec-sandbox", "backlog", "011"),
			"docs/tasks/backlog/011-blocked.md": taskFile("011", "exec-sandbox", "active", "none"),
		}),
		"cycle": fixtureFS(map[string]string{
			"docs/tasks/backlog/010-cycle.md": taskFile("010", "exec-sandbox", "backlog", "011"),
			"docs/tasks/backlog/011-cycle.md": taskFile("011", "exec-sandbox", "backlog", "010"),
		}),
	}

	for name, fsys := range tests {
		t.Run(name, func(t *testing.T) {
			task, ok, err := New(fsys, DefaultRoadmapPath, DefaultTaskDirs...).Next()
			if err != nil {
				t.Fatalf("Next() error = %v", err)
			}
			if ok {
				t.Fatalf("Next() ok = true, task = %+v", task)
			}
		})
	}
}

func TestCandidatesRejectMalformedTaskMetadata(t *testing.T) {
	tests := map[string]struct {
		files map[string]string
		want  string
	}{
		"missing heading": {
			files: map[string]string{
				"docs/tasks/backlog/010-bad.md": strings.ReplaceAll(taskFile("010", "agent-builder", "backlog", "none"), "# Task 010: Example\n", ""),
			},
			want: "task heading",
		},
		"missing project": {
			files: map[string]string{
				"docs/tasks/backlog/010-bad.md": strings.ReplaceAll(taskFile("010", "agent-builder", "backlog", "none"), "**Project:** agent-builder\n", ""),
			},
			want: "project",
		},
		"missing status": {
			files: map[string]string{
				"docs/tasks/backlog/010-bad.md": strings.ReplaceAll(taskFile("010", "agent-builder", "backlog", "none"), "**Status:** backlog\n", ""),
			},
			want: "status",
		},
		"bad status": {
			files: map[string]string{
				"docs/tasks/backlog/010-bad.md": taskFile("010", "agent-builder", "mystery", "none"),
			},
			want: "unrecognized status",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := New(fixtureFS(tc.files), DefaultRoadmapPath, DefaultTaskDirs...).Candidates()
			if err == nil {
				t.Fatal("Candidates() error = nil")
			}
			if !strings.Contains(err.Error(), "docs/tasks/backlog/010-bad.md") {
				t.Fatalf("Candidates() error = %v, want bad file path", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("Candidates() error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestCandidatesRejectMissingDependencyReference(t *testing.T) {
	source := New(fixtureFS(map[string]string{
		"docs/tasks/backlog/010-missing-dep.md": taskFile("010", "agent-builder", "backlog", "999"),
	}), DefaultRoadmapPath, DefaultTaskDirs...)

	_, err := source.Candidates()
	if err == nil {
		t.Fatal("Candidates() error = nil")
	}
	if !strings.Contains(err.Error(), "999") || !strings.Contains(err.Error(), "010") {
		t.Fatalf("Candidates() error = %v, want task and dependency IDs", err)
	}
}

func TestSourceUsesReadOnlyFilesystem(t *testing.T) {
	base := fixtureFS(map[string]string{
		"docs/tasks/completed/001-one.md": taskFile("001", "agent-builder", "completed", "none"),
		"docs/tasks/backlog/010-ready.md": taskFile("010", "agent-builder", "backlog", "001"),
	})
	fsys := &readObservingFS{FS: base}
	source := New(fsys, DefaultRoadmapPath, DefaultTaskDirs...)

	if _, _, err := source.Next(); err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if fsys.opens == 0 {
		t.Fatal("expected read-side Open calls")
	}
	if fsys.writes != 0 {
		t.Fatalf("write attempts = %d, want 0", fsys.writes)
	}
}

func fixtureFS(tasks map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{
		DefaultRoadmapPath: &fstest.MapFile{Data: []byte("# Roadmap\n")},
	}
	for name, body := range tasks {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func taskFile(id, project, status, dependencies string) string {
	return "# Task " + id + ": Example\n" +
		"\n" +
		"**Project:** " + project + "\n" +
		"**Status:** " + status + "\n" +
		"\n" +
		"## Context\n" +
		"- Dependencies: " + dependencies + "\n"
}

func candidateIDs(candidates []Candidate) []string {
	ids := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		ids = append(ids, candidate.Task.ID)
	}
	return ids
}

type readObservingFS struct {
	fs.FS
	opens  int
	writes int
}

func (f *readObservingFS) Open(name string) (fs.File, error) {
	f.opens++
	return f.FS.Open(name)
}

func (f *readObservingFS) Create(name string) (fs.File, error) {
	f.writes++
	return nil, errors.New("write attempted: create " + name)
}

func (f *readObservingFS) OpenFile(name string, flag int, perm fs.FileMode) (fs.File, error) {
	f.writes++
	return nil, errors.New("write attempted: openfile " + name)
}
