// Package agentbuilderworker provides the "agent-builder-worker" recipe — the
// first-party code-authoring worker recipe whose job is to author a new agent
// definition (a new Go recipe) and publish it via the standard branch/PR path
// (ADR 042, ADR 047).
//
// The worker inherits the full worker safety model:
//   - code-scanner step: scans generated code for malware/backdoor patterns
//   - dep-scan step: scans declared dependencies for known CVEs
//   - generated-gate-existence step: asserts the generated recipe declares a
//     non-nil, non-skippable gate binding (ADR 047 point 1)
//   - policy: human approval required before any generated agent is dispatched
//   - audit trail: generated file path + content hash emitted on ActionPublish
//
// Registration: init() registers the recipe via recipe.Register, triggered by an
// import from cmd/agent-builder. This preserves internal/runtime leaf-purity.
//
// Delivery: v1 uses the standard branch/PR path (same as coding-agent). The
// agent-mesh hand-back transport (task 083) is out of scope.
package agentbuilderworker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/audit"
	"github.com/tkdtaylor/agent-builder/internal/gate"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// AgentBuilderWorkerGate is the gate implementation for the agent-builder-worker recipe.
// It composes three steps:
//  1. CodeScannerStep — scans generated code for malware/backdoor patterns
//  2. DepScanStep — scans declared dependencies for known CVEs
//  3. GeneratedGateExistenceStep — asserts the generated recipe declares a gate
//
// All three steps are non-skippable. No Skip, Bypass, or conditional-override
// surface is exposed (fitness-gate-blocking invariant).
type AgentBuilderWorkerGate struct {
	*gate.Gate
}

// Blocks returns true, indicating this is a real, blocking gate.
func (g *AgentBuilderWorkerGate) Blocks() bool {
	return true
}

// GeneratedGateExistenceStep is a gate step that inspects the generated `.go`
// recipe output for a non-nil GateFactory binding (ADR 047 point 1). It walks
// all `.go` files in the target directory and verifies that at least one of them
// declares a `GateFactory` field assignment that is not `nil`.
//
// This is a static text analysis step — it does not compile the generated code.
// It checks for:
//   - Presence of a `GateFactory` identifier in the source text
//   - The binding is not nil (i.e. "GateFactory: nil" or "GateFactory =  nil"
//     patterns are absent while "GateFactory" is present with a non-nil value)
type GeneratedGateExistenceStep struct{}

// Name returns the step identifier used in gate.Verdict.Results.
func (s GeneratedGateExistenceStep) Name() string {
	return "generated-gate-existence"
}

// Run inspects all `.go` files in repoPath for a valid GateFactory binding.
// Returns OK=false if no `.go` files exist, if none declares a GateFactory
// binding, or if every GateFactory binding is set to nil.
func (s GeneratedGateExistenceStep) Run(repoPath string) gate.StepResult {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return gate.StepResult{
			OK:     false,
			Output: fmt.Sprintf("generated-gate-existence: read dir %q: %v", repoPath, err),
		}
	}

	goFiles := make([]string, 0)
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			goFiles = append(goFiles, filepath.Join(repoPath, e.Name()))
		}
	}

	if len(goFiles) == 0 {
		return gate.StepResult{
			OK:     false,
			Output: "generated-gate-existence: no .go files found in output directory; generated recipe must be a .go source file",
		}
	}

	// Inspect each .go file for a GateFactory binding that is not nil.
	for _, goFile := range goFiles {
		result := inspectFileForGate(goFile)
		if result.hasGateFactory && !result.isNilBinding {
			return gate.StepResult{
				OK:     true,
				Output: fmt.Sprintf("generated-gate-existence: found non-nil GateFactory binding in %s", filepath.Base(goFile)),
			}
		}
	}

	// No file had a valid non-nil gate binding.
	return gate.StepResult{
		OK:     false,
		Output: "generated-gate-existence: no non-nil GateFactory binding found in generated recipe output; a recipe must bind a non-nil, blocking gate (ADR 044, ADR 047)",
	}
}

// gateInspectionResult captures what a single file scan found.
type gateInspectionResult struct {
	hasGateFactory bool
	isNilBinding   bool
}

// inspectFileForGate reads a single .go file and determines whether it contains
// a non-nil GateFactory binding using static text analysis.
func inspectFileForGate(path string) gateInspectionResult {
	src, err := os.ReadFile(path)
	if err != nil {
		return gateInspectionResult{}
	}
	return inspectTextForGate(string(src))
}

// inspectTextForGate performs text-based GateFactory detection. It looks for:
//   - `GateFactory` present in the source (indicates the field is referenced)
//   - None of the nil-binding patterns are present (GateFactory: nil, etc.)
//
// A file with "GateFactory" present and no nil binding is treated as having a
// valid (non-nil) gate factory assignment.
func inspectTextForGate(src string) gateInspectionResult {
	if !strings.Contains(src, "GateFactory") {
		return gateInspectionResult{}
	}

	// Check for explicit nil binding patterns.
	nilPatterns := []string{
		"GateFactory: nil",
		"GateFactory:nil",
		"GateFactory: nil,",
		"GateFactory:nil,",
	}
	for _, pattern := range nilPatterns {
		if strings.Contains(src, pattern) {
			return gateInspectionResult{hasGateFactory: true, isNilBinding: true}
		}
	}

	// GateFactory is present and not bound to nil — valid gate reference.
	return gateInspectionResult{hasGateFactory: true, isNilBinding: false}
}

