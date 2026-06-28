package agentbuilderworker

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
)

// TestTC082_01_SelectRecipeReturnsNonNilRecipe tests TC-082-01:
// SelectRecipe("agent-builder-worker") returns a non-nil Recipe whose
// GateFactory produces a real, blocking gate. ListRecipes includes the recipe.
func TestTC082_01_SelectRecipeReturnsNonNilRecipe(t *testing.T) {
	r, err := recipe.SelectRecipe("agent-builder-worker")
	if err != nil {
		t.Fatalf("SelectRecipe(\"agent-builder-worker\") failed: %v", err)
	}

	if r.Name != "agent-builder-worker" {
		t.Errorf("Name = %q, want \"agent-builder-worker\"", r.Name)
	}
	if r.GoalSourceFactory == nil {
		t.Error("GoalSourceFactory is nil")
	}
	if r.GateFactory == nil {
		t.Fatal("GateFactory is nil")
	}
	if r.ResultSinkFactory == nil {
		t.Error("ResultSinkFactory is nil")
	}

	// Gate-existence assertion: GateFactory returns a real, blocking gate.
	g := r.GateFactory()
	if g == nil {
		t.Fatal("GateFactory() returned nil")
	}
	blocker, ok := g.(gate.Blocker)
	if !ok {
		t.Errorf("gate type %T does not implement gate.Blocker", g)
	} else if !blocker.Blocks() {
		t.Errorf("gate.Blocks() = false, want true")
	}

	// ListRecipes includes "agent-builder-worker".
	found := false
	for _, name := range recipe.ListRecipes() {
		if name == "agent-builder-worker" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("\"agent-builder-worker\" not in ListRecipes(): %v", recipe.ListRecipes())
	}
}

// TestTC082_01_GateIsAgentBuilderWorkerGateType tests that the GateFactory
// returns an *AgentBuilderWorkerGate (specific type, implements Blocker).
func TestTC082_01_GateIsAgentBuilderWorkerGateType(t *testing.T) {
	g := newAgentBuilderWorkerGateFactory()
	if g == nil {
		t.Fatal("newAgentBuilderWorkerGateFactory() returned nil")
	}
	if _, ok := g.(*AgentBuilderWorkerGate); !ok {
		t.Errorf("gate type = %T, want *AgentBuilderWorkerGate", g)
	}
}

// TestTC082_01_GateHasThreeSteps tests that the gate composes exactly
// code-scanner, dep-scan, and generated-gate-existence steps.
// Verified by running the gate on a temp dir with fake tool binaries.
func TestTC082_01_GateHasThreeSteps(t *testing.T) {
	// Install fake code-scanner (exits 0) and dep-scan (exits 0) on PATH.
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "code-scanner"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "dep-scan"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	t.Setenv("PATH", binDir)

	// Fixture: a .go file with a valid GateFactory binding.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "recipe.go"), validGateRecipeSource)

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	// Collect step names.
	names := make([]string, len(verdict.Results))
	for i, r := range verdict.Results {
		names[i] = r.Name
	}
	want := []string{"code-scanner", "dep-scan", "generated-gate-existence"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("step names = %v, want %v", names, want)
	}
}

// TestTC082_01_GateHasNoSkipSurface verifies no Skip or Bypass method on the gate.
func TestTC082_01_GateHasNoSkipSurface(t *testing.T) {
	g := newAgentBuilderWorkerGateFactory()
	gateType := reflect.TypeOf(g)
	if _, ok := gateType.MethodByName("Skip"); ok {
		t.Error("AgentBuilderWorkerGate exposes Skip method")
	}
	if _, ok := gateType.MethodByName("Bypass"); ok {
		t.Error("AgentBuilderWorkerGate exposes Bypass method")
	}
}

// ----------------------------------------------------------------------------
// TC-082-02: Gate rejects code-scanner and dep-scan fixtures
// ----------------------------------------------------------------------------

