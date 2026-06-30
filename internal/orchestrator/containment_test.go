package orchestrator_test

// Tests for task 085 — orchestrator self-containment + policy gating + fleet audit.
// Governing ADR: ADR 050 (extends ADR 042 / 038 / 026).
//
//   TC-085-01 — orchestrator run record carries containment=exec-sandbox (L2)
//   TC-085-02 — spawn-worker per-sub-goal gate: deny → no worker + denial reported (L2)
//   TC-085-03 — fleet-wide audit chain covers both tiers in one chain (L2 + L5)
//   TC-085-04 — orchestrator egress posture is default-deny (L2)
//   TC-085-05 — self-repo bright line: runtime deny + static fitness detector (L2/L3)

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"

	_ "github.com/tkdtaylor/agent-builder/internal/recipe/docsfix"
)

// recordingPolicy records every Decide request and returns a per-action decision.
// byAction maps an action name to the decision to return; byRecipe (keyed on the
// spawn-worker Resource.ID) overrides for a specific recipe. Unmapped actions
// default to allow.
type recordingPolicy struct {
	mu       sync.Mutex // guards requests; Decide is called concurrently under task-086 fan-out
	requests []policy.DecideRequest
	byAction map[string]policy.Decision
	byRecipe map[string]policy.Decision // overrides spawn-worker by Resource.ID
	failOn   string                     // action name to return a transport error for
}

func (p *recordingPolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	if p.failOn != "" && req.Action.Name == p.failOn {
		// Fail-closed: the production client returns DecisionDeny on any error.
		return policy.DecideResponse{Decision: policy.DecisionDeny}, errString("simulated policy transport error")
	}
	if req.Action.Name == orchestrator.SpawnWorkerAction {
		if d, ok := p.byRecipe[req.Resource.ID]; ok {
			return policy.DecideResponse{Decision: d}, nil
		}
	}
	if d, ok := p.byAction[req.Action.Name]; ok {
		return policy.DecideResponse{Decision: d}, nil
	}
	return policy.DecideResponse{Decision: policy.DecisionAllow}, nil
}

// --- TC-085-01 — containment=exec-sandbox -------------------------------------

func TestTC085_01_OrchestratorRunRecordContainmentExecSandbox(t *testing.T) {
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{}, runtime.Config{},
		orchestrator.WithRequireApproval(false),
	)

	c := o.Containment()
	if c.Profile != orchestrator.ContainmentProfileExecSandbox {
		t.Errorf("containment profile = %q, want %q", c.Profile, orchestrator.ContainmentProfileExecSandbox)
	}
	if c.Profile != "exec-sandbox" {
		t.Errorf("containment profile constant = %q, want literal %q (same as worker boxes)", c.Profile, "exec-sandbox")
	}
	if !c.Rootless {
		t.Error("containment Rootless = false, want true (same isolation as workers)")
	}
	if !c.ReadOnlyRootfs {
		t.Error("containment ReadOnlyRootfs = false, want true")
	}
	if !c.ResourceLimited {
		t.Error("containment ResourceLimited = false, want true")
	}
	t.Logf("TC-085-01 L2: containment=%+v (L6 live Podman+runsc enforcement operator-deferred)", c)
}

// --- TC-085-04 — egress default-deny ------------------------------------------

func TestTC085_04_OrchestratorEgressDefaultDeny(t *testing.T) {
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		&fakePolicy{decision: policy.DecisionAllow},
		&fakeReporter{}, runtime.Config{},
		orchestrator.WithRequireApproval(false),
	)

	got := o.Containment().EgressPolicy
	if got != orchestrator.EgressDefaultDeny {
		t.Errorf("egress policy = %q, want %q", got, orchestrator.EgressDefaultDeny)
	}
	if got != "default-deny" {
		t.Errorf("egress policy constant = %q, want literal %q (same nftables model as workers)", got, "default-deny")
	}
	t.Logf("TC-085-04 L2: egress=%q (L6 live nftables probe operator-deferred)", got)
}

// --- TC-085-02 — spawn-worker per-sub-goal gate -------------------------------

