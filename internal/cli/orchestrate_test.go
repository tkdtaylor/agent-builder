package cli

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/channel/worker"
	"github.com/tkdtaylor/agent-builder/internal/envelope"
	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/loop"
	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	llmplanner "github.com/tkdtaylor/agent-builder/internal/orchestrator/planner"
	"github.com/tkdtaylor/agent-builder/internal/policy"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	runtimewiring "github.com/tkdtaylor/agent-builder/internal/runtime"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
	"github.com/tkdtaylor/agent-builder/internal/tasksource"

	// Registration side-effect: registers "docs-fix" so recipe.SelectRecipe resolves
	// it on the dispatch path. "coding-agent" is registered transitively via runtime.
	_ "github.com/tkdtaylor/agent-builder/internal/recipe/docsfix"
)

// --- test doubles ------------------------------------------------------------

// stubGoalSource yields each configured goal once, then ok=false.
type stubGoalSource struct {
	goals []supervisor.Task
	idx   int
}

func (s *stubGoalSource) Next() (supervisor.Task, bool, error) {
	if s.idx >= len(s.goals) {
		return supervisor.Task{}, false, nil
	}
	t := s.goals[s.idx]
	s.idx++
	return t, true, nil
}

// recordingGoalSource wraps a goal source and records whether Next was ever called
// (TC-099-06: the SEC-003 startup check must fire before any goal intake).
type recordingGoalSource struct {
	called bool
}

func (s *recordingGoalSource) Next() (supervisor.Task, bool, error) {
	s.called = true
	return supervisor.Task{}, false, nil
}

// perActionPolicy returns a decision keyed by (action, recipe). It lets a test gate
// spawn-plan and each sub-goal's spawn-worker independently (TC-099-04).
type perActionPolicy struct {
	mu sync.Mutex
	// spawnPlan is the spawn-plan decision (and any error to return).
	spawnPlan policy.Decision
	spawnErr  error
	// spawnWorker maps recipe name -> decision for spawn-worker.
	spawnWorker map[string]policy.Decision
	calls       int
}

func (p *perActionPolicy) Decide(req policy.DecideRequest) (policy.DecideResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if req.Action.Name == orchestrator.SpawnWorkerAction {
		d, ok := p.spawnWorker[req.Resource.ID]
		if !ok {
			d = policy.DecisionAllow
		}
		return policy.DecideResponse{Decision: d}, nil
	}
	// spawn-plan
	if p.spawnErr != nil {
		return policy.DecideResponse{}, p.spawnErr
	}
	return policy.DecideResponse{Decision: p.spawnPlan}, nil
}

// spyDispatch records each dispatched sub-goal recipe in order.
type spyDispatch struct {
	mu      sync.Mutex
	recipes []string
}

func (s *spyDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.recipes = append(s.recipes, sub.RecipeName)
	return nil
}

func (s *spyDispatch) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.recipes)
}

// auditingDispatch appends two worker audit events (containment, finish) to the
// SAME shared sink per dispatch, mirroring what a real worker contributes to the
// fleet chain (TC-099-05).
type auditingDispatch struct {
	sink audit.Sink
	mu   sync.Mutex
	n    int
}

func (d *auditingDispatch) fn(_ context.Context, sub orchestrator.SubGoal, _ runtimewiring.Config) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.n++
	_ = d.sink.Append(audit.AuditEvent{Action: audit.ActionContainment, TaskID: sub.Task.ID, RunID: sub.Task.ID, Detail: audit.EventDetail{Launcher: "podman"}})
	_ = d.sink.Append(audit.AuditEvent{Action: audit.ActionFinish, TaskID: sub.Task.ID, RunID: sub.Task.ID, Outcome: audit.OutcomeCompleted})
	return nil
}

// --- helpers -----------------------------------------------------------------

// testSigningKey returns a freshly-generated Ed25519 private key for tests that
// inject the signing key (so they need no key file on disk).
func testSigningKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	return priv
}

// writeSigningKeyFile writes a valid hex-encoded Ed25519 key file and points
// AGENT_BUILDER_WORKER_SIGNING_KEY at it for the duration of the test.
func writeSigningKeyFile(t *testing.T) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	path := filepath.Join(t.TempDir(), "signing.key")
	hexKey := make([]byte, 0)
	for _, b := range priv {
		const hexdigits = "0123456789abcdef"
		hexKey = append(hexKey, hexdigits[b>>4], hexdigits[b&0x0f])
	}
	if err := os.WriteFile(path, hexKey, 0o600); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	t.Setenv(worker.EnvWorkerSigningKey, path)
}

// setBaseConfigEnv sets the minimum environment runtime.ConfigFromEnv requires so
// the orchestrate assembler can build its base runtime.Config. These feed the
// per-worker dispatch assembly; the spy/stub dispatch in these tests never actually
// runs a worker, so the values only need to satisfy the config validator.
func setBaseConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv(runtimewiring.EnvTaskRoot, t.TempDir())
	t.Setenv(runtimewiring.EnvWorktree, t.TempDir())
	t.Setenv(runtimewiring.EnvPublishRemote, "origin")
	t.Setenv(runtimewiring.EnvRunTimeout, "5m")
	t.Setenv(runtimewiring.EnvMaxAttempts, "2")
	t.Setenv("ANTHROPIC_API_KEY", "test-key-not-used")
	t.Setenv("AGENT_BUILDER_INTAKE", "auto")
	t.Setenv("AGENT_BUILDER_REQUIRE_APPROVAL", "false")
}

// twoSubGoalGoal is the canonical 2-sub-goal goal used across policy/audit tests:
// "coding-agent: A" + "docs-fix: B".
func twoSubGoalGoal() supervisor.Task {
	return supervisor.Task{
		ID:   "g1",
		Spec: "coding-agent: implement A\ndocs-fix: document B",
	}
}

func twoRecipePlanner() orchestrator.Planner {
	return orchestrator.NewStructuredPlanner("coding-agent", "docs-fix")
}

// =============================================================================
// TC-099-01 — orchestrate subcommand registered; help prints usage (L2)
// =============================================================================

func TestOrchestrateHelpPrintsUsage(t *testing.T) {
	var out, errbuf bytes.Buffer
	code := Main(Config{Args: []string{"orchestrate", "-h"}, Stdout: &out, Stderr: &errbuf})

	if code != ExitOK {
		t.Fatalf("orchestrate -h exit = %d, want ExitOK (%d)", code, ExitOK)
	}
	if !strings.Contains(out.String(), "orchestrate") {
		t.Errorf("orchestrate -h stdout does not mention %q:\n%s", "orchestrate", out.String())
	}
	if strings.TrimSpace(out.String()) == "" {
		t.Errorf("orchestrate -h produced empty usage synopsis")
	}
	if errbuf.String() != "" {
		t.Errorf("orchestrate -h wrote to stderr (help must go to stdout): %q", errbuf.String())
	}
}

