package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"

	"github.com/tkdtaylor/agent-builder/internal/executor"
	"github.com/tkdtaylor/agent-builder/internal/registry"
	"github.com/tkdtaylor/agent-builder/internal/router"
)

// completerForEntry is the seam for constructing a single-shot Completer for a brain
// entry (ADR 059). It defaults to executor.CompleterForEntry; tests override it.
var completerForEntry = executor.CompleterForEntry

// runAsk implements `agent-builder ask [--entry <id>] <prompt>` — the general
// (non-coding) entrypoint. It selects a registry brain, constructs its single-shot
// Completer, and prints the raw answer to stdout. No worktree, no gate, no branch.
func runAsk(config Config, args []string) int {
	fs := flag.NewFlagSet("ask", flag.ContinueOnError)
	fs.SetOutput(config.Stderr)
	entryID := fs.String("entry", "", "registry entry id (brain) to ask; default: router selection")
	if err := fs.Parse(args); err != nil {
		return ExitUsage
	}

	prompt := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if prompt == "" {
		writef(config.Stderr, "usage: agent-builder ask [--entry <id>] <prompt>\n")
		return ExitUsage
	}

	entry, err := selectAskEntry(*entryID)
	if err != nil {
		writef(config.Stderr, "ask: %v\n", err)
		return ExitUsage
	}

	comp, err := completerForEntry(entry)
	if err != nil {
		writef(config.Stderr, "ask: %v\n", err)
		return ExitGeneric
	}

	answer, err := comp.Complete(context.Background(), entry, prompt)
	if err != nil {
		writef(config.Stderr, "ask: %v\n", err)
		return ExitGeneric
	}

	writef(config.Stdout, "%s\n", strings.TrimSpace(answer))
	return ExitOK
}

// buildBrainCatalog loads the registry brains from env; with an empty registry it
// falls back to the synthetic default Claude entry (same shape as
// internal/runtime.defaultClaudeEntry). Shared by the `ask` subcommand and the
// orchestrate answer path (ADR 059/060).
func buildBrainCatalog() (*registry.Catalog, error) {
	entries, err := registry.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("load registry: %w", err)
	}
	cat := registry.NewCatalog()
	if len(entries) == 0 {
		cat.RegisterEntry(registry.RegistryEntry{
			ID:             defaultCLIClaudeEntryID,
			Harness:        registry.HarnessClaudeCLI,
			CapabilityTier: 1,
			CostWeight:     1,
			Availability:   registry.Availability{Status: registry.AvailStatusAvailable},
		})
	} else {
		for _, e := range entries {
			cat.RegisterEntry(e)
		}
	}
	return cat, nil
}

// selectAskEntry resolves the brain entry to ask: an explicit --entry id, or the
// router's default selection.
func selectAskEntry(entryID string) (registry.RegistryEntry, error) {
	cat, err := buildBrainCatalog()
	if err != nil {
		return registry.RegistryEntry{}, err
	}

	if entryID != "" {
		entry, ok := cat.LookupEntry(entryID)
		if !ok {
			return registry.RegistryEntry{}, fmt.Errorf("unknown registry entry %q (configure it via AGENT_BUILDER_REGISTRY_<ID>_* or omit --entry)", entryID)
		}
		return entry, nil
	}

	entry, err := router.New(cat).Select(router.RoutingSpec{MinCapability: 1})
	if err != nil {
		return registry.RegistryEntry{}, fmt.Errorf("select brain: %w", err)
	}
	return entry, nil
}
