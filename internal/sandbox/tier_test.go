package sandbox

import "testing"

// TC-164-01: ValidTier accepts the empty string (backend default) plus the two
// known tiers, and rejects everything else, including wrong case, a real
// gVisor binary name that must not be aliased, and unnormalized whitespace.
func TestValidTier(t *testing.T) {
	cases := []struct {
		name string
		tier string
		want bool
	}{
		{name: "empty string means backend default", tier: "", want: true},
		{name: "bubblewrap", tier: "bubblewrap", want: true},
		{name: "gvisor", tier: "gvisor", want: true},
		{name: "wrong case is rejected", tier: "Gvisor", want: false},
		{name: "runsc binary name is not aliased to gvisor", tier: "runsc", want: false},
		{name: "nonsense value", tier: "nonsense", want: false},
		{name: "leading whitespace is not trimmed", tier: "  gvisor", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ValidTier(tc.tier)
			if got != tc.want {
				t.Fatalf("ValidTier(%q) = %v, want %v", tc.tier, got, tc.want)
			}
		})
	}
}

// TC-164-01: pin the exported constant values against the existing doc-comment
// values at internal/sandbox/run.go.
func TestTierConstants(t *testing.T) {
	if TierBubblewrap != "bubblewrap" {
		t.Fatalf("TierBubblewrap = %q, want %q", TierBubblewrap, "bubblewrap")
	}
	if TierGvisor != "gvisor" {
		t.Fatalf("TierGvisor = %q, want %q", TierGvisor, "gvisor")
	}
}