// TestTC082_02A_CodeScannerFindingFailsVerdict tests TC-082-02 sub-test A:
// A fake code-scanner that outputs a flagged pattern causes Verdict.OK==false,
// and the code-scanner StepResult names the finding.
func TestTC082_02A_CodeScannerFindingFailsVerdict(t *testing.T) {
	binDir := t.TempDir()
	// Fake code-scanner: outputs MALWARE finding and exits 1.
	writeScript(t, filepath.Join(binDir, "code-scanner"),
		"#!/bin/sh\nprintf 'MALWARE credential pattern found\\n'\nexit 1\n")
	// Fake dep-scan: clean (exits 0) — isolates code-scanner failure.
	writeScript(t, filepath.Join(binDir, "dep-scan"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	t.Setenv("PATH", binDir)

	// Fixture: a .go file with a credential-harvest function name.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "flagged.go"),
		"package flagged\n\nfunc ReadTokenFile() {}\n")

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	if verdict.OK {
		t.Fatal("Verdict.OK = true, want false (code-scanner finding)")
	}

	var scanResult *gate.StepResult
	for i := range verdict.Results {
		if verdict.Results[i].Name == "code-scanner" {
			r := verdict.Results[i]
			scanResult = &r
			break
		}
	}
	if scanResult == nil {
		t.Fatal("code-scanner step result not found in verdict")
	}
	if scanResult.OK {
		t.Errorf("code-scanner StepResult.OK = true, want false")
	}
	if !containsAny(scanResult.Output, "MALWARE", "credential") {
		t.Errorf("code-scanner output = %q; want it to contain 'MALWARE' or 'credential'", scanResult.Output)
	}
}

// TestTC082_02B_DepScanCVEFindingFailsVerdict tests TC-082-02 sub-test B:
// A fake dep-scan that outputs a CVE finding causes Verdict.OK==false,
// and the dep-scan StepResult names the CVE.
func TestTC082_02B_DepScanCVEFindingFailsVerdict(t *testing.T) {
	binDir := t.TempDir()
	// Fake code-scanner: clean.
	writeScript(t, filepath.Join(binDir, "code-scanner"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	// Fake dep-scan: CVE finding exits 1.
	writeScript(t, filepath.Join(binDir, "dep-scan"),
		"#!/bin/sh\nprintf 'CVE-2026-0001 HIGH vulnerable module\\n'\nexit 1\n")
	t.Setenv("PATH", binDir)

	// Fixture: a .go file + go.sum (dep-scan only runs when go.sum exists).
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "recipe.go"), "package recipe\n")
	writeFile(t, filepath.Join(dir, "go.sum"),
		"example.com/dep v1.0.0 h1:abc=\nexample.com/dep v1.0.0/go.mod h1:def=\n")

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	if verdict.OK {
		t.Fatal("Verdict.OK = true, want false (dep-scan CVE finding)")
	}

	var depResult *gate.StepResult
	for i := range verdict.Results {
		if verdict.Results[i].Name == "dep-scan" {
			r := verdict.Results[i]
			depResult = &r
			break
		}
	}
	if depResult == nil {
		t.Fatal("dep-scan step result not found in verdict")
	}
	if depResult.OK {
		t.Errorf("dep-scan StepResult.OK = true, want false")
	}
	if !containsAny(depResult.Output, "CVE-2026-0001") {
		t.Errorf("dep-scan output = %q; want it to contain 'CVE-2026-0001'", depResult.Output)
	}
}

// ----------------------------------------------------------------------------
// TC-082-03: Gate applies gate-existence assertion on generated recipe output
// ----------------------------------------------------------------------------

// TestTC082_03A_GeneratedRecipeWithNoGateFails tests TC-082-03 sub-test A:
// A generated recipe with no GateFactory binding causes the
// generated-gate-existence step to fail with an error naming "GateFactory" or "gate".
func TestTC082_03A_GeneratedRecipeWithNoGateFails(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "code-scanner"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "dep-scan"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	t.Setenv("PATH", binDir)

	// Fixture: generated recipe with no GateFactory field at all.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "generated.go"), noGateRecipeSource)

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	if verdict.OK {
		t.Fatal("Verdict.OK = true, want false (no gate binding in generated recipe)")
	}

	var gateExistResult *gate.StepResult
	for i := range verdict.Results {
		if verdict.Results[i].Name == "generated-gate-existence" {
			r := verdict.Results[i]
			gateExistResult = &r
			break
		}
	}
	if gateExistResult == nil {
		t.Fatal("generated-gate-existence step result not found in verdict")
	}
	if gateExistResult.OK {
		t.Errorf("generated-gate-existence StepResult.OK = true, want false")
	}
	if !containsAny(gateExistResult.Output, "GateFactory", "gate") {
		t.Errorf("generated-gate-existence output = %q; want 'GateFactory' or 'gate'", gateExistResult.Output)
	}
}

// TestTC082_03B_GeneratedRecipeWithNilGateFails tests TC-082-03 sub-test A (nil variant):
// A generated recipe with GateFactory: nil causes the gate-existence step to fail.
func TestTC082_03B_GeneratedRecipeWithNilGateFails(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "code-scanner"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "dep-scan"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	t.Setenv("PATH", binDir)

	// Fixture: generated recipe with explicit GateFactory: nil.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "generated.go"), nilGateRecipeSource)

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	if verdict.OK {
		t.Fatal("Verdict.OK = true, want false (GateFactory: nil in generated recipe)")
	}
	var gateExistResult *gate.StepResult
	for i := range verdict.Results {
		if verdict.Results[i].Name == "generated-gate-existence" {
			r := verdict.Results[i]
			gateExistResult = &r
			break
		}
	}
	if gateExistResult == nil {
		t.Fatal("generated-gate-existence step not in verdict")
	}
	if gateExistResult.OK {
		t.Errorf("generated-gate-existence StepResult.OK = true for nil gate, want false")
	}
}

// TestTC082_03C_GeneratedRecipeWithValidGatePasses tests TC-082-03 sub-test B:
// A generated recipe with a valid (non-nil) GateFactory binding passes the
// generated-gate-existence step and yields Verdict.OK == true.
func TestTC082_03C_GeneratedRecipeWithValidGatePasses(t *testing.T) {
	binDir := t.TempDir()
	writeScript(t, filepath.Join(binDir, "code-scanner"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	writeScript(t, filepath.Join(binDir, "dep-scan"), "#!/bin/sh\nprintf 'clean\\n'\nexit 0\n")
	t.Setenv("PATH", binDir)

	// Fixture: generated recipe with a valid (non-nil) GateFactory binding.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "generated.go"), validGateRecipeSource)

	g := newAgentBuilderWorkerGateFactory()
	verdict := g.Verify(dir)

	if !verdict.OK {
		for _, r := range verdict.Results {
			if !r.OK {
				t.Logf("step %q failed: %s", r.Name, r.Output)
			}
		}
		t.Fatal("Verdict.OK = false, want true (valid gate binding)")
	}

	var gateExistResult *gate.StepResult
	for i := range verdict.Results {
		if verdict.Results[i].Name == "generated-gate-existence" {
			r := verdict.Results[i]
			gateExistResult = &r
			break
		}
	}
	if gateExistResult == nil {
		t.Fatal("generated-gate-existence step not in verdict")
	}
	if !gateExistResult.OK {
		t.Errorf("generated-gate-existence StepResult.OK = false, want true; output: %s", gateExistResult.Output)
	}
}

// ----------------------------------------------------------------------------
// TC-082-04: Human approval is required before generated agent is dispatched
// ----------------------------------------------------------------------------

// TestTC082_04_RequireApprovalPreventsDispatch tests TC-082-04:
// An orchestrator stub that sets PolicyDecision="require_approval" causes the
// goal source to not yield a task, and marks ApprovalSolicited=true.
func TestTC082_04_RequireApprovalPreventsDispatch(t *testing.T) {
	// Simulate the orchestrator receiving require_approval from the policy engine.
	goalSource := &AgentBuilderWorkerGoalSource{
		PolicyDecision: "require_approval",
	}

	task, ok, err := goalSource.Next()
	if err != nil {
		t.Fatalf("Next() returned unexpected error: %v", err)
	}
	if ok {
		t.Errorf("Next() ok = true, want false (dispatch withheld pending approval)")
	}
	if task.ID != "" || task.Repo != "" {
		t.Errorf("Next() returned non-empty task = %+v, want empty Task", task)
	}
	if !goalSource.ApprovalSolicited {
		t.Errorf("ApprovalSolicited = false, want true (approval solicited)")
	}
}

// TestTC082_04_AllowDispatchesNormally verifies that without require_approval,
// Next() yields the task normally.
func TestTC082_04_AllowDispatchesNormally(t *testing.T) {
	goalSource := &AgentBuilderWorkerGoalSource{
		PolicyDecision: "allow",
	}

	task, ok, err := goalSource.Next()
	if err != nil {
		t.Fatalf("Next() returned unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("Next() ok = false, want true (task should be yielded)")
	}
	if task.ID == "" {
		t.Errorf("Next() returned empty task.ID, want non-empty task")
	}
	if goalSource.ApprovalSolicited {
		t.Errorf("ApprovalSolicited = true, want false (no approval required)")
	}
}

// TestTC082_04_EmptyDecisionDispatchesNormally verifies that an empty
// PolicyDecision (the default) yields the task normally.
func TestTC082_04_EmptyDecisionDispatchesNormally(t *testing.T) {
	goalSource := &AgentBuilderWorkerGoalSource{}

	task, ok, err := goalSource.Next()
	if err != nil {
		t.Fatalf("Next() returned unexpected error: %v", err)
	}
	if !ok {
		t.Errorf("Next() ok = false, want true (no policy decision set)")
	}
	if task.ID == "" {
		t.Error("Next() returned empty task when policy is allow (default)")
	}
}

// ----------------------------------------------------------------------------
// TC-082-05: Audit trail records generated file path and content hash
// ----------------------------------------------------------------------------

// TestTC082_05_AuditEventContainsFilePathAndHash tests TC-082-05:
// EmitAuditEvent emits exactly one ActionPublish event whose Detail.Branch
// is the generated file path and Detail.Remote is the SHA-256 content hash.
func TestTC082_05_AuditEventContainsFilePathAndHash(t *testing.T) {
	sink := audit.NewFakeSink()
	contents := []byte("package recipe\n// generated\n")
	generatedFilePath := "/tmp/generated/recipe.go"
	taskID := "task-082-test"
	runID := "run-082-test"

	if err := EmitAuditEvent(sink, taskID, runID, generatedFilePath, contents); err != nil {
		t.Fatalf("EmitAuditEvent returned error: %v", err)
	}

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("FakeSink has %d events, want 1", len(events))
	}

	ev := events[0]

	if ev.Action != audit.ActionPublish {
		t.Errorf("event.Action = %q, want %q", ev.Action, audit.ActionPublish)
	}
	if ev.TaskID != taskID {
		t.Errorf("event.TaskID = %q, want %q", ev.TaskID, taskID)
	}
	if ev.RunID != runID {
		t.Errorf("event.RunID = %q, want %q", ev.RunID, runID)
	}
	// Detail.Branch holds the generated file path.
	if ev.Detail.Branch != generatedFilePath {
		t.Errorf("event.Detail.Branch = %q, want %q (generated file path)", ev.Detail.Branch, generatedFilePath)
	}
	// Detail.Remote holds the SHA-256 hex digest.
	sum := sha256.Sum256(contents)
	wantHash := fmt.Sprintf("%x", sum)
	if ev.Detail.Remote != wantHash {
		t.Errorf("event.Detail.Remote = %q, want SHA-256 %q", ev.Detail.Remote, wantHash)
	}
}

// TestTC082_05_AuditEventWithRealFileContents verifies the hash is reproducible
// using the actual file content bytes.
func TestTC082_05_AuditEventWithRealFileContents(t *testing.T) {
	sink := audit.NewFakeSink()

	// Write a temp file and read it back — simulating a real generated recipe.
	tmpDir := t.TempDir()
	generatedPath := filepath.Join(tmpDir, "generated_recipe.go")
	contents := []byte(validGateRecipeSource)
	if err := os.WriteFile(generatedPath, contents, 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	fileBytes, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	if err := EmitAuditEvent(sink, "task-082", "run-001", generatedPath, fileBytes); err != nil {
		t.Fatalf("EmitAuditEvent: %v", err)
	}

	events := sink.Events()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]

	if ev.Detail.Branch != generatedPath {
		t.Errorf("Detail.Branch = %q, want %q", ev.Detail.Branch, generatedPath)
	}
	sum := sha256.Sum256(fileBytes)
	wantHash := fmt.Sprintf("%x", sum)
	if ev.Detail.Remote != wantHash {
		t.Errorf("Detail.Remote = %q, want %q", ev.Detail.Remote, wantHash)
	}
}

// ----------------------------------------------------------------------------
// TC-082-06: No special code path in internal/runtime or internal/orchestrator
// ----------------------------------------------------------------------------

// TestTC082_06_NoSpecialCodePathInRuntimeOrOrchestrator tests TC-082-06:
// Verifies that the agentbuilderworker package does not directly import
// internal/runtime or internal/orchestrator (the "no special code path" property
// is validated structurally by checking direct imports).
func TestTC082_06_NoSpecialCodePathInRuntimeOrOrchestrator(t *testing.T) {
	out, err := exec.Command("go", "list", "-f", "{{range .Imports}}{{.}}\n{{end}}", ".").CombinedOutput()
	if err != nil {
		t.Fatalf("go list direct imports failed: %v\n%s", err, out)
	}
	imports := string(out)
	if containsAny(imports, "agent-builder/internal/runtime") {
		t.Errorf("agentbuilderworker directly imports internal/runtime; no special code path allowed (REQ-082-06)")
	}
	if containsAny(imports, "agent-builder/internal/orchestrator") {
		t.Errorf("agentbuilderworker directly imports internal/orchestrator; no special code path allowed (REQ-082-06)")
	}
}

// ----------------------------------------------------------------------------
// Unit tests for GeneratedGateExistenceStep
// ----------------------------------------------------------------------------

func TestGeneratedGateExistenceStep_NoGoFiles(t *testing.T) {
	dir := t.TempDir()
	// No .go files — step must fail.
	step := GeneratedGateExistenceStep{}
	result := step.Run(dir)
	if result.OK {
		t.Errorf("OK = true for dir with no .go files, want false")
	}
	if !containsAny(result.Output, ".go", "recipe") {
		t.Errorf("output = %q; want mention of .go or recipe", result.Output)
	}
}

func TestGeneratedGateExistenceStep_NoGateFactory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gen.go"), noGateRecipeSource)
	step := GeneratedGateExistenceStep{}
	result := step.Run(dir)
	if result.OK {
		t.Errorf("OK = true for recipe with no GateFactory, want false")
	}
}

func TestGeneratedGateExistenceStep_NilGateFactory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gen.go"), nilGateRecipeSource)
	step := GeneratedGateExistenceStep{}
	result := step.Run(dir)
	if result.OK {
		t.Errorf("OK = true for recipe with GateFactory: nil, want false")
	}
}

func TestGeneratedGateExistenceStep_ValidGateFactory(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "gen.go"), validGateRecipeSource)
	step := GeneratedGateExistenceStep{}
	result := step.Run(dir)
	if !result.OK {
		t.Errorf("OK = false for recipe with valid GateFactory, want true; output: %s", result.Output)
	}
}

