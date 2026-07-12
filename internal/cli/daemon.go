package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// EnvDaemonLock configures the single-instance lock file path for `agent-builder
// daemon` (ADR 065, task 174). Unset defaults to a file under the OS temp dir.
const EnvDaemonLock = "AGENT_BUILDER_DAEMON_LOCK"

// daemonRunLoop is the control-loop seam. Production points it at runControlLoop
// (reused verbatim, no daemon-specific loop logic); daemon-lifecycle tests override
// it so they do not block on the real long-lived loop.
var daemonRunLoop = runControlLoop

// daemonOnRehydrated is a test-only ordering hook fired AFTER RehydrateInFlight and
// BEFORE the control loop, so a test can assert rehydration precedes steady state.
// Nil in production.
var daemonOnRehydrated func(records []runstore.Record)

// daemonOnSchedulerStarted is a test-only hook fired when the goal scheduler is
// started (task 175). Nil in production; a test uses it to assert a scheduler is
// started only when AGENT_BUILDER_SCHEDULE_PATH is set.
var daemonOnSchedulerStarted func()

func daemonUsage(w io.Writer) {
	writef(w, "usage: agent-builder daemon\n\n"+
		"Run agent-builder as a long-lived control-plane process: reuses the orchestrate\n"+
		"control loop, rehydrates in-flight runs at startup (when a run journal is\n"+
		"configured), and shuts down gracefully on SIGINT/SIGTERM, removing its lock file.\n"+
		"A single-instance lock file (AGENT_BUILDER_DAEMON_LOCK) prevents a second daemon.\n"+
		"Every AGENT_BUILDER_* env var the orchestrate subcommand reads applies here too.\n")
}

// daemonLockPath resolves the lock file path from EnvDaemonLock or a default.
func daemonLockPath(getenv func(string) string) string {
	if p := strings.TrimSpace(getenv(EnvDaemonLock)); p != "" {
		return p
	}
	return filepath.Join(os.TempDir(), "agent-builder-daemon.lock")
}

// acquireLock creates the lock file with O_CREATE|O_EXCL (single-instance) and
// writes the PID for operator diagnosis. An already-existing lock is a clear error
// naming the path (no PID-liveness detection in this task's scope, ADR 065).
func acquireLock(path string) (*os.File, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("another agent-builder daemon appears to be running (lock file %q already exists); remove it if this is a stale lock after a hard crash", path)
		}
		return nil, fmt.Errorf("acquire daemon lock %q: %w", path, err)
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	return f, nil
}

// releaseLock closes and removes the lock file, best-effort (never panics).
func releaseLock(f *os.File, path string) {
	if f != nil {
		_ = f.Close()
	}
	_ = os.Remove(path)
}

// runDaemon is the `daemon` subcommand: a long-lived control plane. It wires the
// SIGINT/SIGTERM shutdown context, then delegates to runDaemonWith (the testable
// core with an injectable context + assembly overrides).
func runDaemon(config Config, args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return runDaemonWith(config, args, ctx, assembleOverrides{ctx: ctx})
}

// runDaemonWith is the testable core: lock → assemble (reusing assembleOrchestrate
// unmodified) → rehydrate in-flight runs → runControlLoop under ctx → graceful exit.
// The lock is released on EVERY return path (startup error included). ctx is the
// shutdown context runControlLoop runs under.
func runDaemonWith(config Config, args []string, ctx context.Context, ov assembleOverrides) int {
	flags := newFlagSet("daemon", config.Stderr)
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			daemonUsage(config.Stdout)
			return ExitOK
		}
		return usage(config.Stderr, err)
	}
	if flags.NArg() != 0 {
		return usage(config.Stderr, fmt.Errorf("daemon accepts no positional arguments"))
	}

	lockPath := daemonLockPath(os.Getenv)
	lockFile, err := acquireLock(lockPath)
	if err != nil {
		writef(config.Stderr, "error: %v\n", err)
		return ExitGeneric
	}
	defer releaseLock(lockFile, lockPath)

	ov.ctx = ctx
	oc, cleanup, err := assembleOrchestrate(config, ov)
	if err != nil {
		writef(config.Stderr, "error: %v\n", err)
		if errors.Is(err, errUsageConfig) {
			return ExitUsage
		}
		return ExitGeneric
	}
	defer cleanup()

	// Scheduled goals (ADR 065, task 175): parse the schedule file up front so a
	// malformed schedule fails fast (ExitUsage) BEFORE the control loop, matching the
	// fail-fast-before-goal-intake convention. Unset schedule path = no scheduler
	// (zero goroutines). When set, the scheduler fires goals into the SAME control-loop
	// intake path via a merged message source (no parallel dispatch).
	if schedPath := strings.TrimSpace(os.Getenv(EnvSchedulePath)); schedPath != "" {
		entries, perr := ParseScheduleFile(schedPath)
		if perr != nil {
			writef(config.Stderr, "error: %v\n", perr)
			return ExitUsage
		}
		schedCh := make(chan supervisor.Message, 16)
		scheduler := NewScheduler(entries, realClock{}, func(t supervisor.Task) {
			select {
			case schedCh <- supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: t.ID, Goal: t}:
			case <-ctx.Done():
			}
		})
		go scheduler.Run(ctx)
		if daemonOnSchedulerStarted != nil {
			daemonOnSchedulerStarted()
		}
		oc.source = newMergedMessageSource(ctx, oc.source, schedCh)
		// On shutdown, confirm the scheduler goroutine exited (no leak).
		defer func() { <-scheduler.Done() }()
	}

	// Startup rehydration (task 167/168): resume in-flight runs BEFORE steady state,
	// so a crash mid-goal is picked back up when the daemon restarts. No-op when no
	// run journal is configured.
	if oc.runStore != nil {
		records, rerr := orchestrator.RehydrateInFlight(oc.runStore)
		if rerr == nil {
			if daemonOnRehydrated != nil {
				daemonOnRehydrated(records)
			}
			for _, rec := range records {
				_, _ = oc.orch.ResumeFromRecord(ctx, rec)
			}
		}
	}

	if err := daemonRunLoop(ctx, oc); err != nil {
		writef(config.Stderr, "error: %v\n", err)
		return ExitGeneric
	}
	writef(config.Stdout, "daemon: graceful shutdown complete\n")
	return ExitOK
}