func TestTC085_02_SpawnWorkerDenySkipsWorkerAndReports(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
	pol := &recordingPolicy{
		byAction: map[string]policy.Decision{orchestrator.SpawnAction: policy.DecisionAllow},
		byRecipe: map[string]policy.Decision{"docs-fix": policy.DecisionDeny}, // deny the 2nd worker
	}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithAuditSink(sink),
		orchestrator.WithRequireApproval(false),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}

	result, err := o.Handle(context.Background(), goal)
	if err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	// Only the allowed worker (coding-agent) is dispatched; docs-fix is NOT spawned.
	if spy.count() != 1 {
		t.Fatalf("spawn-worker deny: want exactly 1 dispatch, got %d", spy.count())
	}
	for _, r := range spy.recipeNames {
		if r == "docs-fix" {
			t.Fatalf("denied worker docs-fix was dispatched: recipeNames=%v", spy.recipeNames)
		}
	}

	// The denied sub-goal is recorded as a failed outcome naming the policy denial.
	if len(result.Outcomes) != 2 {
		t.Fatalf("want 2 outcomes, got %d", len(result.Outcomes))
	}
	denied := result.Outcomes[1]
	if denied.Recipe != "docs-fix" || denied.Success {
		t.Errorf("outcome 1: want docs-fix failed (denied), got %+v", denied)
	}
	if !strings.Contains(denied.Detail, "denied") && !strings.Contains(denied.Detail, "policy") {
		t.Errorf("denied outcome detail = %q, want it to mention policy/denied", denied.Detail)
	}

	// The denial is reported via the Reporter, naming the denied recipe.
	foundDenial := false
	for _, msg := range rep.Reported() {
		if strings.Contains(msg, "denied") && strings.Contains(msg, "docs-fix") {
			foundDenial = true
		}
	}
	if !foundDenial {
		t.Errorf("no denial report naming docs-fix in reports: %v", rep.Reported())
	}

	// The spawn-worker request was issued with the correct action/resource shape.
	var sawSpawnWorker bool
	for _, req := range pol.requests {
		if req.Action.Name == orchestrator.SpawnWorkerAction {
			sawSpawnWorker = true
			if req.Resource.Type != "recipe" {
				t.Errorf("spawn-worker Resource.Type = %q, want %q", req.Resource.Type, "recipe")
			}
			if req.Resource.ID == "" {
				t.Error("spawn-worker Resource.ID is empty, want the recipe name")
			}
		}
	}
	if !sawSpawnWorker {
		t.Error("no spawn-worker decision was issued")
	}
	t.Logf("TC-085-02: spawn-worker deny → 1 dispatch (coding-agent), docs-fix skipped + denied-outcome + report")
}

func TestTC085_02_SpawnWorkerFailClosedOnPolicyError(t *testing.T) {
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
	// Allow spawn-plan, but the spawn-worker decision errors → fail-closed deny.
	pol := &recordingPolicy{
		byAction: map[string]policy.Decision{orchestrator.SpawnAction: policy.DecisionAllow},
		failOn:   orchestrator.SpawnWorkerAction,
	}
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithAuditSink(sink),
		orchestrator.WithRequireApproval(false),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X"}

	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}
	if spy.count() != 0 {
		t.Fatalf("fail-closed: a spawn-worker policy error must skip dispatch, got %d dispatches", spy.count())
	}
	t.Log("TC-085-02 fail-closed: spawn-worker policy error → 0 dispatches")
}

// --- TC-085-03 — fleet-wide audit chain ---------------------------------------

// fleetDispatchSpy is a dispatch seam that ALSO appends two worker events
// (containment, finish) per dispatched worker to the shared audit.Sink, modelling
// the worker tier writing into the same chain as the orchestrator.
type fleetDispatchSpy struct {
	*dispatchSpy
	sink audit.Sink
}

func (s *fleetDispatchSpy) fn(ctx context.Context, sub orchestrator.SubGoal, base runtime.Config) error {
	if err := s.dispatchSpy.fn(ctx, sub, base); err != nil {
		return err
	}
	// Worker-tier events on the SAME chain.
	_ = s.sink.Append(audit.AuditEvent{Action: audit.ActionContainment, TaskID: sub.Task.ID, RunID: sub.Task.ID, Detail: audit.EventDetail{Launcher: "exec-sandbox"}})
	_ = s.sink.Append(audit.AuditEvent{Action: audit.ActionFinish, TaskID: sub.Task.ID, RunID: sub.Task.ID, Outcome: audit.OutcomeCompleted})
	return nil
}

