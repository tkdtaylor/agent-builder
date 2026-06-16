package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/sandbox"
	"github.com/tkdtaylor/agent-builder/internal/sandbox/podman"
)

// srtTokens are the strings whose absence proves the Phase 1 swap removed the
// rented @anthropic-ai/sandbox-runtime backend from the live run pipeline.
var srtTokens = []string{"srt", "sandbox-runtime", "AGENT_BUILDER_SANDBOX_RUNTIME"}

// TestPhase1EndToEndAcceptance drives the real agent-builder binary end to end
// with a fake Claude executor, a fake Podman execution-box launcher (the seam
// task 036 added via AGENT_BUILDER_EXEC_BOX_LAUNCHER), a fixture worktree that
// passes the Gate, and fake git/gh for publication. No real Podman, no srt.
//
// TC-037-01, TC-037-02, TC-037-04.
func TestPhase1EndToEndAcceptance(t *testing.T) {
	binary := buildAgentBuilder(t)

	t.Run("TC-037-01_TC-037-02_TC-037-04_podman_containment_no_srt", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})

		stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
		record := readFile(t, fixture.recordPath)
		if code != 0 {
			t.Fatalf("TC-037-04 run exit code = %d, want 0; stdout=%q stderr=%q record=%q", code, stdout, stderr, record)
		}
		if !strings.Contains(stdout, "run completed: task 001") {
			t.Fatalf("TC-037-04 stdout = %q, want completed run summary", stdout)
		}

		// TC-037-01: the full pipeline emitted the expected lifecycle events.
		events := readEvents(t, fixture.recordPath)
		assertEventContains(t, events, "command", "command", "pick task 001")
		assertEventContains(t, events, "command", "command", "attempt task 001")
		assertEventContains(t, events, "command", "command", "verify worktree "+fixture.worktree)
		assertEventContains(t, events, "stdout", "data", "publication recorded: branch=")
		assertEventContains(t, events, "run_finished", "outcome", "completed")

		// TC-037-02: the run record carries Podman containment evidence naming
		// the launcher, and nowhere references srt / sandbox-runtime.
		assertEventContains(t, events, "command", "command", "containment=podman")
		assertEventContains(t, events, "command", "command", "launcher="+fixture.launcherPath)

		// TC-037-02: ZERO srt occurrences in stdout, stderr, or the run record.
		for _, token := range srtTokens {
			assertNoToken(t, "TC-037-02 stdout", stdout, token)
			assertNoToken(t, "TC-037-02 stderr", stderr, token)
			assertNoToken(t, "TC-037-02 run record", record, token)
		}

		t.Log("TC-037-01 Phase 1 accepted: task selected, Podman containment used, no srt invocation, run record clean")
	})

	t.Run("TC-037-01_failed_containment_probe_fails_run", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		// Point the launcher at a fake that fails the containment probe.
		fixture.launcherPath = writeFailingLauncher(t, fixture.shimDir)

		stdout, stderr, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code == 0 {
			t.Fatalf("TC-037-01 failed-probe exit code = 0, want non-success; stdout=%q stderr=%q", stdout, stderr)
		}
		if strings.Contains(stdout, "run completed") {
			t.Fatalf("TC-037-01 failed-probe stdout = %q, should not mark run completed", stdout)
		}
		// The error names the containment failure (the probe exit surfaced by
		// the sandbox box Create path). The probe fails before the run record
		// opens, so the failure is observable on the CLI rather than the record.
		if !strings.Contains(stderr, "create box") && !strings.Contains(stderr, "create probe exited") {
			t.Fatalf("TC-037-01 failed-probe stderr = %q, want containment failure named", stderr)
		}
		// Even on the failure path, no srt token leaks.
		for _, token := range srtTokens {
			assertNoToken(t, "TC-037-01 failed-probe stderr", stderr, token)
		}
		t.Log("TC-037-01 failed containment probe surfaced as run failure naming the containment box")
	})

	t.Run("TC-037-04_idle_no_ready_task_is_not_a_phase1_failure", func(t *testing.T) {
		fixture := newPublicationFixture(t, publicationFixtureConfig{})
		writeFile(t, filepath.Join(fixture.taskRoot, "docs/tasks/backlog/001-first.md"), `# Task 001: first

**Project:** agent-builder
**Created:** 2026-06-05
**Status:** blocked

## Goal
Fixture task.
`)
		stdout, _, code := runAgentBuilder(t, binary, fixture.env(), "run")
		if code != 0 {
			t.Fatalf("TC-037-04 idle exit code = %d, want 0", code)
		}
		if !strings.Contains(stdout, "run idle: no ready task") {
			t.Fatalf("TC-037-04 idle stdout = %q, want idle summary", stdout)
		}
	})
}