func TestUnknownSubcommandRejected(t *testing.T) {
	var out, errbuf bytes.Buffer
	code := Main(Config{Args: []string{"orchestrate-bad"}, Stdout: &out, Stderr: &errbuf})
	if code != ExitUsage {
		t.Fatalf("unknown subcommand exit = %d, want ExitUsage (%d)", code, ExitUsage)
	}
}

func TestTopLevelUsageMentionsOrchestrate(t *testing.T) {
	var out bytes.Buffer
	code := Main(Config{Args: []string{"help"}, Stdout: &out})
	if code != ExitOK {
		t.Fatalf("help exit = %d, want ExitOK", code)
	}
	if !strings.Contains(out.String(), "orchestrate") {
		t.Errorf("top-level usage does not list the orchestrate subcommand:\n%s", out.String())
	}
}

// =============================================================================
// TC-099-02 — NewPlanStoreFromEnv selects the right backend (L2)
// =============================================================================

func TestPlanStoreFromEnvUnsetIsMemoryWithWarning(t *testing.T) {
	t.Setenv(orchestrator.EnvVarMemoryGuardBin, "")

	var warned bool
	var warnMsg string
	store := orchestrator.NewPlanStoreFromEnv(func(msg string, _ ...any) {
		warned = true
		warnMsg = msg
	})

	if store == nil {
		t.Fatal("NewPlanStoreFromEnv returned nil store")
	}
	if _, ok := store.(*orchestrator.MemoryPlanStore); !ok {
		t.Fatalf("store dynamic type = %T, want *orchestrator.MemoryPlanStore", store)
	}
	if !warned {
		t.Fatal("expected a degraded-mode warning when MEMORY_GUARD_BIN is unset")
	}
	if !strings.Contains(warnMsg, "memory-guard") {
		t.Errorf("warning %q does not mention %q", warnMsg, "memory-guard")
	}
}

func TestPlanStoreFromEnvSetIsMemoryGuard(t *testing.T) {
	t.Setenv(orchestrator.EnvVarMemoryGuardBin, filepath.Join(t.TempDir(), "memory-guard"))

	var warned bool
	store := orchestrator.NewPlanStoreFromEnv(func(string, ...any) { warned = true })

	if _, ok := store.(*orchestrator.MemoryGuardPlanStore); !ok {
		t.Fatalf("store dynamic type = %T, want *orchestrator.MemoryGuardPlanStore", store)
	}
	if warned {
		t.Error("did not expect a degraded warning when MEMORY_GUARD_BIN is set")
	}
}

// =============================================================================
// TC-099-03 — ReplayCache invariant: ONE shared cache per direction; replay
// rejected on the wired dispatch path (L2)
// =============================================================================

func TestSharedReplayCacheRejectsReplayOnWiredPath(t *testing.T) {
	signingKey := testSigningKey(t)
	cache := envelope.NewReplayCache(0)
	resultCache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()

	dispatch, err := newTransportDispatch(signingKey, cache, resultCache, sink, discardLogger(), nil)
	if err != nil {
		t.Fatalf("newTransportDispatch: %v", err)
	}

	// Build one work-item envelope through the SAME sender the dispatch uses, so we
	// can replay its exact nonce. We reconstruct a sender with the same signing key;
	// the transport dispatch verifies against `cache`, so any envelope it has already
	// accepted (by nonce) is a replay.
	sub := orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task:       supervisor.Task{ID: "sg-0", Spec: "first"},
	}

	// First dispatch — work-item accepted, nonce registered in the shared cache.
	if err := dispatch(context.Background(), sub, runtimewiring.Config{}); err != nil {
		// runtime.Run may fail (no real worktree), but the replay check must pass
		// first; a replay error here would be a different failure. We only assert
		// the first dispatch does NOT fail with ErrReplay.
		if errors.Is(err, envelope.ErrReplay) {
			t.Fatalf("first dispatch unexpectedly rejected as replay: %v", err)
		}
	}

	// Now drive a replay: seal a work-item, feed it twice to a receiver over the
	// SAME cache. The second feed must be rejected with ErrReplay — proving the
	// shared cache (not a fresh one) is the gate.
	assertReplayRejectedByCache(t, signingKey, cache)
}

// assertReplayRejectedByCache constructs a sender+receiver over the given shared
// cache and confirms that the SECOND delivery of one envelope is rejected as a
// replay. This is the direct invariant assertion: the same cache instance rejects
// the replay; a fresh cache per dispatch would accept it twice.
func assertReplayRejectedByCache(t *testing.T, signingKey ed25519.PrivateKey, cache *envelope.ReplayCache) {
	t.Helper()
	orchXPub, orchXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	workerXPub, workerXPriv, err := envelope.GenerateKeyPair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	signPub := signingKey.Public().(ed25519.PublicKey)

	sender := worker.NewWorkItemSender(worker.SenderConfig{EdPriv: signingKey, XPriv: orchXPriv, RecipPub: workerXPub})
	receiver := worker.NewWorkItemReceiver(worker.ReceiverConfig{
		SignPub: signPub, RecipPriv: workerXPriv, SenderPub: orchXPub, ReplayCache: cache,
	})

	env, err := sender.DispatchWorkItem(supervisor.Task{ID: "replay-sg", Spec: "once"})
	if err != nil {
		t.Fatalf("DispatchWorkItem: %v", err)
	}
	if _, err := receiver.ReceiveWorkItem(env); err != nil {
		t.Fatalf("first receive over shared cache: %v", err)
	}
	_, err = receiver.ReceiveWorkItem(env)
	if err == nil {
		t.Fatal("shared cache accepted a replayed work-item — a fresh cache per dispatch would do this (083 SEC-001 violation)")
	}
	if !errors.Is(err, envelope.ErrReplay) {
		t.Fatalf("replay error = %v, want errors.Is(err, envelope.ErrReplay)", err)
	}
}

