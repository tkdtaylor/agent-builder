package registry

import "sync"

// Catalog is an in-process registry of executor entries.
type Catalog struct {
	mu      sync.RWMutex
	entries map[string]RegistryEntry
	order   []string // stable, deterministic order
}

// NewCatalog creates a new empty catalog.
func NewCatalog() *Catalog {
	return &Catalog{
		entries: make(map[string]RegistryEntry),
		order:   make([]string, 0),
	}
}

// RegisterEntry adds an entry to the catalog.
// Panics if an entry with the same ID already exists.
func (c *Catalog) RegisterEntry(e RegistryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[e.ID]; exists {
		panic("duplicate registry entry ID: " + e.ID)
	}

	c.entries[e.ID] = e
	c.order = append(c.order, e.ID)
}

// LookupEntry retrieves an entry by ID.
// Returns (RegistryEntry{}, false) if the entry is not found.
func (c *Catalog) LookupEntry(id string) (RegistryEntry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if id == "" {
		return RegistryEntry{}, false
	}

	entry, exists := c.entries[id]
	return entry, exists
}

// UpdateEntry replaces an existing entry in place, preserving its position in
// the stable order. It is the seam the router uses to mutate an entry's
// router-owned availability/usage state (ADR 043: Usage and Availability are
// mutable state the router owns). Updating an entry whose ID is not present is a
// no-op — the router only updates entries it looked up from this catalog.
func (c *Catalog) UpdateEntry(e RegistryEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.entries[e.ID]; !exists {
		return
	}
	c.entries[e.ID] = e
}

// ListEntries returns all entries in stable, deterministic order.
func (c *Catalog) ListEntries() []RegistryEntry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]RegistryEntry, len(c.order))
	for i, id := range c.order {
		result[i] = c.entries[id]
	}
	return result
}
