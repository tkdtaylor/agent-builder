// Package planner provides the LLM-backed Planner concrete (ADR 046 §6) behind the
// stable orchestrator.Planner seam. It decomposes a free-form human goal into an
// ordered orchestrator.Plan by routing a decomposition prompt through a model.
//
// Import boundary (REQ-100-04 / F-014). This package reaches a model through the
// router/registry path (internal/router, internal/registry) but MUST NOT import
// internal/executor directly — the same bright line F-010 enforces on
// internal/orchestrator. The model is reached via the narrow ExecutorResolver +
// Invoker seams defined here; the *router.Router (which itself imports
// internal/executor) satisfies ExecutorResolver only at the wiring layer
// (internal/cli), where the executor import already lives. The planner package
// never sees internal/executor. F-014 (make fitness-llm-planner-no-executor)
// asserts this direct-import invariant.
package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/orchestrator"
	"github.com/tkdtaylor/agent-builder/internal/recipe"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// ExecutorResolver resolves a model for decomposition. It returns the registry
// entry the router selected for the routing spec, never an *executor.Executor —
// this is the seam that keeps internal/executor off this package's import graph
// (REQ-100-04). *router.Router satisfies it via a thin adapter at the wiring layer.
type ExecutorResolver interface {
	Resolve(ctx context.Context, spec router.RoutingSpec) (registry.RegistryEntry, error)
}

// Invoker sends the decomposition prompt to the resolved model and returns the raw
// text response. It is the narrow model-invocation seam (NOT the full
// Executor.Run agentic loop — decomposition returns data, not a branch). The
// production wiring (internal/cli) adapts the router's chosen harness to this
// function; tests supply a stub that returns canned text. The prompt always
// carries the goal text and the recipe catalog (see buildPrompt).
type Invoker func(ctx context.Context, entry registry.RegistryEntry, prompt string) (string, error)

// ErrEmptyResponse is returned when the model returns no parseable sub-goal — an
// empty string, whitespace, or text with no recognized "<recipe>: <spec>" line.
// The LLMPlanner fails closed here (REQ-100-03): it never emits a zero-sub-goal
// Plan, which the orchestrator would reject anyway, and never an empty-repo plan.
var ErrEmptyResponse = errors.New("planner: model returned no parseable sub-goal")

// defaultMinCapability is the routing-spec capability floor the LLMPlanner asks for
// when decomposing. Decomposition is a comprehension task, not code authoring, so a
// modest floor is sufficient; the router picks the cheapest eligible entry at or
// above it.
const defaultMinCapability = 1

// LLMPlanner is the LLM-backed Planner concrete (ADR 046 §6). It satisfies
// orchestrator.Planner: Plan resolves a model through the resolver, sends a
// decomposition prompt via the invoker, and parses the model's structured-line
// response into an orchestrator.Plan. It fails closed on an empty/unparseable
// response or a resolver error (REQ-100-03) and never emits a zero-sub-goal Plan.
type LLMPlanner struct {
	resolver ExecutorResolver
	invoke   Invoker
	// recipes is the set of recipe names the line parser treats as a "<recipe>:"
	// prefix and that the prompt advertises to the model. It is captured at
	// construction from recipe.ListRecipes() so the parser is closed over the
	// catalog (a free-form "word:" that is not a known recipe is kept as spec text,
	// mirroring StructuredPlanner's known-recipe guard).
	recipes []string
	// known is the set form of recipes for O(1) prefix lookup.
	known map[string]bool
}

// Compile-time assertion: LLMPlanner satisfies the same orchestrator.Planner
// interface StructuredPlanner does (REQ-100-01 / TC-100-01).
var _ orchestrator.Planner = (*LLMPlanner)(nil)

// New constructs an LLMPlanner over the given resolver and invoker. The recipe
// catalog is read from recipe.ListRecipes() at construction (the prompt advertises
// it and the parser uses it as the known-recipe set). A nil resolver or invoker is
// a programmer error and panics — the seams are required.
func New(resolver ExecutorResolver, invoke Invoker) *LLMPlanner {
	if resolver == nil {
		panic("planner.New: nil ExecutorResolver")
	}
	if invoke == nil {
		panic("planner.New: nil Invoker")
	}
	names := recipe.ListRecipes()
	known := make(map[string]bool, len(names))
	for _, n := range names {
		known[n] = true
	}
	return &LLMPlanner{
		resolver: resolver,
		invoke:   invoke,
		recipes:  names,
		known:    known,
	}
}