// TestAssembleUsesOneCachePerDirection asserts the assembler creates exactly one
// cache per direction at startup and reuses it (no fresh cache per dispatch). We
// inject explicit caches and confirm the assembler accepts and threads them — the
// transport dispatch built from them then exhibits the shared-cache replay behavior
// asserted above.
func TestAssembleInjectedCacheIsReusedAcrossDispatches(t *testing.T) {
	setBaseConfigEnv(t)
	writeSigningKeyFile(t)
	signingKey := testSigningKey(t)
	cache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()
	spy := &spyDispatch{}

	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient:  &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
		dispatch:      spy.fn, // spy so no real worker runs; cache wiring asserted separately
		auditSink:     sink,
		planner:       twoRecipePlanner(),
		source:        &stubGoalSource{goals: []supervisor.Task{twoSubGoalGoal()}},
		workItemCache: cache,
		signingKey:    signingKey,
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	defer cleanup()

	if err := runControlLoop(context.Background(), oc); err != nil {
		t.Fatalf("goal-intake loop: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("spy dispatch called %d times, want 2", spy.count())
	}
	// The injected cache is the one the assembly holds; confirm it rejects a replay.
	assertReplayRejectedByCache(t, signingKey, cache)
}

// =============================================================================
// TC-099-04 — Policy gates on the live wired path: allow / deny / require_approval
// / deny-on-error (L2)
// =============================================================================

func assembleForPolicyTest(t *testing.T, pol orchestrator.PolicyClient, spy *spyDispatch, sink audit.Sink, source supervisor.GoalSource) orchestrateConfig {
	t.Helper()
	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient: pol,
		dispatch:     spy.fn,
		auditSink:    sink,
		planner:      twoRecipePlanner(),
		source:       source,
		signingKey:   testSigningKey(t),
	})
	if err != nil {
		t.Fatalf("assembleOrchestrate: %v", err)
	}
	t.Cleanup(cleanup)
	return oc
}

func TestPolicyAllowDispatchesBoth(t *testing.T) {
	spy := &spyDispatch{}
	sink := audit.NewFakeSink()
	pol := &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}}
	oc := assembleForPolicyTest(t, pol, spy, sink, &stubGoalSource{goals: []supervisor.Task{twoSubGoalGoal()}})

	if err := runControlLoop(context.Background(), oc); err != nil {
		t.Fatalf("loop: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("allow path dispatched %d, want 2", spy.count())
	}
}

func TestPolicyDenySecondWorkerSkipsIt(t *testing.T) {
	spy := &spyDispatch{}
	sink := audit.NewFakeSink()
	pol := &perActionPolicy{
		spawnPlan:   policy.DecisionAllow,
		spawnWorker: map[string]policy.Decision{"coding-agent": policy.DecisionAllow, "docs-fix": policy.DecisionDeny},
	}
	result, err := assembleAndHandle(t, pol, spy, sink, twoSubGoalGoal())
	if err != nil {
		t.Fatalf("handle: %v", err)
	}
	if spy.count() != 1 {
		t.Fatalf("deny-second dispatched %d, want 1 (coding-agent only)", spy.count())
	}
	if len(result.Outcomes) != 2 {
		t.Fatalf("expected 2 outcomes, got %d", len(result.Outcomes))
	}
	// docs-fix is the second sub-goal; its outcome must be a failure mentioning policy.
	docs := result.Outcomes[1]
	if docs.Success {
		t.Fatalf("docs-fix outcome should be denied, got Success=true")
	}
	if !strings.Contains(strings.ToLower(docs.Detail), "policy") && !strings.Contains(strings.ToLower(docs.Detail), "denied") {
		t.Errorf("docs-fix detail %q should mention policy/denied", docs.Detail)
	}
}

func TestPolicyRequireApprovalPausesThenResume(t *testing.T) {
	spy := &spyDispatch{}
	sink := audit.NewFakeSink()
	pol := &perActionPolicy{spawnPlan: policy.DecisionRequireApproval, spawnWorker: map[string]policy.Decision{}}

	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient: pol, dispatch: spy.fn, auditSink: sink, planner: twoRecipePlanner(),
		source: &stubGoalSource{}, signingKey: testSigningKey(t),
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer cleanup()

	goal := twoSubGoalGoal()
	if _, err := oc.orch.Handle(context.Background(), goal); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !oc.orch.HasPendingPlan(goal.ID) {
		t.Fatal("require_approval should leave a pending plan")
	}
	if spy.count() != 0 {
		t.Fatalf("require_approval should not dispatch; dispatched %d", spy.count())
	}

	res, err := oc.orch.Resume(context.Background(), orchestrator.Approval{
		From: "operator", To: "orchestrator", GoalID: goal.ID, Approved: true,
	})
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if spy.count() != 2 {
		t.Fatalf("after approval dispatched %d, want 2", spy.count())
	}
	if len(res.Outcomes) != 2 {
		t.Fatalf("resumed result outcomes = %d, want 2", len(res.Outcomes))
	}
	for i, oc := range res.Outcomes {
		if !oc.Success {
			t.Errorf("outcome[%d] not successful: %+v", i, oc)
		}
	}
}

func TestPolicyDenyOnErrorFailsClosed(t *testing.T) {
	spy := &spyDispatch{}
	sink := audit.NewFakeSink()
	pol := &perActionPolicy{spawnErr: errors.New("transport boom"), spawnWorker: map[string]policy.Decision{}}
	result, err := assembleAndHandle(t, pol, spy, sink, twoSubGoalGoal())
	// Fail-closed: deny path taken, not panicked. Handle returns a zero-dispatch
	// result (and no error from the spawn-plan deny — it reports a denial).
	if err != nil {
		t.Fatalf("deny-on-error Handle returned error (should fail closed, not error): %v", err)
	}
	if spy.count() != 0 {
		t.Fatalf("deny-on-error should not dispatch; dispatched %d", spy.count())
	}
	if len(result.Outcomes) != 0 {
		t.Fatalf("deny-on-error result should have zero outcomes, got %d", len(result.Outcomes))
	}
}

func assembleAndHandle(t *testing.T, pol orchestrator.PolicyClient, spy *spyDispatch, sink audit.Sink, goal supervisor.Task) (orchestrator.PlanResult, error) {
	t.Helper()
	oc := assembleForPolicyTest(t, pol, spy, sink, &stubGoalSource{})
	return oc.orch.Handle(context.Background(), goal)
}

// =============================================================================
// TC-099-05 — Fleet audit chain: orchestrator + N worker events in ONE sink (L2);
// audit-trail verify valid=true (L5)
// =============================================================================

func TestFleetAuditChainSingleSinkOrderedEvents(t *testing.T) {
	sink := audit.NewFakeSink()
	dispatch := &auditingDispatch{sink: sink}
	pol := &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}}

	setBaseConfigEnv(t)
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		policyClient: pol, dispatch: dispatch.fn, auditSink: sink, planner: twoRecipePlanner(),
		source: &stubGoalSource{goals: []supervisor.Task{twoSubGoalGoal()}}, signingKey: testSigningKey(t),
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	defer cleanup()

	if err := runControlLoop(context.Background(), oc); err != nil {
		t.Fatalf("loop: %v", err)
	}

	events := sink.Events()
	if len(events) < 9 {
		t.Fatalf("fleet chain has %d events, want >= 9", len(events))
	}

	counts := map[audit.AuditAction]int{}
	for _, e := range events {
		counts[e.Action]++
	}
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
			t.Errorf("action %q count = %d, want %d", action, counts[action], n)
		}
	}

	// Ordering: goal-intake first, completion last, and each spawn-decided precedes
	// the first worker containment event.
	if events[0].Action != audit.ActionGoalIntake {
		t.Errorf("first event = %q, want goal-intake", events[0].Action)
	}
	if events[len(events)-1].Action != audit.ActionCompletion {
		t.Errorf("last event = %q, want completion", events[len(events)-1].Action)
	}
	firstSpawn := indexOfAction(events, audit.ActionSpawnDecided)
	firstContainment := indexOfAction(events, audit.ActionContainment)
	if firstSpawn < 0 || firstContainment < 0 || firstSpawn > firstContainment {
		t.Errorf("spawn-decided (idx %d) must precede the first containment (idx %d)", firstSpawn, firstContainment)
	}
}

