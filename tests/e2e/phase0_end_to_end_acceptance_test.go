package e2e_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
)

func TestPhase0EndToEndAcceptance(t *testing.T) {
	binary := buildAgentBuilder(t)

	t.Run("TC-001_TC-002_TC-003_TC-006_success", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})

		stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code != 0 {
			t.Fatalf("TC-001 run exit code = %d, want 0; stdout=%q stderr=%q record=%q", code, stdout, stderr, readOptional(t, fixture.recordPath))
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("TC-001 stdout = %q, want completed task summary", stdout)
		}

		publishLog := readFile(t, fixture.publishLog)
		if !strings.Contains(publishLog, "git push origin task/034-branch-pr-publication") ||
			!strings.Contains(publishLog, "gh pr create --head task/034-branch-pr-publication --fill") {
			t.Fatalf("TC-002 publish log = %q, want branch push and PR create", publishLog)
		}

		events := readEvents(t, fixture.recordPath)
		assertEventContains(t, events, "run_started", "task_id", "001")
		assertEventContains(t, events, "run_started", "box_id", "sandbox-001")
		assertEventContains(t, events, "run_started", "worktree", fixture.worktree)
		assertEventContains(t, events, "command", "command", "RunInside task 001")
		assertEventContains(t, events, "command", "command", "pick task 001")
		assertEventContains(t, events, "command", "command", "attempt task 001")
		assertEventContains(t, events, "command", "command", "verify worktree "+fixture.worktree)
		assertEventContains(t, events, "stdout", "data", "task 001 selected")
		assertEventContains(t, events, "stdout", "data", "executor attempt completed: branch=task/034-branch-pr-publication")
		assertEventContains(t, events, "stdout", "data", "gate passed: PASS go build ./...")
		assertEventContains(t, events, "stdout", "data", "publication recorded: branch=task/034-branch-pr-publication pr=https://github.com/acme/e2e/pull/34")
		assertEventContains(t, events, "command", "command", "finish task 001 outcome=completed branch=task/034-branch-pr-publication pr=https://github.com/acme/e2e/pull/34")
		assertEventContains(t, events, "run_finished", "outcome", "completed")
		assertRunRecordFieldNonEmpty(t, events, "run_started", "run_id")
		assertNoSecret(t, "TC-003 stdout", stdout, fixture.gitToken, fixture.ghToken)
		assertNoSecret(t, "TC-003 stderr", stderr, fixture.gitToken, fixture.ghToken)
		assertNoSecret(t, "TC-003 run record", readFile(t, fixture.recordPath), fixture.gitToken, fixture.ghToken)
		t.Log("TC-001 Phase 0 accepted: task selected, branch produced, PR recorded, gate passed, run record persisted")
	})

	t.Run("TC-001_idle_does_not_start_executor", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		writeFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"), `# Task 001: first

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** blocked

## Goal
Fixture task.
`)

		stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code != 0 {
			t.Fatalf("TC-001 idle exit code = %d, want 0; stdout=%q stderr=%q", code, stdout, stderr)
		}
		if !strings.Contains(stdout, "run idle: no ready task") {
			t.Fatalf("TC-001 idle stdout = %q, want idle summary", stdout)
		}
		assertNoPublishLog(t, fixture.publishLog)
		if _, err := os.Stat(fixture.recordPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("TC-001 idle run record stat err = %v, want no supervisor run record", err)
		}
	})

	t.Run("TC-004_negative_paths_do_not_mark_done", func(t *testing.T) {
		tests := []struct {
			name       string
			config     publicationFixtureConfig
			customize  func(*publicationFixture)
			wantRecord string
		}{
			{
				name:       "executor failure",
				config:     publicationFixtureConfig{claudeExit: 17},
				wantRecord: `"outcome":"failed"`,
			},
			{
				name:       "gate failure",
				config:     publicationFixtureConfig{gateFails: true},
				wantRecord: "gate failed",
			},
			{
				name: "timeout",
				customize: func(f *publicationFixture) {
					f.claudePath = writeSlowClaude032(t, f.shimDir)
				},
				wantRecord: `"outcome":"timed-out"`,
			},
			{
				name: "blocked ingestion",
				customize: func(f *publicationFixture) {
					f.claudePath = writeBlockedIngestionClaude032(t, f.shimDir)
				},
				wantRecord: "Claude web/tool capability disabled",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				fixture := newPublicationFixture(t, tc.config)
				if tc.customize != nil {
					tc.customize(&fixture)
				}
				env := fixture.env()
				if tc.name == "timeout" {
					env[runtimewiring.EnvRunTimeout] = "100ms"
				}

				stdout, stderr, code := runAgentBuilder(t, binary, env, "run")
				if code == 0 {
					t.Fatalf("TC-004 %s exit code = 0, want non-success; stdout=%q stderr=%q record=%q", tc.name, stdout, stderr, readOptional(t, fixture.recordPath))
				}
				taskFile := readFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"))
				if strings.Contains(taskFile, "**Status:** done") {
					t.Fatalf("TC-004 %s marked task done:\n%s", tc.name, taskFile)
				}
				record := readFile(t, fixture.recordPath)
				if !strings.Contains(record, tc.wantRecord) {
					t.Fatalf("TC-004 %s record = %q, want %q", tc.name, record, tc.wantRecord)
				}
			})
		}
	})

	t.Run("TC-005_documentation_labels_evidence_honestly", func(t *testing.T) {
		root := filepath.Join("..", "..")
		tracker := readFile(t, filepath.Join(root, "docs/tasks/test-specs/coverage-tracker.md"))
		roadmap := readFile(t, filepath.Join(root, "docs/plans/roadmap.md"))
		spec := readFile(t, filepath.Join(root, "docs/spec/SPEC.md"))

		// After the Phase 1 Podman swap (ADR 021, task 036), srt is no longer
		// the run-path runtime — it is removed/historical, not pending L6
		// evidence. The honest doc state names Podman as the containment
		// backend and labels acceptance at fake-provider L5. The phrasing must
		// not regress to implying srt is still a pending runtime blocker.
		for label, text := range map[string]string{
			"coverage tracker": tracker,
			"roadmap":          roadmap,
			"SPEC":             spec,
		} {
			if !strings.Contains(text, "fake-provider L5") {
				t.Fatalf("TC-005 %s does not label fake-provider L5 evidence", label)
			}
			if !strings.Contains(text, "Podman") {
				t.Fatalf("TC-005 %s does not name Podman as the containment backend", label)
			}
		}
		// The roadmap and SPEC must frame srt as removed (Phase 1 reality),
		// not as a pending runtime the run path still depends on.
		for label, text := range map[string]string{
			"roadmap": roadmap,
			"SPEC":    spec,
		} {
			if !strings.Contains(text, "srt") {
				continue
			}
			lowered := strings.ToLower(text)
			if !strings.Contains(lowered, "removed") && !strings.Contains(lowered, "historical") {
				t.Fatalf("TC-005 %s mentions srt without framing it as removed/historical for the Phase 1 run path", label)
			}
		}
	})
}

func writeSlowClaude032(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude-slow-032")
	script := `#!/bin/sh
set -eu
sleep 1
prompt=$2
branch_file=""
next_branch=0
while IFS= read -r line; do
	case "$line" in
		"When finished, write only the produced branch name to this file:") next_branch=1 ;;
		*) if [ "$next_branch" = "1" ]; then branch_file=$line; next_branch=0; fi ;;
	esac
done <<EOF
$prompt
EOF
printf 'task/032-timeout-after-slow-executor\n' > "$branch_file"
`
	writeFile(t, path, script)
	chmodExecutable(t, path)
	return path
}

func writeBlockedIngestionClaude032(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "claude-blocked-ingestion-032")
	writeFile(t, path, "#!/bin/sh\nset -eu\necho 'executor: Claude web/tool capability disabled' >&2\nexit 39\n")
	chmodExecutable(t, path)
	return path
}

func assertRunRecordFieldNonEmpty(t *testing.T, events []map[string]any, eventType, field string) {
	t.Helper()
	for _, event := range events {
		if event["type"] != eventType {
			continue
		}
		if strings.TrimSpace(fmt.Sprint(event[field])) != "" {
			return
		}
	}
	t.Fatalf("run record missing non-empty %s field on %s event in %#v", field, eventType, events)
}
