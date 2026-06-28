package registry

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// TranslationProxySeam names the local-entry endpoint convention.
//
// A local entry uses HarnessClaudeCLI with its Endpoint pointing at a
// translation proxy rather than the Anthropic cloud API. The translation proxy
// presents an Anthropic-compatible endpoint over an OpenAI-API local inference
// server (the LiteLLM / claude-code-router pattern). The Claude Code CLI
// honors ANTHROPIC_BASE_URL, so redirecting it to the proxy drives a local
// model without introducing a new harness. Local entries carry no cloud auth
// (SecretRef == "", Budget.Limit == 0 — unlimited).
//
// Typical setup:
//
//	Ollama (local model) → LiteLLM or claude-code-router (translation proxy) → Claude CLI (ANTHROPIC_BASE_URL)
const TranslationProxySeam = "litellm/claude-code-router"

// localHarnessEntries lists entry IDs that use the local/translation-proxy pattern:
// Harness = HarnessClaudeCLI, SecretRef = "" (no cloud auth), Budget.Limit = 0 (unlimited).
// These entries set ANTHROPIC_BASE_URL to their Endpoint at dispatch time instead of
// injecting a cloud auth token.
var localHarnessEntries = map[string]struct{}{
	"local-qwen": {},
	"local":      {},
}

// LoadFromEnv reads well-known env-var prefixes and constructs enabled entries.
// Returns a slice of enabled entries, or an error if required fields are missing or malformed.
func LoadFromEnv() ([]RegistryEntry, error) {
	// Known entry IDs and their corresponding harness drivers
	knownEntries := map[string]HarnessDriver{
		"claude-oauth": HarnessClaudeCLI,
		"local-qwen":   HarnessClaudeCLI,
		"local":        HarnessClaudeCLI,
		"codex":        HarnessCodexCLI,
		"gemini":       HarnessGeminiCLI,
	}

	var entries []RegistryEntry

	for entryID, harness := range knownEntries {
		// Check if this entry is enabled
		enabledKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_ENABLED", envSafeID(entryID))
		enabledStr := os.Getenv(enabledKey)

		// If ENABLED is not set or explicitly false, skip this entry
		if enabledStr == "" || strings.ToLower(enabledStr) == "false" {
			continue
		}

		// Entry is enabled; verify required fields
		entry := RegistryEntry{
			ID:      entryID,
			Harness: harness,
		}

		// Load Endpoint (required)
		endpointKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_ENDPOINT", envSafeID(entryID))
		endpoint := os.Getenv(endpointKey)
		if endpoint == "" {
			return nil, fmt.Errorf("missing required field ENDPOINT for enabled entry %q", entryID)
		}
		entry.Endpoint = endpoint

		// Load SecretRef: required for cloud entries; optional (and expected empty) for local entries.
		// Local entries use the translation-proxy pattern (TranslationProxySeam): they point
		// ANTHROPIC_BASE_URL at a local translation proxy instead of using cloud auth tokens.
		secretRefKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_SECRET_REF", envSafeID(entryID))
		secretRef := os.Getenv(secretRefKey)
		_, isLocal := localHarnessEntries[entryID]
		if secretRef == "" && !isLocal {
			return nil, fmt.Errorf("missing required field SECRET_REF for enabled entry %q", entryID)
		}
		entry.SecretRef = secretRef

		// Load ModelID (required)
		modelKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_MODEL", envSafeID(entryID))
		model := os.Getenv(modelKey)
		if model == "" {
			return nil, fmt.Errorf("missing required field MODEL for enabled entry %q", entryID)
		}
		entry.ModelID = model

		// Load CapabilityTier (required, must be integer)
		tierKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_CAPABILITY_TIER", envSafeID(entryID))
		tierStr := os.Getenv(tierKey)
		if tierStr == "" {
			return nil, fmt.Errorf("missing required field CAPABILITY_TIER for enabled entry %q", entryID)
		}
		tier, err := strconv.Atoi(tierStr)
		if err != nil {
			return nil, fmt.Errorf("invalid CAPABILITY_TIER for entry %q: %q is not an integer", entryID, tierStr)
		}
		entry.CapabilityTier = tier

		// Load CostWeight (required, must be integer)
		costKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_COST_WEIGHT", envSafeID(entryID))
		costStr := os.Getenv(costKey)
		if costStr == "" {
			return nil, fmt.Errorf("missing required field COST_WEIGHT for enabled entry %q", entryID)
		}
		cost, err := strconv.Atoi(costStr)
		if err != nil {
			return nil, fmt.Errorf("invalid COST_WEIGHT for entry %q: %q is not an integer", entryID, costStr)
		}
		entry.CostWeight = cost

		// Load optional Budget fields
		budgetLimitKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_BUDGET_LIMIT", envSafeID(entryID))
		budgetLimitStr := os.Getenv(budgetLimitKey)
		if budgetLimitStr != "" {
			limit, err := strconv.Atoi(budgetLimitStr)
			if err != nil {
				return nil, fmt.Errorf("invalid BUDGET_LIMIT for entry %q: %q is not an integer", entryID, budgetLimitStr)
			}
			entry.Budget.Limit = limit
		}

		budgetWindowKey := fmt.Sprintf("AGENT_BUILDER_REGISTRY_%s_BUDGET_WINDOW", envSafeID(entryID))
		budgetWindowStr := os.Getenv(budgetWindowKey)
		if budgetWindowStr != "" {
			window, err := time.ParseDuration(budgetWindowStr)
			if err != nil {
				return nil, fmt.Errorf("invalid BUDGET_WINDOW for entry %q: %q is not a valid duration", entryID, budgetWindowStr)
			}
			entry.Budget.Window = window
		}

		// Initialize default availability state
		entry.Availability = Availability{
			Status:  AvailStatusAvailable,
			ResetAt: time.Time{},
		}

		entries = append(entries, entry)
	}

	return entries, nil
}

// envSafeID converts an entry ID to a safe env var suffix (e.g., "claude-oauth" -> "CLAUDE_OAUTH")
func envSafeID(id string) string {
	return strings.ToUpper(strings.ReplaceAll(id, "-", "_"))
}