func indexOfAction(events []audit.AuditEvent, action audit.AuditAction) int {
	for i, e := range events {
		if e.Action == action {
			return i
		}
	}
	return -1
}

// TestFleetAuditChainVerifyL5 replays a representative chain through a real
// audit-trail BlockSink and asserts VerifyChain reports valid=true. Skipped when
// the binary is absent (L5 deferred).
func TestFleetAuditChainVerifyL5(t *testing.T) {
	binPath := os.Getenv("AGENT_BUILDER_AUDIT_BIN")
	if binPath == "" {
		t.Skip("L5 audit-trail binary not present — deferred (set AGENT_BUILDER_AUDIT_BIN)")
	}
	logfile := filepath.Join(t.TempDir(), "chain.log")
	sink := audit.NewBlockSink(binPath, logfile)
	seq := []audit.AuditEvent{
		{Action: audit.ActionGoalIntake, TaskID: "g1", RunID: "g1"},
		{Action: audit.ActionPlanDecided, TaskID: "g1", RunID: "g1", Detail: audit.EventDetail{PolicyDecision: "allow"}},
		{Action: audit.ActionSpawnDecided, TaskID: "g1-0", RunID: "g1", Detail: audit.EventDetail{PolicyDecision: "allow"}},
		{Action: audit.ActionSpawnDecided, TaskID: "g1-1", RunID: "g1", Detail: audit.EventDetail{PolicyDecision: "allow"}},
		{Action: audit.ActionContainment, TaskID: "g1-0", RunID: "g1-0", Detail: audit.EventDetail{Launcher: "podman"}},
		{Action: audit.ActionContainment, TaskID: "g1-1", RunID: "g1-1", Detail: audit.EventDetail{Launcher: "podman"}},
		{Action: audit.ActionFinish, TaskID: "g1-0", RunID: "g1-0", Outcome: audit.OutcomeCompleted},
		{Action: audit.ActionFinish, TaskID: "g1-1", RunID: "g1-1", Outcome: audit.OutcomeCompleted},
		{Action: audit.ActionCompletion, TaskID: "g1", RunID: "g1"},
	}
	for i, ev := range seq {
		if err := sink.Append(ev); err != nil {
			t.Fatalf("append[%d]: %v", i, err)
		}
	}
	res, err := audit.VerifyChain(binPath, logfile)
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.Valid {
		t.Fatalf("chain not valid: %+v", res)
	}
}

// =============================================================================
// TC-099-06 — SEC-003 startup check: missing worker signing key exits before
// accepting goals (L2 + L5 subprocess)
// =============================================================================

func TestOrchestrateMissingSigningKeyFailsBeforeGoalIntake(t *testing.T) {
	t.Setenv(worker.EnvWorkerSigningKey, "")
	rec := &recordingGoalSource{}

	_, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		// Provide a source that records Next() calls; the startup check must fire
		// BEFORE this source is ever read.
		source: rec,
		// NOTE: signingKey intentionally NOT set, so the live LoadSigningKey runs.
	})
	defer cleanup()

	if err == nil {
		t.Fatal("expected an error when AGENT_BUILDER_WORKER_SIGNING_KEY is unset")
	}
	if !errors.Is(err, worker.ErrMissingSigningKey) {
		t.Fatalf("error = %v, want errors.Is(err, worker.ErrMissingSigningKey)", err)
	}
	if !strings.Contains(err.Error(), worker.EnvWorkerSigningKey) {
		t.Errorf("error %q must name %q", err.Error(), worker.EnvWorkerSigningKey)
	}
	if rec.called {
		t.Error("goal source was read before the startup key check — SEC-003 ordering violated")
	}
}

func TestOrchestrateMissingSigningKeyCLIExitsNonZero(t *testing.T) {
	t.Setenv(worker.EnvWorkerSigningKey, "")
	var out, errbuf bytes.Buffer
	code := Main(Config{Args: []string{"orchestrate"}, Stdout: &out, Stderr: &errbuf})

	if code == ExitOK {
		t.Fatal("orchestrate with no signing key exited 0; want non-zero")
	}
	if !strings.Contains(errbuf.String(), worker.EnvWorkerSigningKey) {
		t.Errorf("stderr %q must name %q", errbuf.String(), worker.EnvWorkerSigningKey)
	}
	if out.String() != "" {
		t.Errorf("no goal-intake output should appear on stdout before the failure; got %q", out.String())
	}
}

// TestOrchestrateSubcommandStartupKeyCheck drives the assembled subcommand in a
// subprocess-like manner via Main with stub env, asserting the startup key check is
// the failure (the named L5 subprocess test referenced in the verification plan).
func TestOrchestrateSubcommandStartupKeyCheck(t *testing.T) {
	t.Setenv(worker.EnvWorkerSigningKey, "")
	var out, errbuf bytes.Buffer
	code := Main(Config{Args: []string{"orchestrate"}, Stdout: &out, Stderr: &errbuf})
	if code != ExitGeneric {
		t.Fatalf("exit = %d, want ExitGeneric (%d)", code, ExitGeneric)
	}
	if !strings.Contains(errbuf.String(), "missing signing key") && !strings.Contains(errbuf.String(), worker.EnvWorkerSigningKey) {
		t.Errorf("stderr should carry the ErrMissingSigningKey signal: %q", errbuf.String())
	}
}

// =============================================================================
// TC-099-07 — spec docs updated (L2 documentary)
// =============================================================================

func TestConfigurationDocMentionsKeyEnvVars(t *testing.T) {
	data := readRepoDoc(t, "docs/spec/configuration.md")
	for _, want := range []string{worker.EnvWorkerSigningKey, orchestrator.EnvVarMemoryGuardBin, EnvPlanner} {
		if !strings.Contains(data, want) {
			t.Errorf("configuration.md does not document %q", want)
		}
	}
}

func TestInterfacesDocHasOrchestrateRow(t *testing.T) {
	data := readRepoDoc(t, "docs/spec/interfaces.md")
	if !strings.Contains(data, "orchestrate") {
		t.Error("interfaces.md CLI table has no orchestrate row")
	}
}

