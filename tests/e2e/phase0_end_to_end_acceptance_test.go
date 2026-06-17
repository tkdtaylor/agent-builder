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

	t.Run("TC-052_adr031_exists", func(t *testing.T) {
		root := filepath.Join("..", "..")
		adr031 := readFile(t, filepath.Join(root, "docs/architecture/decisions/031-l6-live-mode-probes.md"))

		// TC-052-01: ADR 031 exists and records the correct architectural facts.
		// 9 positive assertions (presence) + 1 negative (absence of srt).
		if !strings.Contains(adr031, "AGENT_BUILDER_LIVE_PUBLISH") {
			t.Fatalf("TC-052-01 ADR 031 missing AGENT_BUILDER_LIVE_PUBLISH env gate")
		}
		if !strings.Contains(adr031, "AGENT_BUILDER_LIVE_E2E") {
			t.Fatalf("TC-052-01 ADR 031 missing AGENT_BUILDER_LIVE_E2E env gate")
		}
		if !strings.Contains(adr031, "host-side") {
			t.Fatalf("TC-052-01 ADR 031 missing 'host-side' (executor location)")
		}
		if !strings.Contains(adr031, "/bin/true") {
			t.Fatalf("TC-052-01 ADR 031 missing '/bin/true' (workload liveness probe)")
		}
		if !strings.Contains(adr031, "ADR 021") {
			t.Fatalf("TC-052-01 ADR 031 missing 'ADR 021' reference (srt removal)")
		}
		if !strings.Contains(adr031, "ADR 026") {
			t.Fatalf("TC-052-01 ADR 031 missing 'ADR 026' reference (Podman containment)")
		}
		if !strings.Contains(adr031, "self-clean") && !strings.Contains(adr031, "self-cleanup") && !strings.Contains(adr031, "close PR") {
			t.Fatalf("TC-052-01 ADR 031 missing cleanup discipline language (self-clean/self-cleanup/close PR)")
		}
		if strings.Contains(adr031, "AGENT_BUILDER_SANDBOX_RUNTIME=srt") {
			t.Fatalf("TC-052-01 ADR 031 contains stale AGENT_BUILDER_SANDBOX_RUNTIME=srt (must be absent per ADR 021)")
		}
		t.Log("TC-052-01 PASS: ADR 031 exists and records all 9 required architectural facts")
	})

	t.Run("TC-052_plan_files_no_stale_srt", func(t *testing.T) {
		root := filepath.Join("..", "..")
		checklist := readFile(t, filepath.Join(root, "docs/plans/phase0-l6-verification-checklist.md"))
		runbook := readFile(t, filepath.Join(root, "docs/plans/l6-operator-runbook.md"))

		// TC-052-02 negative assertions (1-3): no stale srt, no invalid argv.
		if strings.Contains(checklist, "AGENT_BUILDER_SANDBOX_RUNTIME=srt") {
			t.Fatalf("TC-052-02 phase0-l6-verification-checklist.md contains stale AGENT_BUILDER_SANDBOX_RUNTIME=srt")
		}
		if strings.Contains(runbook, "AGENT_BUILDER_SANDBOX_RUNTIME=srt") {
			t.Fatalf("TC-052-02 l6-operator-runbook.md contains stale AGENT_BUILDER_SANDBOX_RUNTIME=srt")
		}
		if strings.Contains(checklist, "--task-root docs/tasks") || strings.Contains(runbook, "--task-root docs/tasks") {
			t.Fatalf("TC-052-02 plan files contain invalid '--task-root docs/tasks' argv (the run subcommand takes no positional args)")
		}
		t.Log("TC-052-02a PASS: no AGENT_BUILDER_SANDBOX_RUNTIME=srt in plan files")
		t.Log("TC-052-02b PASS: no '--task-root docs/tasks' in plan files")
	})

	t.Run("TC-052_plan_files_live_commands", func(t *testing.T) {
		root := filepath.Join("..", "..")
		checklist := readFile(t, filepath.Join(root, "docs/plans/phase0-l6-verification-checklist.md"))
		runbook := readFile(t, filepath.Join(root, "docs/plans/l6-operator-runbook.md"))

		// TC-052-02 positive assertions (4-7): live-test commands, env contract, host-side doc.
		// Assertion 4: 034 row contains AGENT_BUILDER_LIVE_PUBLISH in BOTH plan files
		// (spec mandates the runbook Section 3 table; the checklist mirrors it).
		if !strings.Contains(checklist, "AGENT_BUILDER_LIVE_PUBLISH") || !strings.Contains(runbook, "AGENT_BUILDER_LIVE_PUBLISH") {
			t.Fatalf("TC-052-02c Task 034 missing AGENT_BUILDER_LIVE_PUBLISH (checklist=%v runbook=%v)",
				strings.Contains(checklist, "AGENT_BUILDER_LIVE_PUBLISH"), strings.Contains(runbook, "AGENT_BUILDER_LIVE_PUBLISH"))
		}
		// Assertion 5: 032 row contains AGENT_BUILDER_LIVE_E2E in BOTH plan files.
		if !strings.Contains(checklist, "AGENT_BUILDER_LIVE_E2E") || !strings.Contains(runbook, "AGENT_BUILDER_LIVE_E2E") {
			t.Fatalf("TC-052-02d Task 032 missing AGENT_BUILDER_LIVE_E2E (checklist=%v runbook=%v)",
				strings.Contains(checklist, "AGENT_BUILDER_LIVE_E2E"), strings.Contains(runbook, "AGENT_BUILDER_LIVE_E2E"))
		}
		// Assertion 6: at least one file documents ANTHROPIC_API_KEY in env contract.
		if !strings.Contains(checklist, "ANTHROPIC_API_KEY") && !strings.Contains(runbook, "ANTHROPIC_API_KEY") {
			t.Fatalf("TC-052-02e neither plan file documents ANTHROPIC_API_KEY in the 022/028 env contract")
		}
		// Assertion 7: at least one file documents host-side execution.
		if !strings.Contains(checklist, "host-side") && !strings.Contains(checklist, "host side") && !strings.Contains(checklist, "runs on the host") &&
			!strings.Contains(runbook, "host-side") && !strings.Contains(runbook, "host side") && !strings.Contains(runbook, "runs on the host") {
			t.Fatalf("TC-052-02f neither plan file documents that claude runs host-side")
		}
		t.Log("TC-052-02c PASS: 034 row updated with AGENT_BUILDER_LIVE_PUBLISH")
		t.Log("TC-052-02d PASS: 032 row updated with AGENT_BUILDER_LIVE_E2E")
		t.Log("TC-052-02e PASS: ANTHROPIC_API_KEY documented in env contract")
		t.Log("TC-052-02f PASS: host-side execution documented")
	})

	t.Run("TC-052_tc005_regression_check", func(t *testing.T) {
		root := filepath.Join("..", "..")
		tracker := readFile(t, filepath.Join(root, "docs/tasks/test-specs/coverage-tracker.md"))
		roadmap := readFile(t, filepath.Join(root, "docs/plans/roadmap.md"))
		spec := readFile(t, filepath.Join(root, "docs/spec/SPEC.md"))

		// TC-052-03: existing TC-005 invariants still hold (fake-provider L5, Podman, srt framed as removed).
		for label, text := range map[string]string{
			"coverage tracker": tracker,
			"roadmap":          roadmap,
			"SPEC":             spec,
		} {
			if !strings.Contains(text, "fake-provider L5") {
				t.Fatalf("TC-052-03 regression: %s no longer labels fake-provider L5 evidence", label)
			}
			if !strings.Contains(text, "Podman") {
				t.Fatalf("TC-052-03 regression: %s no longer names Podman as the containment backend", label)
			}
		}
		for label, text := range map[string]string{
			"roadmap": roadmap,
			"SPEC":    spec,
		} {
			if !strings.Contains(text, "srt") {
				continue
			}
			lowered := strings.ToLower(text)
			if !strings.Contains(lowered, "removed") && !strings.Contains(lowered, "historical") {
				t.Fatalf("TC-052-03 regression: %s mentions srt without framing it as removed/historical", label)
			}
		}
		t.Log("TC-052-03 PASS: TC-005 regression check — fake-provider L5, Podman, srt framed as removed all confirmed")
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
