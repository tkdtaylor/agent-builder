package supervisor_test

import (
	"context"
	"testing"

	"github.com/tkdtaylor/agent-builder/internal/supervisor"
)

// stubReporter is a minimal implementation of supervisor.Reporter for compile-time
// and behavioral testing.
type stubReporter struct {
	returnErr error
}

func (s stubReporter) Report(_ context.Context, _ string) error {
	return s.returnErr
}

// TestTC098_01_ReporterInterfaceSignature verifies that supervisor.Reporter exists
// with the exact signature Report(context.Context, string) error and is invokable.
// Satisfies TC-098-01.
func TestTC098_01_ReporterInterfaceSignature(t *testing.T) {
	// Compile-time assertion: stubReporter satisfies supervisor.Reporter.
	var r supervisor.Reporter = stubReporter{}

	// The method is invokable and returns the stub's nil error.
	if err := r.Report(context.Background(), "hello"); err != nil {
		t.Errorf("stubReporter.Report returned unexpected error: %v", err)
	}

	// Also confirm the interface is assignable (not just a method set match).
	var _ supervisor.Reporter = stubReporter{returnErr: nil}
}
