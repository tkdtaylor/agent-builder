package cli

// Task 175: config-declared interval/daily scheduler firing inside the daemon.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

type schedFakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *schedFakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *schedFakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func writeSchedule(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "schedule.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatalf("write schedule: %v", err)
	}
	return p
}

// TC-175-01: schedule file parsing (every + at).
func TestTC175_01_ParseSchedule(t *testing.T) {
	p := writeSchedule(t, `{"entries":[{"goal":"nightly build","every":"1h"},{"goal":"morning report","at":"03:00"}]}`)
	entries, err := ParseScheduleFile(p)
	if err != nil {
		t.Fatalf("ParseScheduleFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(entries))
	}
	if entries[0].Goal != "nightly build" || entries[0].Every != time.Hour || entries[0].IsAt {
		t.Errorf("entry0 = %+v, want {nightly build, 1h, every}", entries[0])
	}
	if entries[1].Goal != "morning report" || !entries[1].IsAt || entries[1].At != 3*time.Hour {
		t.Errorf("entry1 = %+v, want {morning report, at 03:00}", entries[1])
	}
}

// TC-175-02: malformed schedule files fail fast.
func TestTC175_02_ParseScheduleMalformed(t *testing.T) {
	cases := map[string]string{
		"bad_every":  `{"entries":[{"goal":"x","every":"soon"}]}`,
		"both_set":   `{"entries":[{"goal":"x","every":"1h","at":"03:00"}]}`,
		"neither":    `{"entries":[{"goal":"x"}]}`,
		"empty_goal": `{"entries":[{"goal":"","every":"1h"}]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeSchedule(t, body)
			if _, err := ParseScheduleFile(p); err == nil {
				t.Fatalf("%s: ParseScheduleFile returned nil error, want a validation error", name)
			}
		})
	}
}

// TC-175-03: an every entry fires repeatedly on a fake clock.
func TestTC175_03_EveryFiresRepeatedly(t *testing.T) {
	fc := &schedFakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	var fires int
	s := NewScheduler([]ScheduleEntry{{Goal: "tick", Every: 10 * time.Minute}}, fc, func(supervisor.Task) {
		mu.Lock()
		fires++
		mu.Unlock()
	})
	for _, step := range []time.Duration{10 * time.Minute, 10 * time.Minute, 10 * time.Minute, 5 * time.Minute} {
		fc.advance(step)
		s.checkEntries()
	}
	mu.Lock()
	defer mu.Unlock()
	if fires != 3 {
		t.Fatalf("fires = %d, want 3 (at 10m/20m/30m, not the trailing 5m)", fires)
	}
}

// TC-175-04: an at entry fires once per day at the boundary.
func TestTC175_04_AtFiresOncePerDay(t *testing.T) {
	fc := &schedFakeClock{t: time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)}
	var mu sync.Mutex
	var fires int
	s := NewScheduler([]ScheduleEntry{{Goal: "report", At: 3 * time.Hour, IsAt: true}}, fc, func(supervisor.Task) {
		mu.Lock()
		fires++
		mu.Unlock()
	})
	// Day 1: 02:00 (no) → 03:30 (fire) → 04:00 (no refire same day).
	s.checkEntries()
	fc.advance(90 * time.Minute)
	s.checkEntries()
	fc.advance(30 * time.Minute)
	s.checkEntries()
	// Day 2: 02:00 (no) → 03:30 (fire).
	fc.advance(22 * time.Hour)
	s.checkEntries()
	fc.advance(90 * time.Minute)
	s.checkEntries()

	mu.Lock()
	defer mu.Unlock()
	if fires != 2 {
		t.Fatalf("fires = %d, want 2 (one per day-boundary crossing)", fires)
	}
}

// --- TC-175-05 fixtures: minimal orchestrator wiring ---

type schedRecPlanner struct {
	mu   *sync.Mutex
	seen *[]supervisor.Task
}

func (p schedRecPlanner) Plan(g supervisor.Task) (orchestrator.Plan, error) {
	p.mu.Lock()
	*p.seen = append(*p.seen, g)
	p.mu.Unlock()
	return orchestrator.Plan{GoalID: g.ID, Goal: g.Spec, SubGoals: []orchestrator.SubGoal{
		{RecipeName: orchestrator.DefaultRecipeName, Task: supervisor.Task{ID: g.ID + "-0", Spec: g.Spec}},
	}}, nil
}

type schedBlockingSource struct{ ctx context.Context }

func (s schedBlockingSource) Next() (supervisor.Message, bool, error) {
	<-s.ctx.Done()
	return supervisor.Message{}, false, nil
}

// TC-175-05: a fired entry routes through the standard goal-intake path (the fake
// Planner inside a real Orchestrator receives the scheduled goal via runControlLoop).
func TestTC175_05_FiredEntryRoutesThroughIntake(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	var seen []supervisor.Task

	// Build the real orchestrate stack (intake=auto via setBaseConfigEnv, so the goal
	// actor goes straight to planning). Override the planner to record the goal that
	// reaches the intake, and use a blocking primary source so only the scheduled goal
	// flows through the merged source.
	ov := daemonAssembleOK(t)
	ov.planner = schedRecPlanner{mu: &mu, seen: &seen}
	ov.source = nil
	ov.messageSource = schedBlockingSource{ctx: ctx}
	ov.ctx = ctx
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, ov)
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)

	schedCh := make(chan supervisor.Message, 4)
	oc.source = newMergedMessageSource(ctx, oc.source, schedCh)
	loopDone := make(chan struct{})
	go func() { _ = runControlLoop(ctx, oc); close(loopDone) }()

	fc := &schedFakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	s := NewScheduler([]ScheduleEntry{{Goal: "scheduled goal text", Every: time.Nanosecond}}, fc, func(tk supervisor.Task) {
		select {
		case schedCh <- supervisor.Message{Kind: supervisor.MsgNewGoal, GoalID: tk.ID, Goal: tk}:
		case <-ctx.Done():
		}
	})
	fc.advance(time.Second)
	s.checkEntries() // fires → pushes a MsgNewGoal into the merged source

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-loopDone

	mu.Lock()
	defer mu.Unlock()
	if len(seen) < 1 {
		t.Fatal("scheduled goal never reached the Planner (intake path); the scheduler must route through runControlLoop")
	}
	if seen[0].Spec != "scheduled goal text" {
		t.Errorf("intake goal Spec = %q, want the schedule entry text", seen[0].Spec)
	}
	if !strings.HasPrefix(seen[0].ID, "sched-0-") {
		t.Errorf("intake goal ID = %q, want a deterministic sched-0-<timestamp> ID", seen[0].ID)
	}
}

// TC-175-06: unset schedule path starts no scheduler.
func TestTC175_06_UnsetScheduleNoScheduler(t *testing.T) {
	started := 0
	prev := daemonOnSchedulerStarted
	daemonOnSchedulerStarted = func() { started++ }
	t.Cleanup(func() { daemonOnSchedulerStarted = prev })

	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	t.Setenv(EnvDaemonLock, lockPath)
	// EnvSchedulePath deliberately unset.
	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error { return nil })

	code := runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, context.Background(), daemonAssembleOK(t))
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK", code)
	}
	if started != 0 {
		t.Errorf("scheduler started %d times with no schedule path, want 0", started)
	}
}

// TC-175-06b (positive control): a set schedule path starts exactly one scheduler.
func TestTC175_06b_SetScheduleStartsScheduler(t *testing.T) {
	started := 0
	prev := daemonOnSchedulerStarted
	daemonOnSchedulerStarted = func() { started++ }
	t.Cleanup(func() { daemonOnSchedulerStarted = prev })

	lockPath := filepath.Join(t.TempDir(), "daemon.lock")
	t.Setenv(EnvDaemonLock, lockPath)
	t.Setenv(EnvSchedulePath, writeSchedule(t, `{"entries":[{"goal":"x","every":"1h"}]}`))
	// Cancel the shutdown context from inside the fake loop so the scheduler goroutine
	// (started under this ctx) exits and the daemon's scheduler-join defer unblocks.
	ctx, cancel := context.WithCancel(context.Background())
	setDaemonRunLoop(t, func(context.Context, orchestrateConfig) error { cancel(); return nil })

	code := runDaemonWith(Config{Stdout: discard(), Stderr: discard()}, nil, ctx, daemonAssembleOK(t))
	if code != ExitOK {
		t.Fatalf("exit = %d, want ExitOK", code)
	}
	if started != 1 {
		t.Errorf("scheduler started %d times with a schedule path, want 1", started)
	}
}

// TC-175-07: scheduler stops cleanly on context cancellation.
func TestTC175_07_SchedulerStopsOnCancel(t *testing.T) {
	fc := &schedFakeClock{t: time.Now()}
	s := NewScheduler([]ScheduleEntry{{Goal: "x", Every: time.Minute}}, fc, func(supervisor.Task) {})
	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
	cancel()
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("scheduler goroutine did not exit within 2s of cancellation")
	}
}
