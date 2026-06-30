package orchestrator

import (
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/runtime"
)

// --- TC-129-04 ---------------------------------------------------------------
func TestWithRequireApprovalOptionSetsField(t *testing.T) {
	o := New(nil, nil, nil, runtime.Config{}, WithRequireApproval(false))
	if o.requireApproval {
		t.Errorf("expected requireApproval to be false, got true")
	}

	oDefault := New(nil, nil, nil, runtime.Config{})
	if !oDefault.requireApproval {
		t.Errorf("expected requireApproval to default to true, got false")
	}
}
