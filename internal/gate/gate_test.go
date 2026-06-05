package gate

import (
	"errors"
	"reflect"
	"testing"
)

type fakeStep struct {
	name       string
	ok         bool
	output     string
	resultName string
	ran        bool
	repoPath   string
}

func (s *fakeStep) Name() string {
	return s.name
}

func (s *fakeStep) Run(repoPath string) StepResult {
	s.ran = true
	s.repoPath = repoPath
	return StepResult{
		Name:   s.resultName,
		OK:     s.ok,
		Output: s.output,
	}
}

func TestVerifyAggregatesPassingStepResultsInOrder(t *testing.T) {
	first := &fakeStep{name: "test", ok: true, output: "tests passed"}
	second := &fakeStep{name: "build", ok: true, output: "build passed"}
	g, err := New(first, second)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	verdict := g.Verify("/tmp/repo")

	if !verdict.OK {
		t.Fatal("Verify().OK = false, want true")
	}
	if len(verdict.Results) != 2 {
		t.Fatalf("len(Verify().Results) = %d, want 2", len(verdict.Results))
	}

	wantNames := []string{"test", "build"}
	gotNames := []string{verdict.Results[0].Name, verdict.Results[1].Name}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("result names = %v, want %v", gotNames, wantNames)
	}

	wantOutputs := []string{"tests passed", "build passed"}
	gotOutputs := []string{verdict.Results[0].Output, verdict.Results[1].Output}
	if !reflect.DeepEqual(gotOutputs, wantOutputs) {
		t.Fatalf("result outputs = %v, want %v", gotOutputs, wantOutputs)
	}

	for _, result := range verdict.Results {
		if !result.OK {
			t.Fatalf("%s OK = false, want true", result.Name)
		}
		if result.Duration < 0 {
			t.Fatalf("%s Duration = %v, want non-negative", result.Name, result.Duration)
		}
	}
}

func TestVerifyInvokesPluggableStepWithRepoPath(t *testing.T) {
	step := &fakeStep{
		name:       "custom",
		ok:         true,
		output:     "custom passed",
		resultName: "ignored-name",
	}
	g, err := New(step)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	verdict := g.Verify("/worktree")

	if !step.ran {
		t.Fatal("custom step did not run")
	}
	if step.repoPath != "/worktree" {
		t.Fatalf("repoPath = %q, want %q", step.repoPath, "/worktree")
	}
	if verdict.Results[0].Name != "custom" {
		t.Fatalf("result name = %q, want registered step name", verdict.Results[0].Name)
	}
}

func TestVerifyShortCircuitsOnFirstFailingStep(t *testing.T) {
	first := &fakeStep{name: "test", ok: true, output: "tests passed"}
	second := &fakeStep{name: "lint", ok: false, output: "lint failed"}
	third := &fakeStep{name: "scan", ok: true, output: "scan passed"}
	g, err := New(first, second, third)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	verdict := g.Verify("/tmp/repo")

	if verdict.OK {
		t.Fatal("Verify().OK = true, want false")
	}
	if !first.ran {
		t.Fatal("first step did not run")
	}
	if !second.ran {
		t.Fatal("failing step did not run")
	}
	if third.ran {
		t.Fatal("third step ran after blocking failure")
	}
	if len(verdict.Results) != 2 {
		t.Fatalf("len(Verify().Results) = %d, want 2", len(verdict.Results))
	}
	if verdict.Results[1].Name != "lint" || verdict.Results[1].OK {
		t.Fatalf("second result = %#v, want failing lint result", verdict.Results[1])
	}
}

func TestVerifyWithNoStepsIsOKWithEmptyResults(t *testing.T) {
	g, err := New()
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	first := g.Verify("/tmp/repo")
	second := g.Verify("/tmp/repo")

	if !first.OK || !second.OK {
		t.Fatalf("empty gate OK values = %v, %v; want both true", first.OK, second.OK)
	}
	if len(first.Results) != 0 || len(second.Results) != 0 {
		t.Fatalf("empty gate result lengths = %d, %d; want both 0", len(first.Results), len(second.Results))
	}
	if len(first.Results) > 0 && len(second.Results) > 0 && &first.Results[0] == &second.Results[0] {
		t.Fatal("Verify returned shared results backing storage")
	}
}

func TestNewRejectsInvalidSteps(t *testing.T) {
	tests := []struct {
		name    string
		steps   []Step
		wantErr error
	}{
		{
			name:    "nil",
			steps:   []Step{nil},
			wantErr: ErrNilStep,
		},
		{
			name:    "blank name",
			steps:   []Step{&fakeStep{name: " "}},
			wantErr: ErrBlankStepName,
		},
		{
			name: "duplicate name",
			steps: []Step{
				&fakeStep{name: "test"},
				&fakeStep{name: "test"},
			},
			wantErr: ErrDuplicateStepName,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, err := New(tt.steps...)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("New() error = %v, want %v", err, tt.wantErr)
			}
			if g != nil {
				t.Fatalf("New() gate = %#v, want nil", g)
			}
		})
	}
}

func TestGateHasNoSkipSurface(t *testing.T) {
	step := &fakeStep{name: "blocking", ok: true}
	g, err := New(step)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	gateType := reflect.TypeOf(g)
	if _, ok := gateType.MethodByName("Skip"); ok {
		t.Fatal("Gate exposes Skip method")
	}
	if _, ok := gateType.MethodByName("Bypass"); ok {
		t.Fatal("Gate exposes Bypass method")
	}
	verify, ok := gateType.MethodByName("Verify")
	if !ok {
		t.Fatal("Gate does not expose Verify")
	}
	if got := verify.Type.NumIn(); got != 2 {
		t.Fatalf("Verify input count = %d, want receiver plus repoPath only", got)
	}
}