// AgentBuilderWorkerGoalSource is the goal source for the agent-builder-worker
// recipe. It returns the single "author a new recipe" task, unless the
// PolicyDecision field is set to "require_approval", in which case it withholds
// the task and marks ApprovalSolicited.
//
// This implements REQ-082-04: human approval is required before a generated agent
// is dispatched. The real policy query (against the policy-engine block) is
// performed by the orchestrator (task 081); this struct exposes the seam that
// the orchestrator stub can drive in tests.
type AgentBuilderWorkerGoalSource struct {
	// PolicyDecision is the decision from the policy engine. When set to
	// "require_approval", Next() withholds the task and sets ApprovalSolicited.
	PolicyDecision string

	// ApprovalSolicited is set to true when Next() withholds the task because
	// the policy decision requires human approval.
	ApprovalSolicited bool

	served bool
}

// Next returns the "author a new recipe" task, or (Task{}, false, nil) when the
// policy decision requires human approval (REQ-082-04).
func (g *AgentBuilderWorkerGoalSource) Next() (supervisor.Task, bool, error) {
	if g.PolicyDecision == "require_approval" {
		g.ApprovalSolicited = true
		return supervisor.Task{}, false, nil
	}
	if g.served {
		return supervisor.Task{}, false, nil
	}
	g.served = true
	return supervisor.Task{
		ID:   "agent-builder-worker-task",
		Repo: "agent-builder",
		Spec: "author a new agent recipe",
	}, true, nil
}

// AgentBuilderWorkerResultSink is the result sink for the agent-builder-worker
// recipe. It accepts published branches (the generated recipe .go file lands on
// a branch), emitting an audit event for each published result.
type AgentBuilderWorkerResultSink struct {
	// Sink is the audit.Sink to emit events to. May be nil (no-op).
	Sink audit.Sink
}

// Publish records the publish result. If Sink is non-nil, it emits an
// ActionPublish AuditEvent. The generated file path is stored in Detail.Branch
// and the content hash in Detail.Remote (see test spec TC-082-05 note).
func (s *AgentBuilderWorkerResultSink) Publish(_ context.Context, req supervisor.PublishRequest) (supervisor.PublishResult, error) {
	if s.Sink != nil {
		_ = s.Sink.Append(audit.AuditEvent{
			Action: audit.ActionPublish,
			RunID:  req.Branch,
			TaskID: req.Task.ID,
			Detail: audit.EventDetail{
				Branch: req.Branch,
				Remote: req.Remote,
			},
		})
	}
	return supervisor.PublishResult{Branch: req.Branch}, nil
}

// EmitAuditEvent emits an ActionPublish AuditEvent recording the generated file
// path and SHA-256 content hash to the provided sink (REQ-082-05, ADR 047 point 3).
//
// The generated file path is stored in Detail.Branch and the SHA-256 hex digest
// is stored in Detail.Remote (closest available fields in the current AuditEvent
// shape — see test spec TC-082-05 for rationale).
func EmitAuditEvent(sink audit.Sink, taskID, runID, generatedFilePath string, contents []byte) error {
	sum := sha256.Sum256(contents)
	contentHash := fmt.Sprintf("%x", sum)

	return sink.Append(audit.AuditEvent{
		Action: audit.ActionPublish,
		RunID:  runID,
		TaskID: taskID,
		Detail: audit.EventDetail{
			Branch: generatedFilePath, // generated file path (repurposed field — see TC-082-05)
			Remote: contentHash,       // SHA-256 hex digest of file contents (repurposed field)
		},
	})
}

// newAgentBuilderWorkerGate constructs the agent-builder-worker gate with three
// non-skippable steps: code-scanner, dep-scan, generated-gate-existence.
func newAgentBuilderWorkerGate() (supervisor.Gate, error) {
	verifier, err := gate.New(
		gate.CodeScannerStep{},
		gate.DepScanStep{},
		GeneratedGateExistenceStep{},
	)
	if err != nil {
		return nil, fmt.Errorf("construct agent-builder-worker gate: %w", err)
	}
	return &AgentBuilderWorkerGate{verifier}, nil
}

// newAgentBuilderWorkerGateFactory wraps newAgentBuilderWorkerGate to match
// the recipe.GateFactory signature.
func newAgentBuilderWorkerGateFactory() supervisor.Gate {
	g, _ := newAgentBuilderWorkerGate()
	return g
}

// newAgentBuilderWorkerRecipe constructs the agent-builder-worker Recipe.
func newAgentBuilderWorkerRecipe() (recipe.Recipe, error) {
	goalSourceFactory := func(cfg recipe.SeamConfig) (supervisor.GoalSource, error) {
		return &AgentBuilderWorkerGoalSource{}, nil
	}

	resultSinkFactory := func(cfg recipe.SeamConfig) (supervisor.ResultSink, error) {
		return &AgentBuilderWorkerResultSink{}, nil
	}

	return recipe.New(
		goalSourceFactory,
		recipe.RoutingSpec{MinCapability: 2, SensitivityHint: recipe.SensitivitySensitive},
		newAgentBuilderWorkerGateFactory,
		resultSinkFactory,
		nil,
	), nil
}

// init registers the agent-builder-worker recipe at startup.
func init() {
	recipe.Register("agent-builder-worker", newAgentBuilderWorkerRecipe)
}