// ----------------------------------------------------------------------------
// Unit tests for inspectTextForGate
// ----------------------------------------------------------------------------

func TestInspectTextForGate_NoGateFactory(t *testing.T) {
	src := `package example
func NewRecipe() Recipe {
	return Recipe{}
}`
	result := inspectTextForGate(src)
	if result.hasGateFactory {
		t.Error("hasGateFactory = true for source with no GateFactory, want false")
	}
}

func TestInspectTextForGate_NilGateFactory(t *testing.T) {
	src := `package example
func NewRecipe() Recipe {
	return Recipe{GateFactory: nil}
}`
	result := inspectTextForGate(src)
	if !result.hasGateFactory {
		t.Error("hasGateFactory = false, want true")
	}
	if !result.isNilBinding {
		t.Error("isNilBinding = false, want true")
	}
}

func TestInspectTextForGate_ValidGateFactory(t *testing.T) {
	src := `package example
func NewRecipe() Recipe {
	return Recipe{GateFactory: newGateFactory}
}`
	result := inspectTextForGate(src)
	if !result.hasGateFactory {
		t.Error("hasGateFactory = false, want true")
	}
	if result.isNilBinding {
		t.Error("isNilBinding = true for valid binding, want false")
	}
}

// ----------------------------------------------------------------------------
// Fixture sources
// ----------------------------------------------------------------------------