// TestPhase1LivePodman is the optional L6 live harness. Gated by
// AGENT_BUILDER_LIVE_PODMAN=1. It runs `echo phase1-ok` through the real
// podman.Runner with the agent workload (runsc). Skipped when Podman/runsc are
// unavailable (environment availability gap); FAILS when the Gate-toolchain
// directory is missing (a configuration error, not an availability gap).
//
// TC-037-03.
func TestPhase1LivePodman(t *testing.T) {
	if os.Getenv("AGENT_BUILDER_LIVE_PODMAN") != "1" {
		t.Skip("live Podman harness skipped; set AGENT_BUILDER_LIVE_PODMAN=1 to run")
	}

	root := projectRoot(t)
	launcher := filepath.Join(root, "containment/execution-box/run.sh")
	if _, err := os.Stat(launcher); err != nil {
		t.Skipf("TC-037-03 launcher unavailable at %s: %v", launcher, err)
	}

	// Podman absence is an environment availability gap → skip. The launcher
	// checks the Gate-toolchain directory before it probes Podman, so we probe
	// Podman here first to keep the skip/fail distinction honest.
	if _, err := exec.LookPath("podman"); err != nil {
		t.Skipf("TC-037-03 Podman unavailable on PATH: %v", err)
	}

	// Missing Gate-toolchain directory (read by the launcher) is a
	// configuration error, not an availability gap — FAIL, do not skip.
	gateTools := filepath.Join(root, "containment/execution-box/gate-tools")
	if info, err := os.Stat(gateTools); err != nil || !info.IsDir() {
		t.Fatalf("TC-037-03 Gate-toolchain directory missing at %s (configuration error, not env gap): %v", gateTools, err)
	}

	runner := podman.NewWithLauncher(launcher)
	result, exitCode, err := runner.Run(sandbox.Request{
		Command:  []string{"echo", "phase1-ok"},
		Worktree: root,
		Limits: sandbox.Limits{
			WallClockTimeout: 60 * time.Second,
		},
	})
	if err != nil {
		// runsc / runtime unavailable to Podman manifests as an invoke error;
		// treat as an environment availability gap → skip.
		if strings.Contains(err.Error(), "podman") || strings.Contains(err.Error(), "executable file not found") {
			t.Skipf("TC-037-03 Podman/runsc unavailable: %v", err)
		}
		t.Fatalf("TC-037-03 live run error: %v", err)
	}
	if exitCode != 0 {
		// The launcher dies with a clear message when Podman or the runsc
		// runtime is unavailable to it; treat those as availability gaps.
		combined := result.Stdout + result.Stderr
		if strings.Contains(combined, "podman unavailable") ||
			strings.Contains(combined, "rootless Podman is unavailable") ||
			strings.Contains(combined, "OCI runtime unavailable") {
			t.Skipf("TC-037-03 Podman/runsc unavailable to launcher: %s", strings.TrimSpace(combined))
		}
		t.Fatalf("TC-037-03 live exit code = %d, want 0; stdout=%q stderr=%q", exitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "phase1-ok") {
		t.Fatalf("TC-037-03 live stdout = %q, want phase1-ok", result.Stdout)
	}
	t.Log("TC-037-03 live Podman containment ran echo phase1-ok inside the execution-box (workload=agent runtime=runsc)")
}

// writeFailingLauncher writes a fake launcher that exits non-zero, modelling a
// failed containment probe.
func writeFailingLauncher(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "run-failing.sh")
	writeFile(t, path, "#!/bin/sh\necho 'execution-box: containment probe failed' >&2\nexit 7\n")
	chmodExecutable(t, path)
	return path
}

// assertNoToken fails if text contains the given substring.
func assertNoToken(t *testing.T, label, text, token string) {
	t.Helper()
	if strings.Contains(text, token) {
		t.Fatalf("%s leaked forbidden token %q in %q", label, token, text)
	}
}

// projectRoot walks up from the test working directory to the module root,
// identified by go.mod. (The tests/ tree also contains a containment/ package
// dir, so anchoring on go.mod avoids resolving to the wrong root.)
func projectRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
			return cwd
		}
		parent := filepath.Dir(cwd)
		if parent == cwd {
			t.Fatalf("could not find module root (go.mod) from %s", cwd)
		}
		cwd = parent
	}
}
