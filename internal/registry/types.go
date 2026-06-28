package registry

import "time"

// HarnessDriver discriminates which executor harness runs the loop.
type HarnessDriver string

const (
	// HarnessClaudeCLI is the Claude Code CLI harness.
	HarnessClaudeCLI HarnessDriver = "claude-cli"
	// HarnessCodexCLI is the Codex CLI harness.
	HarnessCodexCLI HarnessDriver = "codex-cli"
	// HarnessGeminiCLI is the Google Gemini CLI harness.
	HarnessGeminiCLI HarnessDriver = "gemini-cli"
)

// String returns the human-readable name of the harness driver.
func (h HarnessDriver) String() string {
	switch h {
	case HarnessClaudeCLI:
		return "claude-cli"
	case HarnessCodexCLI:
		return "codex-cli"
	case HarnessGeminiCLI:
		return "gemini-cli"
	default:
		return string(h)
	}
}

// QuotaBudget represents a quota cap over a rolling window.
// Zero Limit means unlimited (no cap).
type QuotaBudget struct {
	Limit  int           // maximum number of dispatches over the window
	Window time.Duration // rolling time window
}

// AvailStatus discriminates availability states.
type AvailStatus string

const (
	// AvailStatusAvailable indicates the entry is currently available.
	AvailStatusAvailable AvailStatus = "available"
	// AvailStatusExhausted indicates the entry is exhausted until ResetAt.
	AvailStatusExhausted AvailStatus = "exhausted"
)

// Availability represents the current availability state of an entry.
type Availability struct {
	Status  AvailStatus // available or exhausted
	ResetAt time.Time   // when the entry becomes available again (for exhausted entries)
}

// RegistryEntry represents a single executor in the registry.
type RegistryEntry struct {
	ID             string        // stable handle, e.g. "claude-oauth", "local-qwen"
	Harness        HarnessDriver // which harness runs the loop
	CapabilityTier int           // ordered: higher = stronger
	CostWeight     int           // relative cost per dispatch; lower = cheaper
	ModelID        string        // model identifier
	Endpoint       string        // base URL the harness points at
	SecretRef      string        // which vault secret to resolve (never the secret itself)
	Budget         QuotaBudget   // configured cap over a rolling window, or zero for unlimited
	Usage          int           // running tally against Budget
	Availability   Availability  // available or exhausted-until ResetAt
}
