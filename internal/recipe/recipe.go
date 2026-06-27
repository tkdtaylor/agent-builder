// Package recipe defines the pluggable agent recipe seam: the Recipe type,
// the IO seam interfaces (GoalSource, ResultSink), the RoutingSpec value type,
// and the in-process registry.
//
// A Recipe declares what one agent needs (capability tier, sensitivity hint)
// and the seam factories (goal source, gate, result sink) it uses. The routing
// spec does NOT bind a concrete executor — that is the router's responsibility
// (a deferred feature, task 095).
//
// Package recipe is a true leaf: it imports only internal/supervisor (for the
// Gate interface) plus stdlib. It imports NO concrete seam implementation
// (executor, tasksource, publisher, etc.), NO registry/router code, and NO
// vault/policy/secrets.
package recipe

import (
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// Sensitivity is a soft routing hint: whether the recipe needs sensitive
// resources (credentials, private data) or can run in an untrusted context.
type Sensitivity int

const (
	// SensitivityNone means the recipe can run in an untrusted environment.
	SensitivityNone Sensitivity = iota
	// SensitivitySensitive means the recipe handles sensitive data and should
	// only run in trusted/audited environments.
	SensitivitySensitive
)

// RoutingSpec is a plain value type that declares what capability tier and
// sensitivity profile a recipe needs. The router (task 095) uses this to
// resolve to a concrete executor at dispatch.
type RoutingSpec struct {
	MinCapability  int         // minimum capability tier (e.g., 1 = basic, 2 = advanced)
	SensitivityHint Sensitivity // soft hint: none or sensitive
}

// GoalSource is a seam interface for reading the task/goal that this agent
// must work on.
type GoalSource interface {
	// FetchGoal returns the task description or goal for the agent to pursue.
	// It is the responsibility of the implementation to return a valid,
	// actionable goal.
	FetchGoal() (string, error)
}

// ResultSink is a seam interface for writing the result of the agent's work
// back to persistent storage or a callback.
type ResultSink interface {
	// WriteResult persists the agent's result (success, failure, branch info,
	// logs). The implementation may block on I/O or network calls.
	WriteResult(result string) error
}

// Recipe is the pluggable agent definition. It declares what seams the agent
// needs (goal source, gate for verification, result sink) and how it should
// be routed (capability and sensitivity hints via RoutingSpec).
//
// A Recipe is not directly executable. Instead, it is paired with a concrete
// Executor (determined at dispatch time by the router) that actually performs
// the work.
type Recipe struct {
	GoalSource   GoalSource
	RoutingSpec  RoutingSpec
	GateFactory  GateFactory
	ResultSink   ResultSink
	BlockWiring  map[string]interface{} // opaque config for block integration
}

// GateFactory is a factory function that creates a new Gate instance. It is
// called once per task execution to obtain the verification gate.
type GateFactory func() supervisor.Gate

// New constructs a Recipe from the provided seam factories. It validates that
// GateFactory is non-nil (panics if nil).
func New(
	goalSource GoalSource,
	routingSpec RoutingSpec,
	gateFactory GateFactory,
	resultSink ResultSink,
	blockWiring map[string]interface{},
) Recipe {
	if gateFactory == nil {
		panic("recipe.New: GateFactory is nil — a Recipe must have a Gate")
	}
	return Recipe{
		GoalSource:  goalSource,
		RoutingSpec: routingSpec,
		GateFactory: gateFactory,
		ResultSink:  resultSink,
		BlockWiring: blockWiring,
	}
}

// recipeRegistry is the global registry of named recipes.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]RecipeFactory)
)

// RecipeFactory is a factory function that constructs a Recipe on demand.
type RecipeFactory func() (Recipe, error)

// Register adds a named recipe to the global registry. Panics if the name
// is already registered (deterministic and loud, not last-writer-wins).
func Register(name string, factory RecipeFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()

	if _, exists := registry[name]; exists {
		panic(fmt.Sprintf("recipe.Register: recipe %q is already registered", name))
	}
	registry[name] = factory
}

// SelectRecipe returns the named recipe, or a non-nil error if the name is
// empty or unrecognized.
func SelectRecipe(name string) (Recipe, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	if name == "" {
		return Recipe{}, errors.New("recipe.SelectRecipe: empty recipe name")
	}

	factory, exists := registry[name]
	if !exists {
		return Recipe{}, fmt.Errorf("recipe.SelectRecipe: recipe %q not found", name)
	}

	// Call the factory to get a fresh Recipe instance.
	return factory()
}

// ListRecipes returns the set of registered recipe names in stable,
// deterministic order (alphabetical).
func ListRecipes() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()

	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
