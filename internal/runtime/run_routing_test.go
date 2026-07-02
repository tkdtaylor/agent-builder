package runtime

// Tests for the real registry+router wiring that replaced the task-077
// stubResolveExecutor (task 095, ADR 043). These unit tests drive the
// runtime-side resolver seam (resolveExecutor / buildCatalog) directly so the
// router selection and the ErrNoEligibleExecutor failure path are asserted
// without subprocesses or the full e2e harness.
//
// TC-095-01: structural — the stub resolver is gone; resolveExecutor exists and
//            routes through registry.LoadFromEnv + router.Select (the
//            buildCatalog seam confirms the live path).
// TC-095-03: a fake two-entry catalog → the router selects the cheaper eligible
//            entry, and the runtime builds the executor for THAT entry.
// TC-095-04: an empty catalog → resolveExecutor returns a descriptive
//            ErrNoEligibleExecutor before any executor is built, and Run returns
//            that error before any sandbox creation / audit event.
//
// Marker cross-references (covered outside this file, kept here so the spec
// marker grep maps every TC to a real, named assertion — these existing tests
// must pass UNMODIFIED, the zero-drift requirement of task 095):
//   - TC-095-02 (zero-drift e2e): tests/e2e TestPhase0EndToEndAcceptance and
//     TestPhase1EndToEndAcceptance — the coding-agent recipe routes to the Claude
//     executor via the real router because the synthetic default Claude entry is
//     the only catalog entry when no AGENT_BUILDER_REGISTRY_* vars are set.
//   - TC-095-05 (unknown recipe errors before dispatch): tests/cli
//     TestRunWithUnknownRecipeReturnsError (the task-077 TC-077-04 regression).
//   - TC-095-06 (F-003 preserved): `make fitness-supervisor-isolation` plus
//     TestSupervisorIsolationExcludesRouterRegistry below (go list -deps assertion).

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// withCatalog swaps the buildCatalog seam for the duration of one test and
// restores it afterward, so tests inject a fake catalog without touching env.
func withCatalog(t *testing.T, entries ...registry.RegistryEntry) {
	t.Helper()
	prev := buildCatalog
	t.Cleanup(func() { buildCatalog = prev })
	buildCatalog = func(_ Config) (*registry.Catalog, error) {
		c := registry.NewCatalog()
		for _, e := range entries {
			c.RegisterEntry(e)
		}
		return c, nil
	}
}

// TC-095-01: structural — the task-077 stub resolver is gone, and the live
// resolver routes through registry.LoadFromEnv + router.Select. Source
// inspection of run.go is the recorded evidence for this assertion.
func TestStubResolverRemovedAndLiveRouterWired(t *testing.T) {
	src, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatalf("read run.go: %v", err)
	}
	text := string(src)

	if strings.Contains(text, "stubResolveExecutor") {
		t.Fatal("TC-095-01: stubResolveExecutor must be removed from internal/runtime (task 095 replaces it with the real router)")
	}
	// The live path constructs the catalog from the registry env loader and
	// selects via the router.
	for _, want := range []string{
		"registry.LoadFromEnv()",
		"router.New(",
		"r.Select(toRouterSpec(spec))",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("TC-095-01: run.go does not wire %q — the real registry+router path is not in place", want)
		}
	}
}

