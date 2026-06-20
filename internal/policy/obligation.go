package policy

// Obligation type identifiers (ADR 038 obligation→seam map).
const (
	// ObligationTierSelect sets the exec-sandbox execution tier
	// (sandbox.Request.Tier). Value is the tier name (e.g. "gvisor").
	ObligationTierSelect = "tier_select"

	// ObligationVaultInjectionFloor raises sandbox.RunWiring.InjectionMode to a
	// stricter floor (raise-only). Value is the floor ("env" or "proxy").
	ObligationVaultInjectionFloor = "vault_injection_floor"

	// ObligationAuditEmit triggers emission of an ActionPolicyDecision audit
	// event on the configured audit.Sink (task 073). Value is bool true.
	ObligationAuditEmit = "audit_emit"
)

// injectionRank orders the InjectionMode values from weakest to strongest so
// the vault_injection_floor obligation can raise but never lower. Unknown
// values rank below "" (treated as no protection) so they can never silently
// outrank a real mode.
func injectionRank(mode string) int {
	switch mode {
	case "proxy":
		return 2
	case "env":
		return 1
	case "":
		return 0
	default:
		return -1
	}
}

// TierSelect returns the tier named by the first tier_select obligation in the
// slice, or "" when none is present. The value is coerced from any to string;
// a non-string value yields "".
func TierSelect(obligations []Obligation) string {
	for _, ob := range obligations {
		if ob.Type != ObligationTierSelect {
			continue
		}
		if s, ok := ob.Value.(string); ok {
			return s
		}
	}
	return ""
}

// AuditEmit reports whether the audit_emit obligation is present with value true
// in the obligations slice (task 073). Returns false when the obligation is absent,
// the value is not a bool, or the bool is false.
func AuditEmit(obligations []Obligation) bool {
	for _, ob := range obligations {
		if ob.Type != ObligationAuditEmit {
			continue
		}
		if b, ok := ob.Value.(bool); ok && b {
			return true
		}
	}
	return false
}

// RaiseInjectionFloor applies any vault_injection_floor obligations to current,
// returning the resulting InjectionMode. The floor is raise-only: a stricter
// obligation value replaces current; an equal-or-weaker value is ignored.
// This guarantees a policy obligation can never weaken vault protection the
// caller already configured (ADR 036 / ADR 038 raise-only invariant).
func RaiseInjectionFloor(current string, obligations []Obligation) string {
	result := current
	for _, ob := range obligations {
		if ob.Type != ObligationVaultInjectionFloor {
			continue
		}
		floor, ok := ob.Value.(string)
		if !ok {
			continue
		}
		if injectionRank(floor) > injectionRank(result) {
			result = floor
		}
	}
	return result
}