// Plan decomposes the goal into an ordered plan by routing a decomposition prompt
// through the resolved model (ADR 046 §6). It fails closed: a resolver error, an
// invoker error, or a response yielding no valid sub-goal all return a non-nil
// error and the zero Plan — never a partial or zero-sub-goal Plan (REQ-100-03).
func (p *LLMPlanner) Plan(goal supervisor.Task) (orchestrator.Plan, error) {
	ctx := context.Background()

	entry, err := p.resolver.Resolve(ctx, router.RoutingSpec{
		MinCapability:   defaultMinCapability,
		SensitivityHint: router.SensitivityNone,
	})
	if err != nil {
		return orchestrator.Plan{}, fmt.Errorf("planner: resolve model: %w", err)
	}

	prompt := p.buildPrompt(goal.Spec)
	response, err := p.invoke(ctx, entry, prompt)
	if err != nil {
		return orchestrator.Plan{}, fmt.Errorf("planner: invoke model: %w", err)
	}

	subGoals, err := p.parse(goal, response)
	if err != nil {
		return orchestrator.Plan{}, err
	}

	return orchestrator.Plan{
		Goal:     goal.Spec,
		GoalID:   goal.ID,
		SubGoals: subGoals,
	}, nil
}

// parse turns the model's structured-line response into sub-goals. Each non-blank,
// non-comment line is parsed with the same "<recipe>: <spec>" tokenizer
// StructuredPlanner uses, extended with "| target_repo=<repo>" / "| sink=<sink>"
// metadata extraction. Own-repo sub-goals are filtered defensively here so the
// orchestrator never even sees a dispatch targeting the own-repo (REQ-100-02); the
// orchestrator's decideSpawnWorker bright line remains the second, independent
// guard. If no valid sub-goal survives, parse fails closed (REQ-100-03).
func (p *LLMPlanner) parse(goal supervisor.Task, response string) ([]orchestrator.SubGoal, error) {
	lines := strings.Split(response, "\n")
	var subGoals []orchestrator.SubGoal
	idx := 0
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		body, targetRepo, sink := splitMetadata(line)
		recipeName, spec, ok := p.splitLine(body)
		if !ok || spec == "" {
			// Fail-closed on a model response: a line that does not name a known
			// recipe is NOT silently collapsed onto the default recipe (unlike
			// StructuredPlanner, which decomposes goal TEXT). Garbage prose must
			// not become a sub-goal (REQ-100-03 / TC-100-03 B).
			continue
		}

		sub := orchestrator.SubGoal{
			RecipeName: recipeName,
			Task: supervisor.Task{
				ID:   subGoalID(goal.ID, idx),
				Repo: goal.Repo,
				Spec: spec,
			},
			TargetRepo: targetRepo,
			Sink:       sink,
		}

		// Defensive own-repo filter (REQ-100-02 / TC-100-02): a sub-goal whose
		// target repo or sink is the agent-builder own-repo is dropped here so it
		// is never dispatched. The orchestrator's decideSpawnWorker is the second,
		// independent guard (belt-and-suspenders, ADR 050 §2).
		if targetsOwnRepo(sub) {
			continue
		}

		subGoals = append(subGoals, sub)
		idx++
	}

	if len(subGoals) == 0 {
		return nil, fmt.Errorf("%w (response had %d line(s))", ErrEmptyResponse, len(lines))
	}
	return subGoals, nil
}