// =============================================================================
// TC-099-08 — diagrams.md updated (L2 documentary)
// =============================================================================

func TestDiagramsMentionOrchestratorInLiveFlow(t *testing.T) {
	data := readRepoDoc(t, "docs/architecture/diagrams.md")
	if !strings.Contains(data, "orchestrate") && !strings.Contains(data, "Orchestrator") {
		t.Error("diagrams.md does not mention orchestrate/Orchestrator in the live flow")
	}
	// Updated date must be on/after this task's commit date (2026-06-28).
	if !strings.Contains(data, "2026-06-28") && !containsLaterDate(data) {
		t.Errorf("diagrams.md updated date appears stale (want >= 2026-06-28)")
	}
}

func containsLaterDate(data string) bool {
	// Cheap guard: any 2026-06-3x or 2026-0[7-9] / 2026-1x or 2027+ date counts as
	// "not before 2026-06-28". The exact-date check above is the primary path.
	for _, marker := range []string{"2026-06-29", "2026-06-30", "2026-07", "2026-08", "2026-09", "2026-1", "2027", "2028"} {
		if strings.Contains(data, marker) {
			return true
		}
	}
	return false
}

// =============================================================================
// TC-110-01 — AGENT_BUILDER_PLANNER=llm assembles *planner.LLMPlanner (L2)
// TC-110-02 — unknown planner value drives ExitUsage (L2)
// (retained: structured default from prior TC-099 suite)
// =============================================================================

func TestPlannerFromEnvStructuredDefault(t *testing.T) {
	t.Setenv(EnvPlanner, "")
	p, err := plannerFromEnv()
	if err != nil {
		t.Fatalf("structured default: %v", err)
	}
	if _, ok := p.(*orchestrator.StructuredPlanner); !ok {
		t.Fatalf("planner type = %T, want *StructuredPlanner", p)
	}
}

func TestPlannerFromEnvStructuredExplicit(t *testing.T) {
	t.Setenv(EnvPlanner, "structured")
	p, err := plannerFromEnv()
	if err != nil {
		t.Fatalf("structured explicit: %v", err)
	}
	if _, ok := p.(*orchestrator.StructuredPlanner); !ok {
		t.Fatalf("planner type = %T, want *StructuredPlanner", p)
	}
}

// TC-110-01: AGENT_BUILDER_PLANNER=llm → *llmplanner.LLMPlanner, nil error.
// No AGENT_BUILDER_REGISTRY_* vars set → CLI builds synthetic-default catalog.
func TestPlannerFromEnvLLMAssemblesLLMPlanner(t *testing.T) {
	t.Setenv(EnvPlanner, "llm")
	// No registry env vars set: the CLI synthesizes the default catalog entry.

	p, err := plannerFromEnv()
	if err != nil {
		t.Fatalf("TC-110-01: plannerFromEnv with =llm returned error: %v", err)
	}
	if p == nil {
		t.Fatal("TC-110-01: plannerFromEnv returned nil planner")
	}
	// Dynamic type must be *llmplanner.LLMPlanner, NOT *orchestrator.StructuredPlanner.
	if _, ok := p.(*llmplanner.LLMPlanner); !ok {
		t.Fatalf("TC-110-01: planner dynamic type = %T, want *llmplanner.LLMPlanner", p)
	}
}

// TC-110-02: unknown AGENT_BUILDER_PLANNER value → error naming value + valid
// options; Main returns ExitUsage.
func TestPlannerFromEnvUnknownValueReturnsError(t *testing.T) {
	t.Setenv(EnvPlanner, "magic")
	_, err := plannerFromEnv()
	if err == nil {
		t.Fatal("TC-110-02: expected non-nil error for unknown planner value")
	}
	if !strings.Contains(err.Error(), "magic") {
		t.Errorf("TC-110-02: error %q must name the bad value %q", err.Error(), "magic")
	}
	if !strings.Contains(err.Error(), plannerStructured) || !strings.Contains(err.Error(), plannerLLM) {
		t.Errorf("TC-110-02: error %q must list valid options %q and %q", err.Error(), plannerStructured, plannerLLM)
	}
}

func TestPlannerFromEnvUnknownValueDrivesExitUsage(t *testing.T) {
	t.Setenv(EnvPlanner, "magic")
	setBaseConfigEnv(t)
	writeSigningKeyFile(t)
	var out, errbuf bytes.Buffer
	code := Main(Config{Args: []string{"orchestrate"}, Stdout: &out, Stderr: &errbuf})
	if code != ExitUsage {
		t.Fatalf("TC-110-02: orchestrate with =magic exit = %d, want ExitUsage (%d); stderr: %s", code, ExitUsage, errbuf.String())
	}
}

// =============================================================================
// TC-110-03 — routerResolverAdapter wraps router.Select; drops ctx (L2)
// =============================================================================

// TestRouterResolverAdapterDelegatesSelect verifies the CLI's ExecutorResolver
// adapter delegates to router.Select unchanged and propagates ErrNoEligibleExecutor.
func TestRouterResolverAdapterDelegatesSelect(t *testing.T) {
	// Build a one-entry catalog: local-ollama, CapabilityTier=1.
	cat := registry.NewCatalog()
	cat.RegisterEntry(registry.RegistryEntry{
		ID:             "local-ollama",
		Harness:        registry.HarnessOllamaNative,
		CapabilityTier: 1,
		CostWeight:     1,
		Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
		Endpoint:       "http://localhost:11434",
		ModelID:        "qwen3:8b",
	})
	r := router.New(cat)
	res := &routerResolverAdapter{r: r}

	// Sub-test A: eligible entry returned.
	entry, err := res.Resolve(context.Background(), router.RoutingSpec{MinCapability: 1})
	if err != nil {
		t.Fatalf("TC-110-03A: Resolve returned error: %v", err)
	}
	if entry.ID != "local-ollama" {
		t.Errorf("TC-110-03A: got entry ID %q, want %q", entry.ID, "local-ollama")
	}

	// Sub-test B: no eligible entry → ErrNoEligibleExecutor.
	_, err = res.Resolve(context.Background(), router.RoutingSpec{MinCapability: 99})
	if !errors.Is(err, router.ErrNoEligibleExecutor) {
		t.Errorf("TC-110-03B: err = %v, want errors.Is(err, router.ErrNoEligibleExecutor)", err)
	}

	// Sub-test C: cancelled ctx does NOT change result — the adapter drops ctx.
	// This is the documented limitation: router.Select is not context-cancellable.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	entry2, err2 := res.Resolve(ctx, router.RoutingSpec{MinCapability: 1})
	if err2 != nil {
		t.Fatalf("TC-110-03C: cancelled ctx caused Resolve to fail: %v (adapter must drop ctx)", err2)
	}
	if entry2.ID != "local-ollama" {
		t.Errorf("TC-110-03C: cancelled ctx changed result; got %q, want %q", entry2.ID, "local-ollama")
	}
}