func TestTC085_03_FleetAuditChainCoversBothTiers(t *testing.T) {
	sink := audit.NewFakeSink()
	spy := &fleetDispatchSpy{dispatchSpy: newDispatchSpy(), sink: sink}
	rep := &fakeReporter{}
	pol := &fakePolicy{decision: policy.DecisionAllow} // spawn-plan allow; spawn-worker allow

	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithAuditSink(sink),
		orchestrator.WithRequireApproval(false),
	)
	goal := supervisor.Task{ID: "g1", Spec: "coding-agent: implement X\ndocs-fix: update Y"}

	if _, err := o.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: unexpected error: %v", err)
	}

	events := sink.Events()
	counts := map[audit.AuditAction]int{}
	for _, ev := range events {
		counts[ev.Action]++
	}
	// One chain covering orchestrator + both worker tiers.
	want := map[audit.AuditAction]int{
		audit.ActionGoalIntake:   1,
		audit.ActionPlanDecided:  1,
		audit.ActionSpawnDecided: 2,
		audit.ActionContainment:  2,
		audit.ActionFinish:       2,
		audit.ActionCompletion:   1,
	}
	for action, n := range want {
		if counts[action] != n {
			t.Errorf("chain action %q count = %d, want %d (events: %v)", action, counts[action], n, actionList(events))
		}
	}
	if len(events) < 9 {
		t.Errorf("fleet chain has %d events, want >= 9 in one chain", len(events))
	}

	// Ordering: each orchestrator spawn-decided precedes that worker's containment.
	firstSpawn := indexOfAction(events, audit.ActionSpawnDecided)
	firstContainment := indexOfAction(events, audit.ActionContainment)
	if firstSpawn < 0 || firstContainment < 0 || firstSpawn > firstContainment {
		t.Errorf("ordering: first spawn-decided idx=%d must precede first containment idx=%d", firstSpawn, firstContainment)
	}
	// goal-intake is first, completion is last.
	if events[0].Action != audit.ActionGoalIntake {
		t.Errorf("first event = %q, want goal-intake", events[0].Action)
	}
	if events[len(events)-1].Action != audit.ActionCompletion {
		t.Errorf("last event = %q, want completion", events[len(events)-1].Action)
	}
	t.Logf("TC-085-03 L2: one fleet chain, %d events: %v", len(events), actionList(events))

	// --- L5: replay the same sequence through the real audit-trail binary ---
	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		t.Log("TC-085-03 L5 binary-deferred: AGENT_BUILDER_AUDIT_BIN unset; FakeSink single-chain coverage asserted at L2")
		return
	}
	logfile := filepath.Join(t.TempDir(), "fleet-085-l5.log")
	block := audit.NewBlockSink(binPath, logfile)
	for _, ev := range events {
		if err := block.Append(ev); err != nil {
			t.Fatalf("BlockSink.Append(%s): %v", ev.Action, err)
		}
	}
	if err := block.Seal(); err != nil {
		t.Fatalf("BlockSink.Seal: %v", err)
	}
	res, err := audit.VerifyChain(binPath, logfile)
	if err != nil {
		t.Fatalf("VerifyChain (fleet chain): %v", err)
	}
	if !res.Valid {
		t.Fatalf("VerifyChain: Valid=false, want true; message=%q", res.Message)
	}
	t.Logf("TC-085-03 L5: audit-trail verify → valid=%v on the fleet chain", res.Valid)
}

func actionList(events []audit.AuditEvent) []audit.AuditAction {
	out := make([]audit.AuditAction, len(events))
	for i, ev := range events {
		out[i] = ev.Action
	}
	return out
}

func indexOfAction(events []audit.AuditEvent, a audit.AuditAction) int {
	for i, ev := range events {
		if ev.Action == a {
			return i
		}
	}
	return -1
}

// --- TC-085-05 — self-repo bright line (runtime deny + static detector) --------