// splitLine separates a "<recipe>: <spec>" body into a recipe name and spec text,
// using the same tokenizer rules as orchestrator.StructuredPlanner.splitLine: a
// recipe prefix must be a single token followed by non-empty spec text. It differs
// in the fail-closed direction: the prefix MUST name a known recipe (ok == false
// otherwise). Unlike StructuredPlanner — which decomposes free-form goal text and
// collapses an unrecognized line onto the default recipe — the LLMPlanner parses a
// MODEL response and must reject prose that names no known recipe rather than
// silently spawning a default worker on garbage (REQ-100-03 / TC-100-03 B). When no
// recipe catalog is registered, the prefix is accepted as-is (the parser cannot
// validate against an empty set).
func (p *LLMPlanner) splitLine(body string) (recipeName, spec string, ok bool) {
	colon := strings.IndexByte(body, ':')
	if colon <= 0 {
		return "", "", false
	}
	candidate := strings.TrimSpace(body[:colon])
	rest := strings.TrimSpace(body[colon+1:])
	if candidate == "" || strings.ContainsAny(candidate, " \t") || rest == "" {
		return "", "", false
	}
	if len(p.known) > 0 && !p.known[candidate] {
		return "", "", false
	}
	return candidate, rest, true
}

// buildPrompt assembles the decomposition prompt: the goal text plus the catalog of
// available recipe names and the structured-line output format the parser expects.
// Prompt engineering beyond this minimum is out of scope (task 100 Out of scope).
func (p *LLMPlanner) buildPrompt(goalText string) string {
	var b strings.Builder
	b.WriteString("Decompose the following goal into an ordered list of sub-goals.\n")
	b.WriteString("Return ONE sub-goal per line in this exact format:\n")
	b.WriteString("  <recipe-name>: <spec text> [| target_repo=<repo> | sink=<sink>]\n")
	b.WriteString("Use only these recipe names:\n")
	if len(p.recipes) == 0 {
		b.WriteString("  (none registered — use the default recipe)\n")
	}
	for _, r := range p.recipes {
		b.WriteString("  - ")
		b.WriteString(r)
		b.WriteString("\n")
	}
	b.WriteString("Goal:\n")
	b.WriteString(goalText)
	b.WriteString("\n")
	return b.String()
}

// splitMetadata separates the "| target_repo=<repo> | sink=<sink>" trailing
// metadata fields from the sub-goal body. Fields may appear in any order and are
// optional. The returned body is the line with all recognized metadata segments
// removed and trimmed; unrecognized "| ..." segments are left on the body (so a
// spec text legitimately containing a pipe is not silently dropped).
func splitMetadata(line string) (body, targetRepo, sink string) {
	parts := strings.Split(line, "|")
	body = strings.TrimSpace(parts[0])
	var leftover []string
	for _, seg := range parts[1:] {
		seg = strings.TrimSpace(seg)
		switch {
		case strings.HasPrefix(seg, "target_repo="):
			targetRepo = strings.TrimSpace(strings.TrimPrefix(seg, "target_repo="))
		case strings.HasPrefix(seg, "sink="):
			sink = strings.TrimSpace(strings.TrimPrefix(seg, "sink="))
		default:
			leftover = append(leftover, seg)
		}
	}
	if len(leftover) > 0 {
		body = strings.TrimSpace(body + " | " + strings.Join(leftover, " | "))
	}
	return body, targetRepo, sink
}

// targetsOwnRepo reports whether a sub-goal's target repo or sink is the
// agent-builder own-repo. It mirrors the orchestrator's runtime bright-line
// predicate so the planner can filter own-repo sub-goals defensively before they
// reach dispatch (REQ-100-02). It uses orchestrator.CanonicalizeRepo so every
// path form (https://, git@, .git suffix, case, trailing slash) is caught, and
// fails closed (treats an uncanonicalizable non-empty value as a match).
func targetsOwnRepo(sub orchestrator.SubGoal) bool {
	canonOwn := orchestrator.CanonicalizeRepo(orchestrator.OwnRepo)
	for _, target := range []string{sub.Task.Repo, sub.TargetRepo, sub.Sink} {
		if target == "" {
			continue
		}
		canonical := orchestrator.CanonicalizeRepo(target)
		if canonical == "" || canonical == canonOwn {
			return true
		}
	}
	return false
}

// subGoalID derives a stable sub-goal Task ID from the goal ID and sub-goal index,
// matching StructuredPlanner's scheme ("<goalID>-<idx>").
func subGoalID(goalID string, idx int) string {
	if goalID == "" {
		goalID = "goal"
	}
	return fmt.Sprintf("%s-%d", goalID, idx)
}
