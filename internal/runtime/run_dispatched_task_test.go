package runtime

// Tests for task 119: route dispatched sub-goal task to worker (ADR 055 seam 2).
//
// TC-001 and TC-003 live in internal/cli (orchestrate_119_test.go) where the
// dispatch seam (newTransportDispatch / runWorker spy) lives.
//
// TC-002: the single-task `run` subcommand still discovers task files unchanged.
//   When Config.DispatchedTask is nil, Run selects the next ready task via the
//   recipe's file-based GoalSourceFactory (tasksource), exactly as before task
//   119. This verifies the 119 change is scoped to the orchestrate path.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"
)

// recordingGoalSource wraps a real tasksource.Source and records the task ID
// selected by Next so the test can assert the correct file-based task was
// picked without coupling to the sandbox layer.
type recordingGoalSource struct {
	src    *tasksource.Source
	picked string
	ok     bool
}

func (r *recordingGoalSource) Next() (supervisor.Task, bool, error) {
	task, ok, err := r.src.Next()
	if ok {
		r.picked = task.ID
		r.ok = true
	}
	return task, ok, err
}

// noopResultSink satisfies supervisor.ResultSink for tests that never reach the
// publish step.
type noopResultSink struct{}

func (n *noopResultSink) Publish(_ context.Context, _ supervisor.PublishRequest) (supervisor.PublishResult, error) {
	return supervisor.PublishResult{}, nil
}

// tc119SpyRecipeName is the unique recipe name registered by TC-002. It is
// registered once at package-init time so parallel tests don't race on
// recipe.Register (which panics on re-registration).
const tc119SpyRecipeName = "tc119-spy-run-path"

// tc119SpyGoalSource is the shared recording goal source that the spy recipe
// wires up. It is reset at the start of each test that uses it.
var tc119SpyGoalSource *recordingGoalSource

func init() {
	recipe.Register(tc119SpyRecipeName, func() (recipe.Recipe, error) {
		goalSourceFactory := func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) {
			src := tasksource.New(
				os.DirFS(cfg.TaskRoot()),
				tasksource.DefaultRoadmapPath,
				tasksource.DefaultTaskDirs...,
			)
			spy := &recordingGoalSource{src: src}
			tc119SpyGoalSource = spy
			return spy, nil
		}
		return recipe.New(
			goalSourceFactory,
			recipe.RoutingSpec{MinCapability: 1},
			newProductionGateFactory,
			func(_ recipe.SeamConfig) (supervisor.ResultSink, error) {
				return &noopResultSink{}, nil
			},
			nil,
		), nil
	})
}

// TestTC119_02_RunPathUsesTaskSourceWhenDispatchedTaskNil verifies that when
// Config.DispatchedTask is nil the run path uses the recipe's file-based
// GoalSourceFactory (tasksource) and picks the seeded ready task file — not
// a dispatched goal.
func TestTC119_02_RunPathUsesTaskSourceWhenDispatchedTaskNil(t *testing.T) {
	// Seed a minimal task-file tree (one ready task, ID "001").
	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	worktree := filepath.Join(root, "work")
	mustMkdir(t, taskRoot)
	mustMkdir(t, worktree)
	writeRoutingTaskFixture(t, taskRoot) // writes task "001" as ready

	// Empty catalog → resolveExecutor fails after the goal source is called.
	// This is the same trick used in TestRunEmptyRegistryFailsBeforeDispatchNoAudit
	// (run_routing_test.go) to assert goal-source behaviour without a real sandbox.
	withCatalog(t) // empty → ErrNoEligibleExecutor

	config := Config{
		TaskRoot:        taskRoot,
		Worktree:        worktree,
		ClaudeCLI:       "claude",
		ClaudeToken:     "sk-test",
		ExecBoxLauncher: "containment/execution-box/run.sh",
		RunTimeout:      5 * time.Second,
		MaxAttempts:     1,
		PublishRemote:   "origin",
		RecipeName:      tc119SpyRecipeName,
		// DispatchedTask is nil — this is the run-path regression assertion.
	}

	err := Run(context.Background(), config, nil)

	// Run must fail at resolveExecutor (empty catalog), not at the goal source.
	if err == nil {
		t.Fatal("TC-002: expected error from empty catalog, got nil")
	}
	if !errors.Is(err, router.ErrNoEligibleExecutor) {
		t.Fatalf("TC-002: error = %v, want router.ErrNoEligibleExecutor (run must reach the executor step)", err)
	}

	// The spy goal source must have been wired and its Next() called.
	spy := tc119SpyGoalSource
	if spy == nil {
		t.Fatal("TC-002: spy goal source was never wired — GoalSourceFactory was not called on the run path")
	}
	if !spy.ok {
		t.Error("TC-002: spy goal source Next() was not called or found no task — tasksource must select the seeded ready task")
	}
	// The task selected must be the seeded file task "001", not a dispatched goal.
	if spy.picked != "001" {
		t.Errorf("TC-002: tasksource selected task ID %q, want %q — run path must discover task files", spy.picked, "001")
	}
}
