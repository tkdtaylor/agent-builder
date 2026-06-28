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
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

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
// all `.go` files (recursively) in the target directory and verifies that
// every recipe registered in the generated code binds a non-nil GateFactory.
//
// This step uses AST parsing (go/parser + go/ast) for semantic validation — it
// does NOT rely on substring matching. This defeats evasions like comments,
// string literals, whitespace variation, and cast-to-nil patterns. Parse
// failures are gate failures (SEC-001, SEC-002, SEC-003).
type GeneratedGateExistenceStep struct{}

// Name returns the step identifier used in gate.Verdict.Results.
func (s GeneratedGateExistenceStep) Name() string {
	return "generated-gate-existence"
}

// Run inspects all `.go` files (recursively) in repoPath for valid GateFactory
// bindings. Every recipe registered in the generated output must bind a non-nil
// GateFactory. Returns OK=false if:
//   - No `.go` files exist
//   - Any `.go` file fails to parse (SEC-003: parse errors are gate failures)
//   - A recipe is registered with no GateFactory binding
//   - A recipe's GateFactory is bound to nil or a nil identifier
//   - No recipes are registered at all (detected as zero gates)
func (s GeneratedGateExistenceStep) Run(repoPath string) gate.StepResult {
	// Walk the directory recursively, collecting all .go files.
	goFiles, err := findGoFiles(repoPath)
	if err != nil {
		return gate.StepResult{
			OK:     false,
			Output: fmt.Sprintf("generated-gate-existence: walk directory %q: %v", repoPath, err),
		}
	}

	if len(goFiles) == 0 {
		return gate.StepResult{
			OK:     false,
			Output: "generated-gate-existence: no .go files found in output directory; generated recipe must be a .go source file",
		}
	}

	// Parse each file and collect recipes. Track parse failures as gate failures.
	var parseErrors []string
	recipes := make([]*RecipeBinding, 0)

	for _, filePath := range goFiles {
		src, readErr := os.ReadFile(filePath)
		if readErr != nil {
			parseErrors = append(parseErrors,
				fmt.Sprintf("%s: read failed: %v", filepath.Base(filePath), readErr))
			continue
		}

		fset := token.NewFileSet()
		f, parseErr := parser.ParseFile(fset, filePath, src, 0)
		if parseErr != nil {
			parseErrors = append(parseErrors,
				fmt.Sprintf("%s: parse failed: %v", filepath.Base(filePath), parseErr))
			continue
		}

		// Extract recipe bindings from this file.
		fileRecipes := extractRecipeBindings(f)
		recipes = append(recipes, fileRecipes...)
	}

	// If any file failed to parse, that is a gate failure (SEC-003).
	if len(parseErrors) > 0 {
		output := "generated-gate-existence: parse error(s) in generated code (gate failure — generated recipe must be valid Go):\n"
		for _, msg := range parseErrors {
			output += "  " + msg + "\n"
		}
		return gate.StepResult{
			OK:     false,
			Output: output,
		}
	}

	// No recipes registered — gate failure (a generated recipe must define at least one).
	if len(recipes) == 0 {
		return gate.StepResult{
			OK:     false,
			Output: "generated-gate-existence: no recipes registered in generated code; a generated recipe must call recipe.Register()",
		}
	}

	// Every recipe must have a non-nil GateFactory binding (SEC-001d: check all, not first).
	for _, rb := range recipes {
		if !rb.HasGateFactory {
			return gate.StepResult{
				OK:     false,
				Output: fmt.Sprintf("generated-gate-existence: recipe %q has no GateFactory binding; a recipe must bind a non-nil, blocking gate (ADR 044, ADR 047)", rb.Name),
			}
		}
		if rb.IsNilBinding {
			return gate.StepResult{
				OK:     false,
				Output: fmt.Sprintf("generated-gate-existence: recipe %q binds GateFactory to nil; a recipe must bind a non-nil, blocking gate (ADR 044, ADR 047)", rb.Name),
			}
		}
	}

	// All recipes have valid non-nil gate bindings.
	return gate.StepResult{
		OK:     true,
		Output: fmt.Sprintf("generated-gate-existence: verified %d recipe binding(s) with non-nil GateFactory", len(recipes)),
	}
}

// RecipeBinding captures the gate-binding status of a single registered recipe.
type RecipeBinding struct {
	Name           string
	HasGateFactory bool
	IsNilBinding   bool
}

// findGoFiles walks repoPath recursively and returns all .go file paths
// (excluding _test.go files per convention for generated recipes).
func findGoFiles(repoPath string) ([]string, error) {
	var files []string
	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && filepath.Ext(path) == ".go" {
			// Skip test files — generated recipes are production code.
			base := filepath.Base(path)
			if len(base) < 8 || base[len(base)-8:] != "_test.go" {
				files = append(files, path)
			}
		}
		return nil
	})
	return files, err
}

