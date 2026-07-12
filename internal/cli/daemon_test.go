package cli

// Task 174: agent-builder daemon (process lifecycle: lock, signals, rehydration).

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runstore"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// daemonAssembleOK returns an assembleOverrides that makes assembleOrchestrate
// succeed with fakes (no real Telegram/keys needed beyond the injected signing key).
func daemonAssembleOK(t *testing.T) assembleOverrides {
	t.Helper()
	setBaseConfigEnv(t)
	return assembleOverrides{
		policyClient: &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:     func(context.Context, orchestrator.SubGoal, runtimewiring.Config) error { return nil },
		auditSink:    audit.NewFakeSink(),
		planner:      twoRecipePlanner(),
		source:       &stubGoalSource{goals: []supervisor.Task{}},
		signingKey:   testSigningKey(t),
	}
}

// setDaemonRunLoop overrides the control-loop seam for the test and restores it.
func setDaemonRunLoop(t *testing.T, fn func(context.Context, orchestrateConfig) error) {
	t.Helper()
	prev := daemonRunLoop
	daemonRunLoop = fn
	t.Cleanup(func() { daemonRunLoop = prev })
}

// TC-174-01: Main dispatches "daemon" (help flag, no long-lived loop entered).
func TestTC174_01_MainDispatchesDaemonHelp(t *testing.T) {
	var out, errb bytes.Buffer
	code := Main(Config{Args: []string{"daemon", "-h"}, Stdout: &out, Stderr: &errb})
	if code != ExitOK {
		t.Fatalf("daemon -h exit = %d, want ExitOK; stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "daemon") {
		t.Errorf("daemon -h stdout = %q, want daemon usage text", out.String())
	}
}

// TC-174-02: a pre-existing lock prevents a second instance.
func TestTC174_02_LockPreventsSecondInstance(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	// Simulate a first instance already holding the lock.
	if err := os.WriteFile(lockPath, []byte("999\n"), 0o600); err != nil {
		t.Fatalf("pre-create lock: %v", err)
	}
	t.Setenv(EnvDaemonLock, lockPath)

	invoked := false
	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error { invoked = true; return nil })

	var errb bytes.Buffer
	code := runDaemonWith(Config{Stdout: discard(), Stderr: &errb}, nil, context.Background(), assembleOverrides{})
	if code == ExitOK {
		t.Fatalf("second instance exit = ExitOK, want non-zero")
	}
	if !strings.Contains(errb.String(), lockPath) {
		t.Errorf("stderr = %q, want it to name the lock path %q", errb.String(), lockPath)
	}
	if invoked {
		t.Error("control loop was invoked despite the lock being held")
	}
	// The pre-existing lock file is untouched (not removed by the failed second instance).
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("pre-existing lock file was removed: %v", err)
	}
}

// TC-174-03: lock acquisition succeeds when none exists; lock is held during the loop.
func TestTC174_03_LockAcquiredWhenNoneExists(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	t.Setenv(EnvDaemonLock, lockPath)

	heldDuringLoop := false
	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error {
		// The lock must exist while the control loop runs.
		if _, err := os.Stat(lockPath); err == nil {
			heldDuringLoop = true
		}
		return nil
	})

	code := runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, context.Background(), daemonAssembleOK(t))
	if code != ExitOK {
		t.Fatalf("runDaemonWith exit = %d, want ExitOK", code)
	}
	if !heldDuringLoop {
		t.Error("lock file did not exist while the control loop ran")
	}
	// Removed on clean shutdown.
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file not removed after shutdown: stat err=%v", err)
	}
}

// TC-174-04: lock file removed on graceful (ctx-cancel) shutdown.
func TestTC174_04_LockRemovedOnGracefulShutdown(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	t.Setenv(EnvDaemonLock, lockPath)

	setDaemonRunLoop(t, func(ctx context.Context, _ orchestrateConfig) error {
		<-ctx.Done() // block until the "signal" fires
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, ctx, daemonAssembleOK(t))
	}()
	time.Sleep(20 * time.Millisecond)
	cancel() // simulate SIGTERM

	select {
	case code := <-done:
		if code != ExitOK {
			t.Fatalf("exit = %d, want ExitOK after graceful shutdown", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runDaemonWith did not return after ctx cancel")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file not removed after graceful shutdown: stat err=%v", err)
	}
}

// TC-174-05: lock file removed on a startup-assembly error (missing signing key).
func TestTC174_05_LockRemovedOnAssemblyError(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	t.Setenv(EnvDaemonLock, lockPath)
	setBaseConfigEnv(t)

	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error {
		t.Fatal("control loop must not run when assembly fails")
		return nil
	})

	// No signingKey override and no env signing key → SEC-003 assembly failure.
	code := runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, context.Background(), assembleOverrides{})
	if code == ExitOK {
		t.Fatalf("exit = ExitOK, want non-zero on assembly failure")
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Errorf("lock file left behind after a startup error: stat err=%v", err)
	}
}

// TC-174-07: RunStore-configured startup rehydrates BEFORE the steady-state loop.
func TestTC174_07_RehydrateBeforeLoop(t *testing.T) {
	dir := t.TempDir()
	store, err := runstore.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	if err := store.Save(runstore.Record{GoalID: "g-inflight", Status: runstore.StatusRunning}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	var mu sync.Mutex
	var order []string
	var rehydrated []runstore.Record

	prevHook := daemonOnRehydrated
	daemonOnRehydrated = func(records []runstore.Record) {
		mu.Lock()
		order = append(order, "rehydrate")
		rehydrated = append([]runstore.Record{}, records...)
		mu.Unlock()
	}
	t.Cleanup(func() { daemonOnRehydrated = prevHook })

	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error {
		mu.Lock()
		order = append(order, "loop")
		mu.Unlock()
		return nil
	})

	ov := daemonAssembleOK(t)
	ov.runStore = store
	code := runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, context.Background(), ov)
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK", code)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "rehydrate" || order[1] != "loop" {
		t.Fatalf("order = %v, want [rehydrate loop] (rehydration must precede the loop)", order)
	}
	found := false
	for _, r := range rehydrated {
		if r.GoalID == "g-inflight" {
			found = true
		}
	}
	if !found {
		t.Errorf("RehydrateInFlight results %v did not include the seeded in-flight record", rehydrated)
	}
}

// TC-174-08: assembleOrchestrate reused unmodified — daemon.go has exactly one
// assembleOrchestrate( call site and no forked assembly function.
func TestTC174_08_AssembleOrchestrateReusedNoFork(t *testing.T) {
	src, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatalf("read daemon.go: %v", err)
	}
	s := string(src)
	if n := strings.Count(s, "assembleOrchestrate("); n != 1 {
		t.Errorf("daemon.go has %d assembleOrchestrate( call sites, want exactly 1 (no forked assembly)", n)
	}
	if strings.Contains(s, "func assembleDaemon") || strings.Contains(s, "func assembleOrchestrateDaemon") {
		t.Error("daemon.go defines a parallel assembly function; it must reuse assembleOrchestrate unmodified")
	}
}