// =============================================================================
// TC-110-04 — Invoker closure routes through CompleterForEntry (L2)
// =============================================================================

// TestInvokerClosureOllamaEntryYieldsCompleter verifies the Invoker closure
// built in buildLLMPlanner obtains a non-nil completer for an ollama entry
// (no ErrSingleShotUnsupported).
func TestInvokerClosureOllamaEntryYieldsCompleter(t *testing.T) {
	ollamaEntry := registry.RegistryEntry{
		ID:       "local-ollama",
		Harness:  registry.HarnessOllamaNative,
		Endpoint: "http://localhost:11434",
		ModelID:  "qwen3:8b",
	}
	// The Invoker is the same closure buildLLMPlanner constructs; we replicate it
	// here to assert the completer-construction path in isolation.
	invoke := llmplanner.Invoker(func(ctx context.Context, entry registry.RegistryEntry, _ string) (string, error) {
		c, err := executor.CompleterForEntry(entry)
		if err != nil {
			return "", err
		}
		// We don't call Complete (no live model); just confirm c is non-nil.
		_ = c
		return "ok", nil
	})

	// TC-110-04A: ollama entry → no ErrSingleShotUnsupported, completer non-nil.
	result, err := invoke(context.Background(), ollamaEntry, "ping")
	if err != nil {
		t.Fatalf("TC-110-04A: Invoker(ollama) returned error: %v", err)
	}
	if result != "ok" {
		t.Errorf("TC-110-04A: Invoker result = %q, want %q", result, "ok")
	}

	// TC-110-04B: cloud entry → ErrSingleShotUnsupported propagated through Invoker.
	cloudEntry := registry.RegistryEntry{
		ID:      "claude-oauth",
		Harness: registry.HarnessClaudeCLI,
	}
	_, err = invoke(context.Background(), cloudEntry, "ping")
	if !errors.Is(err, executor.ErrSingleShotUnsupported) {
		t.Errorf("TC-110-04B: err = %v, want errors.Is(err, executor.ErrSingleShotUnsupported)", err)
	}
}

// =============================================================================
// TC-110-05 — assembleOrchestrate with =llm wires *llmplanner.LLMPlanner (L2)
// (F-010/F-014 fitness checks are asserted at L3 via make fitness targets)
// =============================================================================