// TestTC085_05_RuntimeSelfRepoWithRealPlanner tests the PRODUCTION PATH: the real
// StructuredPlanner populates Task.Repo from the goal, and the self-repo guard must
// deny a dispatch when that repo is the own-repo. This test catches SEC-001: if the
// guard only checked SubGoal.TargetRepo/Sink (never populated by StructuredPlanner),
// the production path would be unguarded.
func TestTC085_05_RuntimeSelfRepoWithRealPlanner(t *testing.T) {
	pol := &recordingPolicy{}
	spy := newDispatchSpy()
	rep := &fakeReporter{}
	sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
	o := orchestrator.New(
		orchestrator.NewStructuredPlanner(knownRecipes...),
		pol, rep, runtime.Config{},
		orchestrator.WithDispatchFunc(spy.fn),
		orchestrator.WithAuditSink(sink),
		orchestrator.WithRequireApproval(false),
	)

	// Goal with Repo == own-repo (as the inbound channel would provide it).
	goal := supervisor.Task{
		ID:   "g1",
		Repo: orchestrator.OwnRepo, // github.com/tkdtaylor/agent-builder
		Spec: "coding-agent: edit ourselves",
	}

	result, err := o.Handle(context.Background(), goal)
	if err != nil {
		t.Fatalf("Handle with own-repo goal: unexpected error: %v", err)
	}

	// The worker MUST NOT be dispatched (SEC-001 fix).
	if spy.count() != 0 {
		t.Fatalf("own-repo goal via real planner: want 0 dispatches, got %d", spy.count())
	}

	// The outcome must be a denied failure.
	if len(result.Outcomes) < 1 || result.Outcomes[0].Success {
		t.Fatalf("want denied outcome, got: %+v", result.Outcomes)
	}

	// The denial must mention self-repo, not policy.
	if !strings.Contains(result.Outcomes[0].Detail, "self-repo") {
		t.Errorf("own-repo denial detail = %q, want it to mention self-repo", result.Outcomes[0].Detail)
	}

	t.Log("TC-085-05 SEC-001 real-planner: own-repo goal → 0 dispatches, denied outcome")
}

// TestTC085_05_CanonicalizeRepoVariants tests the canonicalizer (SEC-002) against
// all repo path formats, proving the guard catches every variant of the own-repo.
func TestTC085_05_CanonicalizeRepoVariants(t *testing.T) {
	canonical := orchestrator.CanonicalizeRepo(orchestrator.OwnRepo)
	if canonical != "github.com/tkdtaylor/agent-builder" {
		t.Errorf("CanonicalizeRepo(OwnRepo) = %q, want %q", canonical, orchestrator.OwnRepo)
	}

	cases := []struct {
		name     string
		repo     string
		wantDeny bool // true if this should be considered the own-repo
	}{
		// Baseline cases
		{"exact", "github.com/tkdtaylor/agent-builder", true},
		{"https scheme", "https://github.com/tkdtaylor/agent-builder", true},
		{"http scheme", "http://github.com/tkdtaylor/agent-builder", true},
		{"ssh scheme", "ssh://github.com/tkdtaylor/agent-builder", true},
		{"git scheme", "git://github.com/tkdtaylor/agent-builder", true},
		{"git@scp", "git@github.com:tkdtaylor/agent-builder", true},
		{"git@ with slash", "git@github.com/tkdtaylor/agent-builder", true},
		{".git suffix", "github.com/tkdtaylor/agent-builder.git", true},
		{"trailing slash", "github.com/tkdtaylor/agent-builder/", true},
		{"uppercase", "GITHUB.COM/TKDTAYLOR/AGENT-BUILDER", true},
		{"mixed case", "GitHub.com/TkdTaylor/Agent-Builder", true},
		{"combination", "https://git@github.com/TkdTaylor/Agent-Builder.git/", true},
		// SEC-002 evasion cases — all should now be DENIED (fixed by net/url + order)
		{"HTTPS uppercase scheme", "HTTPS://github.com/tkdtaylor/agent-builder", true},
		{"Https mixed case scheme", "Https://github.com/tkdtaylor/agent-builder", true},
		{".GIT uppercase suffix", "github.com/tkdtaylor/agent-builder.GIT", true},
		{"leading space", "  github.com/tkdtaylor/agent-builder", true},
		{"trailing space", "github.com/tkdtaylor/agent-builder  ", true},
		{"both spaces", "  github.com/tkdtaylor/agent-builder  ", true},
		{"ssh with port", "ssh://github.com:22/tkdtaylor/agent-builder", true},
		{"https with port", "https://github.com:443/tkdtaylor/agent-builder", true},
		{"userinfo in URL", "https://user:pass@github.com/tkdtaylor/agent-builder", true},
		{"scp with port", "ssh://git@github.com:22/tkdtaylor/agent-builder", true},
		{"git@scp no .git", "git@github.com:tkdtaylor/agent-builder", true},
		{"trailing dot host", "github.com./tkdtaylor/agent-builder", true},
		{"backslashes", "github.com\\tkdtaylor\\agent-builder", true},
		{"protocol-relative", "//github.com/tkdtaylor/agent-builder", true},
		// Near-misses and other repos — must NOT be denied
		{"near-miss (evil)", "github.com/tkdtaylor/agent-builder-evil", false},
		{"evil HTTPS", "HTTPS://github.com/tkdtaylor/agent-builder-evil", false},
		{"evil with space", "  github.com/tkdtaylor/agent-builder-evil  ", false},
		{"other owner", "github.com/other/agent-builder", false},
		{"other repo", "github.com/tkdtaylor/other-repo", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := orchestrator.CanonicalizeRepo(tc.repo)
			isDeny := got == canonical && tc.repo != ""
			if isDeny != tc.wantDeny {
				t.Errorf("canonicalize(%q) = %q; isDeny = %v, want %v", tc.repo, got, isDeny, tc.wantDeny)
			}
		})
	}
}

