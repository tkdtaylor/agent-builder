// Package sandbox defines the exec-sandbox run adapter seam used by trusted
// host-side code to start work inside an isolated execution box.
package sandbox

import (
	"errors"
	"strings"
	"time"
)

var (
	// ErrInvalidCommand means a request did not contain an executable command.
	ErrInvalidCommand = errors.New("sandbox: invalid command")

	// ErrNoFakeResponse means a fake runner was called more times than it was
	// configured to answer.
	ErrNoFakeResponse = errors.New("sandbox: no fake response")
)

// Runner is the exec-sandbox run() adapter seam.
type Runner interface {
	Run(Request) (Result, int, error)
}

// Request is the complete input to one contained command run.
type Request struct {
	Command  []string
	Worktree string
	Limits   Limits
	Tier     string    // "bubblewrap" (default) or "gvisor" — first-class seam as of ADR 035
	Wiring   RunWiring // vault/proxy wiring; zero value = ADR 035 empty wiring (no regression)
}

// RunWiring carries the vault token-brokering wiring from the trusted host into
// the execution box (ADR 036, task 066). The zero value produces the empty
// wiring that ADR 035 specified as the deferred default: no vault socket, no
// secret refs, no injection mode. When populated, exec-sandbox calls vault.inject
// per handle at spawn time and the egress proxy injects the credential — the
// sandbox never sees the plaintext.
type RunWiring struct {
	// VaultSocket is the Unix socket path of the running vault daemon.
	VaultSocket string
	// SecretRefs are the opaque vault handles to inject (never plaintext values).
	SecretRefs []string
	// InjectionMode is "proxy" when vault wiring is active; "" otherwise.
	InjectionMode string
}

// Limits is the typed resource and egress contract for contained runs.
type Limits struct {
	WallClockTimeout time.Duration
	MemoryBytes      int64
	CPUCount         int
	PidsLimit        int
	EgressAllowlist  []string
}

// Result is the structured output from a contained command run.
type Result struct {
	Stdout    string
	Stderr    string
	Duration  time.Duration
	SandboxID string   // Unique identifier for the sandbox instance (from exec-sandbox block)
	Tier      string   // Execution tier: "bubblewrap" or "gvisor" (from exec-sandbox block)
	Status    string   // Sandbox execution status: "clean", "timeout", etc. (from exec-sandbox block)
	Degraded  []string // Resource limits that were degraded (from exec-sandbox block)
}

// ValidateRequest checks the backend-independent request contract.
func ValidateRequest(req Request) error {
	if len(req.Command) == 0 || strings.TrimSpace(req.Command[0]) == "" {
		return ErrInvalidCommand
	}
	return nil
}

// FakeResponse is one deterministic response queued by FakeRunner.
type FakeResponse struct {
	Result   Result
	ExitCode int
	Err      error
}

// FakeRunner is an in-process Runner for tests. It performs no isolation.
type FakeRunner struct {
	responses []FakeResponse
	requests  []Request
}

// NewFakeRunner returns a fake Runner configured with ordered responses.
func NewFakeRunner(responses ...FakeResponse) *FakeRunner {
	copied := make([]FakeResponse, len(responses))
	copy(copied, responses)

	return &FakeRunner{
		responses: copied,
		requests:  make([]Request, 0, len(copied)),
	}
}

// Run records a valid request and returns the next queued deterministic response.
func (f *FakeRunner) Run(req Request) (Result, int, error) {
	if err := ValidateRequest(req); err != nil {
		return Result{}, 0, err
	}

	f.requests = append(f.requests, copyRequest(req))
	if len(f.responses) == 0 {
		return Result{}, 0, ErrNoFakeResponse
	}

	response := f.responses[0]
	f.responses = f.responses[1:]
	return response.Result, response.ExitCode, response.Err
}

// Requests returns the valid requests recorded by the fake runner.
func (f *FakeRunner) Requests() []Request {
	copied := make([]Request, len(f.requests))
	for i, req := range f.requests {
		copied[i] = copyRequest(req)
	}
	return copied
}

// CallCount returns the number of valid requests recorded by the fake runner.
func (f *FakeRunner) CallCount() int {
	return len(f.requests)
}

func copyRequest(req Request) Request {
	req.Command = append([]string(nil), req.Command...)
	req.Limits.EgressAllowlist = append([]string(nil), req.Limits.EgressAllowlist...)
	req.Wiring.SecretRefs = append([]string(nil), req.Wiring.SecretRefs...)
	return req
}
