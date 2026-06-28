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