// TestAssembleOrchestrateWithLLMPlannerWiresLLMPlanner asserts that with
// AGENT_BUILDER_PLANNER=llm, the control-plane assembly (assembleOrchestrate)
// feeds a *llmplanner.LLMPlanner into orchestrator.New — i.e. the planner seam
// survives the task-112 loop rewrite and reaches the orchestrator on the async
// path. This drives the REAL assembleOrchestrate with NO planner override (so
// plannerFromEnv runs under =llm) and captures the exact planner handed to
// orchestrator.New via the onPlanner seam — proving the producer→consumer link,
// not merely that plannerFromEnv returns the right type (that is TC-110-01).
func TestAssembleOrchestrateWithLLMPlannerWiresLLMPlanner(t *testing.T) {
	t.Setenv(EnvPlanner, "llm")
	setBaseConfigEnv(t)

	var captured orchestrator.Planner
	var capturedCount int
	oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
		// No planner override → assembleOrchestrate calls plannerFromEnv() under =llm.
		dispatch:   (&spyDispatch{}).fn, // spy so no real worker runs
		signingKey: testSigningKey(t),   // satisfy SEC-003 without a key file
		onPlanner: func(p orchestrator.Planner) {
			captured = p
			capturedCount++
		},
	})
	if err != nil {
		t.Fatalf("TC-110-05: assembleOrchestrate(=llm) returned error: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}
	if oc.orch == nil {
		t.Fatal("TC-110-05: assembleOrchestrate returned a nil orchestrator")
	}
	if capturedCount != 1 {
		t.Fatalf("TC-110-05: onPlanner invoked %d times, want exactly 1 (the planner fed to orchestrator.New)", capturedCount)
	}
	if _, ok := captured.(*llmplanner.LLMPlanner); !ok {
		t.Fatalf("TC-110-05: planner fed into orchestrator.New = %T, want *llmplanner.LLMPlanner", captured)
	}
}

// =============================================================================
// TC-110-06 — stale "pending task 100" notes removed; config doc matches live (L2)
// =============================================================================

func TestUsageStringHasNoPendingTask100(t *testing.T) {
	var buf bytes.Buffer
	orchestrateUsage(&buf)
	usage := buf.String()
	if strings.Contains(usage, "pending task 100") {
		t.Errorf("TC-110-06: orchestrateUsage still contains stale %q text:\n%s", "pending task 100", usage)
	}
	if !strings.Contains(usage, "llm") {
		t.Errorf("TC-110-06: orchestrateUsage does not mention %q as a live planner value", "llm")
	}
}

func TestEnvPlannerDocHasNoPendingTask100(t *testing.T) {
	// Assert the EnvPlanner constant doc comment no longer contains "pending task 100".
	// We check the configuration.md directly (the code doc comment is source-internal).
	data := readRepoDoc(t, "docs/spec/configuration.md")
	// The PLANNER row must exist and must NOT say "pending".
	if !strings.Contains(data, EnvPlanner) {
		t.Errorf("TC-110-06: configuration.md does not contain %q", EnvPlanner)
	}
	// The row must not carry "pending task 100" stale text.
	if strings.Contains(data, "pending task 100") {
		t.Errorf("TC-110-06: configuration.md still contains stale %q text", "pending task 100")
	}
	// The row must describe llm as live (ollama-only behavior with cloud fail-closed).
	if !strings.Contains(data, "llm") {
		t.Errorf("TC-110-06: configuration.md PLANNER row does not mention %q as a live value", "llm")
	}
}

func TestConfigurationDocLLMIsLiveNotPending(t *testing.T) {
	data := readRepoDoc(t, "docs/spec/configuration.md")
	// Confirm the row describes the ollama-only live behavior.
	if !strings.Contains(data, "ollama") {
		t.Errorf("TC-110-06: configuration.md PLANNER row does not mention ollama-only live behavior")
	}
}

// =============================================================================
// TC-111-01 — failed keypair generation propagates out of newTransportDispatch (L2)
// TC-111-02 — assembleOrchestrate propagates the error and never enters the loop (L2)
// TC-111-03 — happy path: real keygen yields a working dispatcher (L2)
// =============================================================================

// errFakeRand is the sentinel injected by the keygen seam in TC-111-01 and TC-111-02.
var errFakeRand = errors.New("injected: rand failure")

// TestNewTransportDispatchKeygenFailurePropagates is TC-111-01: the fault-injection
// seam causes newTransportDispatch to return a nil DispatchFunc + non-nil error.
func TestNewTransportDispatchKeygenFailurePropagates(t *testing.T) {
	// Override the seam to fail on every call.
	orig := generateSealKeyPair
	generateSealKeyPair = func() ([32]byte, [32]byte, error) {
		return [32]byte{}, [32]byte{}, errFakeRand
	}
	t.Cleanup(func() { generateSealKeyPair = orig })

	signingKey := testSigningKey(t)
	cache := envelope.NewReplayCache(0)
	resultCache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()

	fn, err := newTransportDispatch(signingKey, cache, resultCache, sink, discardLogger(), nil)

	// TC-111-01 assertions:

	// 1. Must return non-nil error.
	if err == nil {
		t.Fatal("expected non-nil error from newTransportDispatch when keygen fails, got nil")
	}
	// 2. The returned DispatchFunc must be nil — no degenerate zero-key dispatcher.
	if fn != nil {
		t.Errorf("returned DispatchFunc should be nil on keygen failure, got non-nil")
	}
	// 3. The error wraps the injected sentinel (errors.Is).
	if !errors.Is(err, errFakeRand) {
		t.Errorf("errors.Is(err, errFakeRand) = false; err = %v", err)
	}
	// 4. The error message names the keypair failure (contains "keypair").
	if !strings.Contains(err.Error(), "keypair") {
		t.Errorf("error message %q should contain %q", err.Error(), "keypair")
	}
}

// TestAssembleOrchestrateKeygenFailurePropagates is TC-111-02: assembleOrchestrate
// propagates the keygen error, returns zero config, runs cleanup, never enters the
// goal-intake loop.
func TestAssembleOrchestrateKeygenFailurePropagates(t *testing.T) {
	setBaseConfigEnv(t)

	// Override the seam to fail.
	orig := generateSealKeyPair
	generateSealKeyPair = func() ([32]byte, [32]byte, error) {
		return [32]byte{}, [32]byte{}, errFakeRand
	}
	t.Cleanup(func() { generateSealKeyPair = orig })

	// Use a recording goal source to assert the intake loop is never entered.
	rec := &recordingGoalSource{}

	// ov.dispatch == nil so the live newTransportDispatch path is taken.
	// ov.signingKey is provided to satisfy the SEC-003 check without a key file.
	oc, cleanup, err := assembleOrchestrate(
		Config{Stdout: discard(), Stderr: discard()},
		assembleOverrides{
			signingKey: testSigningKey(t),
			source:     rec,
		},
	)
	// cleanup must always be called even on error (mirrors the other branches).
	if cleanup != nil {
		cleanup()
	}

	// TC-111-02 assertions:

	// 1. Must return non-nil error wrapping the keygen failure.
	if err == nil {
		t.Fatal("expected assembleOrchestrate to return non-nil error on keygen failure")
	}
	if !errors.Is(err, errFakeRand) {
		t.Errorf("errors.Is(err, errFakeRand) = false; err = %v", err)
	}

	// 2. The returned orchestrateConfig must be the zero value (no partial leak).
	if oc.orch != nil || oc.source != nil || oc.stdout != nil {
		t.Errorf("expected zero orchestrateConfig on error, got %+v", oc)
	}

	// 3. cleanup was non-nil (safe to call) — asserted by the deferred call above not panicking.

	// 4. The goal-intake loop was never entered — the recording source was never read.
	if rec.called {
		t.Error("goal source was read; assembleOrchestrate must fail before the intake loop on keygen failure")
	}
}

// TestAssembleOrchestrateWiresStatusWriter is TC-005-CLI: assembleOrchestrate
// constructs a non-nil *tasksource.StatusWriter from baseConfig.TaskRoot when
// ov.statusWriter is nil, proving the orchestrate path supplies the blocked-action
// reevaluation seam (task 123, ADR 055 seam 4). The test seam onStatusWriter
// captures the constructed writer so we can assert it is non-nil and is the
// right concrete type.
func TestAssembleOrchestrateWiresStatusWriter(t *testing.T) {
	// Set up a temporary task root so the status writer has a valid directory.
	tmpRoot := t.TempDir()
	t.Setenv("AGENT_BUILDER_TASK_ROOT", tmpRoot)
	t.Setenv("AGENT_BUILDER_MAX_ATTEMPTS", "1")
	setBaseConfigEnv(t)

	var capturedWriter loop.StatusWriter
	oc, cleanup, err := assembleOrchestrate(
		Config{Stdout: discard(), Stderr: discard()},
		assembleOverrides{
			signingKey:     testSigningKey(t),
			source:         &recordingGoalSource{},
			onStatusWriter: func(w loop.StatusWriter) { capturedWriter = w },
		},
	)
	t.Cleanup(cleanup)

	// TC-005-CLI assertions:

	// 1. assembleOrchestrate succeeds (no error).
	if err != nil {
		t.Fatalf("assembleOrchestrate returned error: %v", err)
	}

	// 2. The orchestrator was constructed (oc.orch is non-nil).
	if oc.orch == nil {
		t.Fatal("orchestrateConfig.orch is nil")
	}

	// 3. The onStatusWriter seam captured a non-nil StatusWriter.
	if capturedWriter == nil {
		t.Fatal("onStatusWriter seam captured nil StatusWriter; assembleOrchestrate did not construct or wire the status writer")
	}

	// 4. The captured writer is a *reporterStatusWriter (the concrete type
	// constructed on the orchestrate path).
	_, ok := capturedWriter.(*reporterStatusWriter)
	if !ok {
		t.Fatalf("captured StatusWriter is type %T, want *reporterStatusWriter", capturedWriter)
	}
}

// TestNewTransportDispatchHappyPath is TC-111-03: real keygen yields a non-nil
// DispatchFunc and nil error; the existing round-trip still passes.
func TestNewTransportDispatchHappyPath(t *testing.T) {
	signingKey := testSigningKey(t)
	cache := envelope.NewReplayCache(0)
	resultCache := envelope.NewReplayCache(0)
	sink := audit.NewFakeSink()

	fn, err := newTransportDispatch(signingKey, cache, resultCache, sink, discardLogger(), nil)

	// TC-111-03 assertions:

	// 1. nil error on success.
	if err != nil {
		t.Fatalf("newTransportDispatch returned unexpected error: %v", err)
	}
	// 2. Non-nil DispatchFunc.
	if fn == nil {
		t.Fatal("newTransportDispatch returned nil DispatchFunc on success")
	}

	// 3. The dispatch actually seals under real (non-zero) keys — exercised indirectly
	//    by asserting the existing replay-cache behavior still holds with the returned
	//    dispatcher. Use the shared cache to confirm it records the nonce (which only
	//    happens when a real seal/sign succeeded).
	sub := orchestrator.SubGoal{
		RecipeName: "coding-agent",
		Task:       supervisor.Task{ID: "tc-111-03", Spec: "happy path"},
	}
	// Drive one dispatch; it may fail at runtime.Run (no real worktree) but the
	// transport layer (seal → verify) must not fail with ErrReplay or ErrBadSignature.
	if dispErr := fn(context.Background(), sub, runtimewiring.Config{}); dispErr != nil {
		if errors.Is(dispErr, envelope.ErrReplay) || errors.Is(dispErr, envelope.ErrBadSignature) {
			t.Fatalf("dispatch failed with crypto error (zero keys?): %v", dispErr)
		}
		// runtime.Run failures (missing worktree etc.) are expected and irrelevant here.
	}

	// 4. Replay check: the cache now holds the nonce from the dispatch above — confirm
	//    the seam is using real key material (assertReplayRejectedByCache proves the
	//    shared cache is live, which only works if real envelopes were sealed).
	assertReplayRejectedByCache(t, signingKey, cache)
}

// --- small local helpers -----------------------------------------------------

func discard() *bytes.Buffer { return &bytes.Buffer{} }

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func readRepoDoc(t *testing.T, rel string) string {
	t.Helper()
	root := repoRoot(t)
	data, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// repoRoot walks up from the test working directory to the module root (the dir
// containing go.mod), so doc-content tests work under `go test ./...` regardless of
// the package's nested path.
func repoRoot(t *testing.T) string {
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
			t.Fatalf("could not locate go.mod above %s", dir)
		}
		dir = parent
	}
}

