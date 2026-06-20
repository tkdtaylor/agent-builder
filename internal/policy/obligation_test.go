package policy

import "testing"

// TC-072-04: vault_injection_floor obligation raises InjectionMode and never
// lowers it. All five sub-cases from the test spec table.
func TestVaultInjectionFloorObligation(t *testing.T) {
	cases := []struct {
		name     string
		initial  string
		value    string
		expected string
	}{
		{"empty raised to proxy", "", "proxy", "proxy"},
		{"env raised to proxy", "env", "proxy", "proxy"},
		{"proxy not lowered to env", "proxy", "env", "proxy"},
		{"proxy unchanged by proxy", "proxy", "proxy", "proxy"},
		{"empty raised to env", "", "env", "env"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			obs := []Obligation{{Type: ObligationVaultInjectionFloor, Value: tc.value}}
			got := RaiseInjectionFloor(tc.initial, obs)
			if got != tc.expected {
				t.Fatalf("RaiseInjectionFloor(%q, floor=%q) = %q, want %q", tc.initial, tc.value, got, tc.expected)
			}
		})
	}
}

// TC-072-04: applying the obligation only touches InjectionMode — a non-floor
// obligation present in the slice does not change the mode.
func TestRaiseInjectionFloorIgnoresOtherObligations(t *testing.T) {
	obs := []Obligation{
		{Type: ObligationTierSelect, Value: "gvisor"},
		{Type: "audit_emit", Value: "policy-decision"},
	}
	if got := RaiseInjectionFloor("env", obs); got != "env" {
		t.Fatalf("RaiseInjectionFloor with no floor obligation = %q, want unchanged env", got)
	}
}

// A non-string obligation value is ignored rather than panicking.
func TestRaiseInjectionFloorIgnoresNonStringValue(t *testing.T) {
	obs := []Obligation{{Type: ObligationVaultInjectionFloor, Value: 42}}
	if got := RaiseInjectionFloor("env", obs); got != "env" {
		t.Fatalf("RaiseInjectionFloor with non-string value = %q, want unchanged env", got)
	}
}

// TC-072-03: tier_select returns the obligation tier value.
func TestTierSelect(t *testing.T) {
	obs := []Obligation{{Type: ObligationTierSelect, Value: "gvisor"}}
	if got := TierSelect(obs); got != "gvisor" {
		t.Fatalf("TierSelect = %q, want gvisor", got)
	}
	if got := TierSelect(nil); got != "" {
		t.Fatalf("TierSelect(nil) = %q, want empty", got)
	}
	if got := TierSelect([]Obligation{{Type: ObligationTierSelect, Value: 7}}); got != "" {
		t.Fatalf("TierSelect with non-string value = %q, want empty", got)
	}
}