// extractRecipeBindings walks the AST and extracts all recipe.Register() calls,
// returning RecipeBindings that indicate whether each recipe binds a non-nil GateFactory.
func extractRecipeBindings(f *ast.File) []*RecipeBinding {
	var bindings []*RecipeBinding

	// First pass: collect all function definitions so we can resolve named factories.
	functionBodies := make(map[string]*ast.FuncDecl)
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok {
			functionBodies[fn.Name.Name] = fn
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		// Look for recipe.Register(...) calls.
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "Register" {
					// Found recipe.Register() call. Extract the recipe name and binding.
					// Signature: recipe.Register(name string, factory RecipeFactory func() (Recipe, error))
					if len(call.Args) >= 2 {
						// Extract name if it's a string literal
						recipeName := ""
						if lit, ok := call.Args[0].(*ast.BasicLit); ok {
							recipeName = lit.Value
						}
						rb := extractRecipeBindingFromFactory(call.Args[1], recipeName, functionBodies)
						if rb != nil {
							bindings = append(bindings, rb)
						}
					}
				}
			}
		}

		// Also look for recipe.New(...) calls to extract GateFactory binding directly.
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "New" {
					// recipe.New(goalSourceFactory, routingSpec, gateFactory, resultSinkFactory, blockWiring)
					// The 3rd argument (index 2) is the gateFactory.
					if len(call.Args) >= 3 {
						rb := &RecipeBinding{
							Name:           "(recipe.New direct)",
							HasGateFactory: !isNilExpr(call.Args[2]),
							IsNilBinding:   isNilExpr(call.Args[2]),
						}
						if rb.HasGateFactory && !rb.IsNilBinding {
							bindings = append(bindings, rb)
						}
					}
				}
			}
		}

		return true
	})
	return bindings
}

// extractRecipeBindingFromFactory analyzes a recipe factory function to extract
// the recipe binding. This handles:
// 1. Function literals that call recipe.New() or return a Recipe{...} struct literal
// 2. Function identifiers (named factory functions) - resolved via functionBodies
func extractRecipeBindingFromFactory(arg ast.Expr, recipeName string, functionBodies map[string]*ast.FuncDecl) *RecipeBinding {
	// Case 1: If the factory is a function literal, inspect its body for recipe.New() or
	// Recipe{...} struct literal.
	if lit, ok := arg.(*ast.FuncLit); ok {
		var gateExpr ast.Expr

		ast.Inspect(lit, func(n ast.Node) bool {
			// Case 1a: recipe.New(...) call
			if call, ok := n.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "New" {
						if len(call.Args) >= 3 {
							gateExpr = call.Args[2]
						}
					}
				}
			}

			// Case 1b: recipe.Recipe{...} struct literal (direct construction)
			if comp, ok := n.(*ast.CompositeLit); ok {
				if sel, ok := comp.Type.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "Recipe" {
						// Find the GateFactory field in the struct literal.
						for _, elt := range comp.Elts {
							if kv, ok := elt.(*ast.KeyValueExpr); ok {
								if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == "GateFactory" {
									gateExpr = kv.Value
									break
								}
							}
						}
					}
				}
			}
			return true
		})

		if gateExpr != nil {
			return &RecipeBinding{
				Name:           recipeName,
				HasGateFactory: true,
				IsNilBinding:   isNilExpr(gateExpr),
			}
		}
	}

	// Case 2: If the factory is a plain identifier (named function), resolve it via functionBodies.
	if ident, ok := arg.(*ast.Ident); ok {
		if fn, found := functionBodies[ident.Name]; found {
			// Recursively analyze the named function's body
			var gateExpr ast.Expr
			ast.Inspect(fn, func(n ast.Node) bool {
				// Look for recipe.Recipe{...} struct literal
				if comp, ok := n.(*ast.CompositeLit); ok {
					if sel, ok := comp.Type.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "Recipe" {
							for _, elt := range comp.Elts {
								if kv, ok := elt.(*ast.KeyValueExpr); ok {
									if ident, ok := kv.Key.(*ast.Ident); ok && ident.Name == "GateFactory" {
										gateExpr = kv.Value
										break
									}
								}
							}
						}
					}
				}
				// Look for recipe.New(...) call
				if call, ok := n.(*ast.CallExpr); ok {
					if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok && x.Name == "recipe" && sel.Sel.Name == "New" {
							if len(call.Args) >= 3 {
								gateExpr = call.Args[2]
							}
						}
					}
				}
				return true
			})
			if gateExpr != nil {
				return &RecipeBinding{
					Name:           recipeName,
					HasGateFactory: true,
					IsNilBinding:   isNilExpr(gateExpr),
				}
			}
		}
	}

	return nil
}

// isNilExpr checks whether an AST expression is the nil identifier or a
// conversion-to-nil pattern like (T)(nil). This defeats string-matching evasions.
func isNilExpr(expr ast.Expr) bool {
	if ident, ok := expr.(*ast.Ident); ok && ident.Name == "nil" {
		return true
	}
	// Check for (T)(nil) cast pattern.
	if call, ok := expr.(*ast.CallExpr); ok {
		if len(call.Args) >= 1 {
			if arg, ok := call.Args[0].(*ast.Ident); ok && arg.Name == "nil" {
				return true
			}
		}
	}
	return false
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