func TestTC085_05_RuntimeSelfRepoDeny(t *testing.T) {
	// Policy allows EVERYTHING — proving the self-repo guard is independent of the
	// policy file (fail-closed by construction).
	pol := &recordingPolicy{}

	t.Run("target_repo own-repo denied", func(t *testing.T) {
		spy := newDispatchSpy()
		rep := &fakeReporter{}
		sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
		o := orchestrator.New(
			orchestrator.NewStructuredPlanner(knownRecipes...),
			pol, rep, runtime.Config{},
			orchestrator.WithDispatchFunc(spy.fn),
			orchestrator.WithAuditSink(sink),
			staticPlan(orchestrator.Plan{
				Goal: "g", GoalID: "g",
				SubGoals: []orchestrator.SubGoal{
					{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g-0", Spec: "self-repo edit"}, TargetRepo: orchestrator.OwnRepo},
				},
			}),
			orchestrator.WithRequireApproval(false),
		)
		result, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"})
		if err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if spy.count() != 0 {
			t.Fatalf("self-repo (target_repo): want 0 dispatches, got %d", spy.count())
		}
		if result.Outcomes[0].Success {
			t.Error("self-repo outcome should be a failure (denied)")
		}
		if !strings.Contains(result.Outcomes[0].Detail, "self-repo") {
			t.Errorf("self-repo deny detail = %q, want it to mention self-repo", result.Outcomes[0].Detail)
		}
	})

	t.Run("sink own-repo denied", func(t *testing.T) {
		spy := newDispatchSpy()
		rep := &fakeReporter{}
		sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
		o := orchestrator.New(
			orchestrator.NewStructuredPlanner(knownRecipes...),
			pol, rep, runtime.Config{},
			orchestrator.WithDispatchFunc(spy.fn),
			orchestrator.WithAuditSink(sink),
			staticPlan(orchestrator.Plan{
				Goal: "g", GoalID: "g",
				SubGoals: []orchestrator.SubGoal{
					{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g-0", Spec: "self-repo sink"}, Sink: orchestrator.OwnRepo},
				},
			}),
			orchestrator.WithRequireApproval(false),
		)
		_, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"})
		if err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if spy.count() != 0 {
			t.Fatalf("self-repo (sink): want 0 dispatches, got %d", spy.count())
		}
	})

	t.Run("non-own-repo target IS dispatched", func(t *testing.T) {
		spy := newDispatchSpy()
		rep := &fakeReporter{}
		sink := audit.NewFakeSink() // SEC-003: deny events require an audit sink
		o := orchestrator.New(
			orchestrator.NewStructuredPlanner(knownRecipes...),
			pol, rep, runtime.Config{},
			orchestrator.WithDispatchFunc(spy.fn),
			orchestrator.WithAuditSink(sink),
			staticPlan(orchestrator.Plan{
				Goal: "g", GoalID: "g",
				SubGoals: []orchestrator.SubGoal{
					{RecipeName: "coding-agent", Task: supervisor.Task{ID: "g-0", Spec: "other repo"}, TargetRepo: "github.com/tkdtaylor/some-other-repo"},
				},
			}),
			orchestrator.WithRequireApproval(false),
		)
		if _, err := o.Handle(context.Background(), supervisor.Task{ID: "g", Spec: "x"}); err != nil {
			t.Fatalf("Handle: %v", err)
		}
		if spy.count() != 1 {
			t.Fatalf("non-own-repo target: want 1 dispatch (guard is targeted), got %d", spy.count())
		}
	})
}

// TestTC085_05_FitnessDetectorFiresOnViolation exercises the F-013 detection
// predicate (orchestrator.SelfRepoSinkViolation): it must return true on a recipe
// source declaring the own-repo as a sink, and false on clean source.
func TestTC085_05_FitnessDetectorFiresOnViolation(t *testing.T) {
	violation := `func sinkFactory() { remote := "github.com/tkdtaylor/agent-builder"; _ = remote }`
	if !orchestrator.SelfRepoSinkViolation(violation) {
		t.Errorf("SelfRepoSinkViolation(violation) = false, want true")
	}
	clean := `func sinkFactory() { remote := "github.com/tkdtaylor/some-target-repo"; _ = remote }`
	if orchestrator.SelfRepoSinkViolation(clean) {
		t.Errorf("SelfRepoSinkViolation(clean) = true, want false")
	}
	// Own-repo present but not as a sink (e.g. an import path) is not flagged.
	importLine := `import "github.com/tkdtaylor/agent-builder/internal/supervisor"`
	if orchestrator.SelfRepoSinkViolation(importLine) {
		t.Errorf("SelfRepoSinkViolation(import only) = true, want false")
	}
	t.Log("TC-085-05 detector: fires on own-repo sink, clean on non-own-repo, ignores imports")
}

// TestTC085_05_FitnessCheckFiresOnViolationFixture runs the real
// `make fitness-no-self-repo-sink` target against a violation fixture directory
// (asserting non-zero exit) and against a clean directory (asserting exit 0).
func TestTC085_05_FitnessCheckFiresOnViolationFixture(t *testing.T) {
	if _, err := exec.LookPath("make"); err != nil {
		t.Skip("make not on PATH; skipping fitness-check shell invocation")
	}
	repoRoot := moduleRoot(t)

	// Violation fixture: a .go file declaring the own-repo as a publish remote.
	vdir := t.TempDir()
	violation := "package fixture\n\nfunc sink() string {\n\tremote := \"github.com/tkdtaylor/agent-builder\"\n\treturn remote\n}\n"
	if err := os.WriteFile(filepath.Join(vdir, "bad_recipe.go"), []byte(violation), 0o644); err != nil {
		t.Fatalf("write violation fixture: %v", err)
	}
	cmd := exec.Command("make", "fitness-no-self-repo-sink", "SELF_REPO_SINK_DIR="+vdir) //nolint:gosec // fixed args
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("fitness-no-self-repo-sink on violation fixture: want non-zero exit, got success; output:\n%s", out)
	}
	if !strings.Contains(string(out), "FAIL fitness-no-self-repo-sink") {
		t.Errorf("violation output missing FAIL marker:\n%s", out)
	}
	t.Logf("TC-085-05 fitness fires on violation: exit non-zero, output contains FAIL")

	// Clean fixture directory: no own-repo sink → exit 0.
	cdir := t.TempDir()
	clean := "package fixture\n\nfunc sink() string { return \"github.com/tkdtaylor/some-other-repo\" }\n"
	if err := os.WriteFile(filepath.Join(cdir, "ok_recipe.go"), []byte(clean), 0o644); err != nil {
		t.Fatalf("write clean fixture: %v", err)
	}
	cmd2 := exec.Command("make", "fitness-no-self-repo-sink", "SELF_REPO_SINK_DIR="+cdir) //nolint:gosec // fixed args
	cmd2.Dir = repoRoot
	out2, err2 := cmd2.CombinedOutput()
	if err2 != nil {
		t.Fatalf("fitness-no-self-repo-sink on clean fixture: want exit 0, got %v; output:\n%s", err2, out2)
	}
	if !strings.Contains(string(out2), "PASS fitness-no-self-repo-sink") {
		t.Errorf("clean output missing PASS marker:\n%s", out2)
	}
	t.Log("TC-085-05 fitness passes on clean fixture")
}

// moduleRoot walks up from the test's working directory to the directory holding go.mod.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find go.mod walking up from test dir")
		}
		dir = parent
	}
}

// --- helpers for the static-plan injection -------------------------------------

// fixedPlanner returns a fixed Plan from Plan(), ignoring the goal. It lets the
// self-repo tests inject sub-goals carrying TargetRepo/Sink (the StructuredPlanner
// does not parse those from goal text).
type fixedPlanner struct{ plan orchestrator.Plan }

func (p fixedPlanner) Plan(_ supervisor.Task) (orchestrator.Plan, error) { return p.plan, nil }

func staticPlan(p orchestrator.Plan) orchestrator.Option {
	return orchestrator.WithPlanner(fixedPlanner{plan: p})
}