// validGateRecipeSource is a generated recipe stub with a valid (non-nil) GateFactory.
const validGateRecipeSource = `package generatedrecipe

import (
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func newMyGateFactory() supervisor.Gate { return nil }

func newMyRecipe() (recipe.Recipe, error) {
	return recipe.New(
		func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) { return nil, nil },
		recipe.RoutingSpec{MinCapability: 1},
		newMyGateFactory,
		func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) { return nil, nil },
		nil,
	), nil
}

func init() {
	recipe.Register("my-generated-recipe", newMyRecipe)
}
`

// noGateRecipeSource is a generated recipe stub with no GateFactory reference at all.
const noGateRecipeSource = `package generatedrecipe

// This is a generated recipe with no gate binding — should fail gate-existence check.
func placeholder() {
	_ = "no gate here"
}
`

// nilGateRecipeSource is a generated recipe stub with an explicit GateFactory: nil.
const nilGateRecipeSource = `package generatedrecipe

import (
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

func newBadRecipe() (recipe.Recipe, error) {
	return recipe.Recipe{
		GoalSourceFactory: func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) { return nil, nil },
		GateFactory: nil,
		ResultSinkFactory: func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) { return nil, nil },
	}, nil
}
`

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func containsAny(s string, substrings ...string) bool {
	for _, sub := range substrings {
		found := false
		for i := 0; i <= len(s)-len(sub); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if found {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func writeScript(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o700); err != nil {
		t.Fatalf("write script %s: %v", path, err)
	}
}
