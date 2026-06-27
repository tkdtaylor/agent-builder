// Package recipe defines the pluggable agent recipe seam: the Recipe type,
// the RoutingSpec value type, and the in-process registry. The seam interfaces
// (GoalSource, ResultSink, Gate) live in internal/supervisor; recipe is a true
// leaf that depends only on supervisor and stdlib.
//
// A Recipe declares what seams an agent needs (via factory functions) and how
// it should be routed (capability tier, sensitivity hint via RoutingSpec).
// The routing spec does NOT bind a concrete executor — that is the router's
// responsibility (a deferred feature, task 095).
//
// Package recipe is a true leaf: it imports only internal/supervisor (for the
// seam interface types) plus stdlib. It imports NO concrete seam implementation
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

// SeamConfig is the narrow, leaf-defined accessor interface that seam factories
// receive at assembly time. runtime.Config satisfies it; the leaf names no
// runtime type. This is option (b) from ADR 044: a small, typed interface for
// config-flow that keeps the leaf pure.
type SeamConfig interface {
	TaskRoot() string
	PublishRemote() string
	GitToken() string
	GitHubToken() string
	GitCLI() string
	GitHubCLI() string
	Worktree() string
}

// RoutingSpec is a plain value type that declares what capability tier and
// sensitivity profile a recipe needs. The router (task 095) uses this to
// resolve to a concrete executor at dispatch.
type RoutingSpec struct {
	MinCapability   int         // minimum capability tier (e.g., 1 = basic, 2 = advanced)
	SensitivityHint Sensitivity // soft hint: none or sensitive
}

// GoalSourceFactory is a factory function that creates a GoalSource seam.
// It takes a SeamConfig and returns a goal source or an error.
type GoalSourceFactory func(cfg SeamConfig) (supervisor.GoalSource, error)

// GateFactory is a factory function that creates a new Gate instance. It is
// called once per task execution and takes no config (the production gate is
// built from compiled-in defaults). See ADR 044 §4.
type GateFactory func() supervisor.Gate

// ResultSinkFactory is a factory function that creates a ResultSink seam.
// It takes a SeamConfig and returns a result sink or an error.
type ResultSinkFactory func(cfg SeamConfig) (supervisor.ResultSink, error)

// Recipe is the pluggable agent definition. It declares what factories the agent
// needs (goal source, gate, result sink) and how it should be routed (capability
// and sensitivity hints via RoutingSpec).
//
// A Recipe is not directly executable. Instead, it is paired with a concrete
// Executor (determined at dispatch time by the router) that actually performs
// the work.
//
// The Name field is populated by SelectRecipe and carries the registered recipe
// name (the registry key is the single source of truth).
type Recipe struct {
	Name                string
	GoalSourceFactory   GoalSourceFactory
	RoutingSpec         RoutingSpec
	GateFactory         GateFactory
	ResultSinkFactory   ResultSinkFactory
	BlockWiring         map[string]interface{} // opaque config for block integration
}

// New constructs a Recipe from the provided seam factories. It validates that
// GoalSourceFactory, GateFactory, and ResultSinkFactory are non-nil (panics if
// any is nil) — a Recipe with missing seams cannot do useful work.
func New(
	goalSourceFactory GoalSourceFactory,
	routingSpec RoutingSpec,
	gateFactory GateFactory,
	resultSinkFactory ResultSinkFactory,
	blockWiring map[string]interface{},
) Recipe {
	if goalSourceFactory == nil {
		panic("recipe.New: GoalSourceFactory is nil — a Recipe must have a GoalSource")
	}
	if gateFactory == nil {
		panic("recipe.New: GateFactory is nil — a Recipe must have a Gate")
	}
	if resultSinkFactory == nil {
		panic("recipe.New: ResultSinkFactory is nil — a Recipe must have a ResultSink")
	}
	return Recipe{
		GoalSourceFactory:   goalSourceFactory,
		RoutingSpec:         routingSpec,
		GateFactory:         gateFactory,
		ResultSinkFactory:   resultSinkFactory,
		BlockWiring:         blockWiring,
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
// empty or unrecognized. The returned Recipe has its Name field set to the
// registered name (the registry key is the single source of truth).
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
	r, err := factory()
	if err != nil {
		return Recipe{}, err
	}

	// Stamp the recipe with its registered name.
	r.Name = name
	return r, nil
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