// TC-095-03: fake two-entry catalog — the router selects the cheaper eligible
// entry, and resolveExecutor returns the executor for that entry.
func TestResolveExecutorSelectsCheapestEligible(t *testing.T) {
	local := registry.RegistryEntry{
		ID:             "local",
		Harness:        registry.HarnessClaudeCLI, // local entry: SecretRef == "" → translation proxy
		CapabilityTier: 1,
		CostWeight:     1,
		Endpoint:       "http://localhost:8080",
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
	claudeOAuth := registry.RegistryEntry{
		ID:             "claude-oauth",
		Harness:        registry.HarnessClaudeCLI,
		CapabilityTier: 3,
		CostWeight:     10,
		SecretRef:      "claude-token",
		Endpoint:       "https://api.anthropic.com",
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
	}
	withCatalog(t, claudeOAuth, local) // registration order deliberately puts the expensive entry first

	config := Config{ClaudeCLI: "claude", Worktree: "/tmp/work", ClaudeToken: "sk-test"}
	spec := recipe.RoutingSpec{MinCapability: 1, SensitivityHint: recipe.SensitivitySensitive}

	exec, entry, _, err := resolveExecutor(spec, config)
	if err != nil {
		t.Fatalf("resolveExecutor error = %v, want nil", err)
	}
	if entry.ID != "local" {
		t.Fatalf("selected entry = %q, want %q (cheapest eligible per ADR 043)", entry.ID, "local")
	}
	if entry.CostWeight != 1 {
		t.Fatalf("selected entry CostWeight = %d, want 1 (the cheaper of {1,10})", entry.CostWeight)
	}
	if exec == nil {
		t.Fatal("resolveExecutor returned nil executor for the selected entry")
	}
}

// TC-095-03 (control): with only the expensive entry eligible at the required
// capability, the router selects it — proving the cheap entry won above because
// it was eligible, not by accident of ordering.
func TestResolveExecutorSelectsHigherTierWhenCapabilityDemandsIt(t *testing.T) {
	local := registry.RegistryEntry{
		ID: "local", Harness: registry.HarnessClaudeCLI, CapabilityTier: 1, CostWeight: 1,
		Endpoint: "http://localhost:8080", Availability: registry.Availability{Status: registry.AvailStatusAvailable},
	}
	claudeOAuth := registry.RegistryEntry{
		ID: "claude-oauth", Harness: registry.HarnessClaudeCLI, CapabilityTier: 3, CostWeight: 10,
		SecretRef: "claude-token", Endpoint: "https://api.anthropic.com",
		Availability: registry.Availability{Status: registry.AvailStatusAvailable},
	}
	withCatalog(t, local, claudeOAuth)

	config := Config{ClaudeCLI: "claude", Worktree: "/tmp/work", ClaudeToken: "sk-test"}
	// MinCapability 2 excludes the tier-1 local entry → only claude-oauth qualifies.
	_, entry, _, err := resolveExecutor(recipe.RoutingSpec{MinCapability: 2}, config)
	if err != nil {
		t.Fatalf("resolveExecutor error = %v, want nil", err)
	}
	if entry.ID != "claude-oauth" {
		t.Fatalf("selected entry = %q, want %q (only entry meeting capability floor 2)", entry.ID, "claude-oauth")
	}
}

// TC-095-04: an empty catalog → resolveExecutor returns a descriptive error
// wrapping ErrNoEligibleExecutor, and builds no executor.
func TestResolveExecutorEmptyRegistryErrors(t *testing.T) {
	withCatalog(t) // no entries

	config := Config{ClaudeCLI: "claude", Worktree: "/tmp/work", ClaudeToken: "sk-test"}
	exec, _, _, err := resolveExecutor(recipe.RoutingSpec{MinCapability: 1}, config)
	if err == nil {
		t.Fatal("resolveExecutor error = nil, want ErrNoEligibleExecutor")
	}
	if !errors.Is(err, router.ErrNoEligibleExecutor) {
		t.Fatalf("resolveExecutor error = %v, want it to wrap router.ErrNoEligibleExecutor", err)
	}
	if !strings.Contains(err.Error(), "no eligible executor") {
		t.Fatalf("error message = %q, want it to describe the no-eligible-executor condition", err.Error())
	}
	if exec != nil {
		t.Fatalf("resolveExecutor returned a non-nil executor (%T) on the empty-registry path, want nil", exec)
	}
}

// TC-095-04: Run with an empty registry returns the resolve error BEFORE any
// sandbox creation, and emits NO audit events. The FakeSink is wired through the
// supervisor option path; because the failure happens before supervisor.New, the
// sink must never receive an event.
func TestRunEmptyRegistryFailsBeforeDispatchNoAudit(t *testing.T) {
	withCatalog(t) // empty registry → ErrNoEligibleExecutor

	// Swap the audit BlockSink constructor for a FakeSink so we can assert no
	// events are emitted. newAuditSink is the seam; restore it on cleanup.
	sink := audit.NewFakeSink()
	prev := newAuditSink
	t.Cleanup(func() { newAuditSink = prev })
	newAuditSink = func(Config) (audit.Sink, error) { return sink, nil }

	root := t.TempDir()
	taskRoot := filepath.Join(root, "tasks")
	worktree := filepath.Join(root, "work")
	mustMkdir(t, taskRoot)
	mustMkdir(t, worktree)
	// A ready task so goalSource.Next() returns one — proving the resolver, not an
	// idle goal source, is what stops the run.
	writeRoutingTaskFixture(t, taskRoot)

	config := Config{
		TaskRoot:        taskRoot,
		Worktree:        worktree,
		ClaudeCLI:       "claude",
		ClaudeToken:     "sk-test",
		ExecBoxLauncher: "containment/execution-box/run.sh",
		RunTimeout:      5_000_000_000, // 5s
		MaxAttempts:     1,
		PublishRemote:   "origin",
		RecipeName:      "coding-agent",
		AuditRecordPath: filepath.Join(root, "audit.ndjson"), // forces audit sink construction path
	}

	err := Run(context.Background(), config, nil)
	if err == nil {
		t.Fatal("Run error = nil, want ErrNoEligibleExecutor before dispatch")
	}
	if !errors.Is(err, router.ErrNoEligibleExecutor) {
		t.Fatalf("Run error = %v, want it to wrap router.ErrNoEligibleExecutor", err)
	}
	if got := sink.Events(); len(got) != 0 {
		t.Fatalf("audit sink received %d event(s) on the no-eligible-executor path, want 0: %#v", len(got), got)
	}
	// No sandbox box was created: the run record path was never set, and no
	// supervisor run occurred. The audit assertion above is the load-bearing
	// proof that dispatch did not begin.
}

// TC-095-06 / REQ-095-04: F-003 preserved — the supervisor import graph must
// contain neither internal/router nor internal/registry after the stub
// replacement. This asserts the same invariant `make fitness-supervisor-isolation`
// checks, but inline so a regression fails the unit suite directly.
func TestSupervisorIsolationExcludesRouterRegistry(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "github.com/tkdtaylor/agent-builder/internal/supervisor/...")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps supervisor failed: %v\n%s", err, out)
	}
	deps := string(out)
	for _, forbidden := range []string{
		"github.com/tkdtaylor/agent-builder/internal/router",
		"github.com/tkdtaylor/agent-builder/internal/registry",
	} {
		if strings.Contains(deps, forbidden) {
			t.Fatalf("TC-095-06: supervisor import graph contains forbidden package %q (F-003 violated)", forbidden)
		}
	}
}

func mustMkdir(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %q: %v", dir, err)
	}
}

func writeRoutingTaskFixture(t *testing.T, taskRoot string) {
	t.Helper()
	mustMkdir(t, filepath.Join(taskRoot, "docs/plans"))
	mustMkdir(t, filepath.Join(taskRoot, "docs/tasks/backlog"))
	if err := os.WriteFile(filepath.Join(taskRoot, "docs/plans/roadmap.md"), []byte("# Roadmap\n"), 0o644); err != nil {
		t.Fatalf("write roadmap: %v", err)
	}
	task := `# Task 001: first

**Project:** agent-builder
**Created:** 2026-06-27
**Status:** ready

## Goal
Fixture task.
`
	if err := os.WriteFile(filepath.Join(taskRoot, "docs/tasks/backlog/001-first.md"), []byte(task), 0o644); err != nil {
		t.Fatalf("write task: %v", err)
	}
}
