package recipe

// resetRegistry clears the global recipe registry for test isolation.
// This is a test helper function used in tests to ensure clean state between test cases.
func resetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]RecipeFactory)
}