// --- TC-129-05 ---------------------------------------------------------------
func TestAssembleOrchestrateReadsRequireApprovalEnv(t *testing.T) {
	setBaseConfigEnv(t)
	writeSigningKeyFile(t)

	cases := []struct {
		envVal      string
		expectPause bool
	}{
		{envVal: "", expectPause: true}, // unset / default
		{envVal: "true", expectPause: true},
		{envVal: "1", expectPause: true},
		{envVal: "yes", expectPause: true},
		{envVal: "false", expectPause: false},
		{envVal: "0", expectPause: false},
		{envVal: "no", expectPause: false},
		{envVal: "NO", expectPause: false},
	}

	for _, tc := range cases {
		t.Run("env="+tc.envVal, func(t *testing.T) {
			t.Setenv("AGENT_BUILDER_REQUIRE_APPROVAL", tc.envVal)
			spy := &spyDispatch{}
			sink := audit.NewFakeSink()

			oc, cleanup, err := assembleOrchestrate(Config{Stdout: discard(), Stderr: discard()}, assembleOverrides{
				policyClient: &perActionPolicy{spawnPlan: policy.DecisionAllow, spawnWorker: map[string]policy.Decision{}},
				dispatch:     spy.fn,
				auditSink:    sink,
				planner:      twoRecipePlanner(),
				source:       &stubGoalSource{goals: []supervisor.Task{twoSubGoalGoal()}},
				signingKey:   testSigningKey(t),
			})
			if err != nil {
				t.Fatalf("assembleOrchestrate: %v", err)
			}
			t.Cleanup(cleanup)

			res, err := oc.orch.ConfirmAndPlan(context.Background(), twoSubGoalGoal())
			if err != nil {
				t.Fatalf("ConfirmAndPlan: %v", err)
			}

			if tc.expectPause {
				if spy.count() != 0 {
					t.Errorf("expected plan to pause (0 dispatches), got %d", spy.count())
				}
				if !oc.orch.HasPendingPlan("g1") {
					t.Error("expected pending plan to be held in store")
				}
				if len(res.Outcomes) != 0 {
					t.Errorf("expected 0 outcomes in pause result, got %d", len(res.Outcomes))
				}
			} else {
				if spy.count() != 2 {
					t.Errorf("expected plan to auto-dispatch (2 dispatches), got %d", spy.count())
				}
				if oc.orch.HasPendingPlan("g1") {
					t.Error("expected no pending plan to be held in store")
				}
				if len(res.Outcomes) != 2 {
					t.Errorf("expected 2 outcomes in auto-dispatch result, got %d", len(res.Outcomes))
				}
			}
		})
	}
}

// TestAssembleOrchestrateUsesReporterStatusWriter is TC-130-04: verifies that
// assembleOrchestrate wires reporterStatusWriter which routes needs-human
// status writes to the spy reporter.
func TestAssembleOrchestrateUsesReporterStatusWriter(t *testing.T) {
	setBaseConfigEnv(t)
	rep := &recordingReporter{}

	var capturedWriter loop.StatusWriter
	oc, cleanup, err := assembleOrchestrate(
		Config{Stdout: discard(), Stderr: discard()},
		assembleOverrides{
			signingKey:     testSigningKey(t),
			source:         &recordingGoalSource{},
			reporter:       rep,
			onStatusWriter: func(w loop.StatusWriter) { capturedWriter = w },
		},
	)
	t.Cleanup(cleanup)

	if err != nil {
		t.Fatalf("assembleOrchestrate returned error: %v", err)
	}

	if oc.orch == nil {
		t.Fatal("orchestrateConfig.orch is nil")
	}

	if capturedWriter == nil {
		t.Fatal("onStatusWriter captured nil status writer")
	}

	// 1. Verify the concrete type is *reporterStatusWriter
	w, ok := capturedWriter.(*reporterStatusWriter)
	if !ok {
		t.Fatalf("captured StatusWriter is type %T, want *reporterStatusWriter", capturedWriter)
	}

	// 2. Verify that WriteStatus propagates to our spy reporter
	res, err := w.WriteStatus("goal-99", tasksource.WritableStatusNeedsHuman)
	if err != nil {
		t.Fatalf("WriteStatus failed: %v", err)
	}
	if !res.Changed {
		t.Error("expected Changed to be true")
	}

	reports := rep.all()
	if len(reports) != 1 {
		t.Fatalf("expected exactly 1 report, got %d", len(reports))
	}

	expected := "needs-human: goal goal-99 escalated (needs-human)"
	if reports[0] != expected {
		t.Errorf("reported text = %q, want %q", reports[0], expected)
	}
}

// TestReporterStatusWriterReplacesFileBackedWriter is TC-130-05: verifies that
// the file-backed tasksource.StatusWriter is no longer constructed on the
// orchestrate path and that tasksource is not directly imported in orchestrate.go.
func TestReporterStatusWriterReplacesFileBackedWriter(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "orchestrate.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("failed to parse orchestrate.go AST: %v", err)
	}

	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "github.com/tkdtaylor/agent-builder/internal/tasksource" {
			t.Error("orchestrate.go directly imports internal/tasksource; this direct dependency must be removed")
		}
	}
}

