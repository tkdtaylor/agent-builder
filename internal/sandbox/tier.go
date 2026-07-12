package sandbox

// Recognized exec-sandbox execution tiers. These are the only values the
// host-side gate accepts on the tier_select obligation; anything else is a
// fail-closed halt (see internal/runtime's decideGate).
const (
	TierBubblewrap = "bubblewrap"
	TierGvisor     = "gvisor"
)

// ValidTier reports whether tier is a recognized exec-sandbox execution tier.
// The empty string is valid (it means "backend default").
func ValidTier(tier string) bool {
	switch tier {
	case "", TierBubblewrap, TierGvisor:
		return true
	default:
		return false
	}
}
