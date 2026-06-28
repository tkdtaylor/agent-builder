package orchestrator

import "sync"

// MemoryPlanStore is the v1 in-memory PlanStore (ADR 046 §3). It holds plans
// awaiting approval in process memory for the duration of one goal's lifecycle.
// Task 084 swaps a durable, memory-guarded backend behind the PlanStore interface
// without changing the orchestrator.
//
// MemoryPlanStore is safe for concurrent use.
type MemoryPlanStore struct {
	mu    sync.Mutex
	plans map[string]Plan
}

// NewMemoryPlanStore constructs an empty in-memory plan store.
func NewMemoryPlanStore() *MemoryPlanStore {
	return &MemoryPlanStore{plans: make(map[string]Plan)}
}

// Put stores a plan keyed by its GoalID, overwriting any prior plan for that goal.
func (s *MemoryPlanStore) Put(plan Plan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.plans[plan.GoalID] = plan
}

// Get returns the plan held for goalID, and whether one was found.
func (s *MemoryPlanStore) Get(goalID string) (Plan, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	plan, ok := s.plans[goalID]
	return plan, ok
}

// Delete removes the plan held for goalID (a no-op if none is held).
func (s *MemoryPlanStore) Delete(goalID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.plans, goalID)
}
