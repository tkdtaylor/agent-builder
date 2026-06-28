package router

import (
	"fmt"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/secrets"
	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// ErrUnknownHarness is returned by ResolveExecutor when an entry names a harness
// driver the router does not know how to construct.
var ErrUnknownHarness = fmt.Errorf("router: unknown harness driver")

// ResolveExecutor selects an entry for the dispatch (via Select) and constructs
// the concrete supervisor.Executor that backs it, using the entry's harness
// driver. This is the executor-side boundary ADR 043 describes: the router hands
// back a supervisor.Executor; the supervisor sees a seam, not a router.
//
// secretSource brokers each entry's per-provider credential (vault in production,
// env in tests) and worktree is the on-disk path the harness operates on. The
// returned executor satisfies supervisor.Executor.
//
// ResolveExecutor returns the selected entry alongside the executor so the caller
// can feed the entry ID back into OnGateFailure / OnQuotaExhausted to drive the
// two fallback axes across attempts.
func (r *Router) ResolveExecutor(spec RoutingSpec, secretSource secrets.SecretSource, worktree string) (supervisor.Executor, registry.RegistryEntry, error) {
	entry, err := r.Select(spec)
	if err != nil {
		return nil, registry.RegistryEntry{}, err
	}

	exec, err := buildExecutor(entry, secretSource, worktree)
	if err != nil {
		return nil, registry.RegistryEntry{}, err
	}
	return exec, entry, nil
}

// buildExecutor constructs the concrete harness adapter for an entry. One harness
// driver backs many entries (ADR 043): the Claude CLI harness drives both cloud
// Claude and local entries (the latter via the translation-proxy endpoint).
func buildExecutor(entry registry.RegistryEntry, secretSource secrets.SecretSource, worktree string) (supervisor.Executor, error) {
	switch entry.Harness {
	case registry.HarnessClaudeCLI:
		return executor.NewClaudeCLIFromEntry(entry, secretSource, worktree), nil
	case registry.HarnessCodexCLI:
		return executor.NewCodexCLI(entry, secretSource, worktree), nil
	case registry.HarnessGeminiCLI:
		return executor.NewGeminiCLI(entry, secretSource, worktree), nil
	default:
		return nil, fmt.Errorf("%w: %q (entry %q)", ErrUnknownHarness, entry.Harness, entry.ID)
	}
}
