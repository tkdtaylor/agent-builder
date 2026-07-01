package authz

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TC-151-08: the approved-sender store round-trips across a reload (the persistence
// proof). A store written by one instance and reloaded by an INDEPENDENT second
// instance from the same path has an identical approved set; file is 0600; missing
// file → graceful empty store; malformed file → Load error.
func TestTC151_08_StoreRoundTripsAcrossReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "approved.json")

	// Writer instance.
	w := NewStore(path)
	if err := w.Add("42"); err != nil {
		t.Fatalf("Add(42): %v", err)
	}
	if err := w.Add("1001"); err != nil {
		t.Fatalf("Add(1001): %v", err)
	}
	if err := w.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// 0600 permission assertion.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("store file perm = %o, want 0600", perm)
	}

	// On-disk bytes parse as JSON.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("on-disk store is not valid JSON: %v (bytes: %s)", err, data)
	}

	// Independent reader instance (fresh object, not the writer).
	r := NewStore(path)
	if err := r.Load(); err != nil {
		t.Fatalf("reader Load: %v", err)
	}
	for _, id := range []string{"42", "1001"} {
		ok, err := r.Contains(id)
		if err != nil {
			t.Fatalf("Contains(%s): %v", id, err)
		}
		if !ok {
			t.Errorf("reloaded store missing approved ID %s", id)
		}
	}
	if r.Len() != 2 {
		t.Errorf("reloaded store Len = %d, want 2", r.Len())
	}
	// A non-approved ID must NOT be present.
	if ok, _ := r.Contains("99"); ok {
		t.Errorf("reloaded store unexpectedly contains 99")
	}
}

// TC-151-08 edge: a missing file on Load is graceful absence (empty store, no error).
func TestTC151_08_MissingFileGracefulAbsence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "does-not-exist.json")

	s := NewStore(path)
	if err := s.Load(); err != nil {
		t.Fatalf("Load on missing file returned error, want graceful absence: %v", err)
	}
	if s.Len() != 0 {
		t.Errorf("missing-file store Len = %d, want 0", s.Len())
	}
	if ok, _ := s.Contains("42"); ok {
		t.Errorf("missing-file store should approve nobody")
	}
}

// TC-151-08 edge: a malformed JSON file on Load is a fail-fast error (distinguishes
// "absent" from "corrupt" so an operator notices).
func TestTC151_08_MalformedFileLoadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "corrupt.json")
	if err := os.WriteFile(path, []byte("{ this is not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	s := NewStore(path)
	if err := s.Load(); err == nil {
		t.Fatal("Load on malformed file returned nil error, want fail-fast error")
	}
}

// TC-151-11: sender-ID normalization prevents a trivial bypass or duplicate-entry
// split. Adding 42, "42", "042", " 42 " all normalize to ONE stored entry; cross-format
// Contains checks succeed both directions; a non-numeric ID is rejected with an error.
func TestTC151_11_NormalizationPreventsBypassAndDuplicates(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "s.json"))

	for _, variant := range []string{"42", "42", "042", " 42 "} {
		if err := s.Add(variant); err != nil {
			t.Fatalf("Add(%q): %v", variant, err)
		}
	}
	// All four variants collapse to a single canonical entry.
	if got := s.Len(); got != 1 {
		t.Errorf("after adding 4 format-variants of 42, Len = %d, want 1", got)
	}

	// Cross-format Contains succeeds in both directions: store holds 42, query "042".
	ok, err := s.Contains("042")
	if err != nil {
		t.Fatalf(`Contains("042"): %v`, err)
	}
	if !ok {
		t.Error(`Contains("042") = false, want true (store holds 42)`)
	}
	// And a whitespace-padded query.
	if ok, _ := s.Contains(" 42 "); !ok {
		t.Error(`Contains(" 42 ") = false, want true`)
	}
	// A bare numeric int form via the canonical string.
	if ok, _ := s.Contains("42"); !ok {
		t.Error(`Contains("42") = false, want true`)
	}

	// A non-numeric ID is rejected at the normalize step, never coerced to 0/wildcard.
	if _, err := s.Contains("abc"); err == nil {
		t.Error(`Contains("abc") returned nil error, want rejection`)
	}
	if err := s.Add("4x2"); err == nil {
		t.Error(`Add("4x2") returned nil error, want rejection`)
	}
	// The rejected Add did not mutate the set.
	if got := s.Len(); got != 1 {
		t.Errorf("Len after rejected Add = %d, want 1 (no mutation)", got)
	}
}

// TC-151-11 companion: Normalize's contract directly (canonical numeric form; error on
// non-numeric; empty rejected).
func TestTC151_11_NormalizeContract(t *testing.T) {
	cases := []struct {
		in      string
		want    int64
		wantErr bool
	}{
		{"42", 42, false},
		{"042", 42, false},
		{" 42 ", 42, false},
		{"-7", -7, false},
		{"0", 0, false},
		{"", 0, true},
		{"  ", 0, true},
		{"abc", 0, true},
		{"4.2", 0, true},
		{"0x2a", 0, true},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q) err = nil, want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q) err = %v, want nil", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("Normalize(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

// TC-151-10 (store-level seeding/union proof): a second Persist against the same path
// after an independent Load+Add is additive — previously-persisted IDs survive. This is
// the store-level guarantee the assembly-level union test (cli package) relies on.
func TestTC151_10_SeedingIsAdditiveUnion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "union.json")

	// First "assembly": seed {42, 1001}.
	a := NewStore(path)
	if err := a.Load(); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	_ = a.Add("42")
	_ = a.Add("1001")
	if err := a.Persist(); err != nil {
		t.Fatalf("first Persist: %v", err)
	}

	// Second "assembly" against the SAME path with a DIFFERENT (empty) static list.
	// Load must bring the existing IDs back; a bare Persist must not drop them.
	b := NewStore(path)
	if err := b.Load(); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	// (empty static list — no Add calls)
	if err := b.Persist(); err != nil {
		t.Fatalf("second Persist: %v", err)
	}

	// Reload from a THIRD instance: the originally-seeded IDs must still be present.
	c := NewStore(path)
	if err := c.Load(); err != nil {
		t.Fatalf("third Load: %v", err)
	}
	for _, id := range []string{"42", "1001"} {
		if ok, _ := c.Contains(id); !ok {
			t.Errorf("union-seeded ID %s lost after re-assembly with empty static list", id)
		}
	}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2 (no destructive overwrite)", c.Len())
	}
}

// Remove deletes a normalized entry; a non-numeric ID is rejected.
func TestStoreRemove(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "s.json"))
	_ = s.Add("42")
	_ = s.Add("1001")
	if err := s.Remove("042"); err != nil { // cross-format remove
		t.Fatalf("Remove: %v", err)
	}
	if ok, _ := s.Contains("42"); ok {
		t.Error("42 still present after Remove(042)")
	}
	if s.Len() != 1 {
		t.Errorf("Len after Remove = %d, want 1", s.Len())
	}
	if err := s.Remove("nope"); err == nil {
		t.Error("Remove(non-numeric) returned nil error, want rejection")
	}
}
